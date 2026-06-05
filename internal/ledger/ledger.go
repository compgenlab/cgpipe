// Package ledger is the optional, SQLite-backed job ledger. It records which
// job last produced (owns) each output file, plus each job's inputs and
// dependency edges — and nothing else. It stores no job state (the scheduler
// owns liveness) and no file metadata (the filesystem owns staleness). Its core
// use is cross-run: when a pipeline is re-run while jobs are still queued, the
// owning job id is reused so dependents wait on it instead of resubmitting.
package ledger

import (
	"database/sql"
	"fmt"
	"io"
	"strings"

	_ "modernc.org/sqlite"
)

// Ledger is a handle to a single ledger database. Access is serialized across
// processes by an NFS-safe lockfile (see lock.go), enforcing single-writer.
type Ledger struct {
	db   *sql.DB
	lock *lockHandle
}

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
    job_id      TEXT PRIMARY KEY,
    run_id      TEXT,
    name        TEXT,
    pipeline    TEXT,
    working_dir TEXT,
    user        TEXT,
    submit_time INTEGER,
    start_time  INTEGER,
    end_time    INTEGER,
    return_code INTEGER
);
CREATE INDEX IF NOT EXISTS jobs_by_run ON jobs(run_id);

CREATE TABLE IF NOT EXISTS output_owner (
    path   TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES jobs(job_id)
);

CREATE TABLE IF NOT EXISTS job_outputs (
    job_id  TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
    path    TEXT NOT NULL,
    is_temp INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (job_id, path)
);
CREATE INDEX IF NOT EXISTS job_outputs_by_path ON job_outputs(path);

CREATE TABLE IF NOT EXISTS job_inputs (
    job_id TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
    path   TEXT NOT NULL,
    PRIMARY KEY (job_id, path)
);
CREATE TABLE IF NOT EXISTS job_deps (
    job_id TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
    dep_id TEXT NOT NULL,
    PRIMARY KEY (job_id, dep_id)
);
CREATE TABLE IF NOT EXISTS job_settings (
    job_id TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
    key    TEXT NOT NULL,
    value  TEXT
);
CREATE TABLE IF NOT EXISTS job_src (
    job_id TEXT NOT NULL REFERENCES jobs(job_id) ON DELETE CASCADE,
    lineno INTEGER NOT NULL,
    line   TEXT,
    PRIMARY KEY (job_id, lineno)
);
`

// Open opens (creating if needed) the ledger at path, taking an exclusive
// cross-process lockfile first (released by Close). Because the lockfile already
// guarantees a single writer, SQLite's own (NFS-unreliable) file locking is
// disabled with nolock=1.
func Open(path string) (*Ledger, error) {
	lock, err := acquireLock(path + ".lock")
	if err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&nolock=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		lock.release()
		return nil, err
	}
	db.SetMaxOpenConns(1) // single connection
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		lock.release()
		return nil, fmt.Errorf("ledger schema: %w", err)
	}
	return &Ledger{db: db, lock: lock}, nil
}

// OpenRead opens the ledger read-only WITHOUT taking the lockfile, so a status
// reader (e.g. the HTML report) never blocks a running pipeline that holds the
// write lock. It runs no DDL; if the database doesn't exist yet it errors.
func OpenRead(path string) (*Ledger, error) {
	dsn := "file:" + path + "?mode=ro&_pragma=busy_timeout(2000)&nolock=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Ledger{db: db}, nil
}

// Close closes the database and releases the lockfile (if held).
func (l *Ledger) Close() error {
	err := l.db.Close()
	if l.lock != nil {
		l.lock.release()
	}
	return err
}

// Job is one recorded submission.
type Job struct {
	JobID      string
	RunID      string
	Name       string
	Pipeline   string
	WorkingDir string
	User       string
	SubmitTime int64
	Outputs    []string
	Temp       map[string]bool
	Inputs     []string
	Deps       []string
	Script     string            // rendered job body (searchable; stored as job_src lines)
	Settings   map[string]string // per-job settings (mem, procs, …)
}

// Record stores a submitted job and claims its outputs (last job wins).
func (l *Ledger) Record(j Job) error {
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO jobs(job_id, run_id, name, pipeline, working_dir, user, submit_time)
		 VALUES(?,?,?,?,?,?,?)`,
		j.JobID, j.RunID, j.Name, j.Pipeline, j.WorkingDir, j.User, j.SubmitTime); err != nil {
		return err
	}
	for _, o := range j.Outputs {
		temp := 0
		if j.Temp[o] {
			temp = 1
		}
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO job_outputs(job_id, path, is_temp) VALUES(?,?,?)`,
			j.JobID, o, temp); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO output_owner(path, job_id) VALUES(?,?)
			 ON CONFLICT(path) DO UPDATE SET job_id = excluded.job_id`,
			o, j.JobID); err != nil {
			return err
		}
	}
	for _, in := range j.Inputs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO job_inputs(job_id, path) VALUES(?,?)`, j.JobID, in); err != nil {
			return err
		}
	}
	for _, d := range j.Deps {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO job_deps(job_id, dep_id) VALUES(?,?)`, j.JobID, d); err != nil {
			return err
		}
	}
	// settings / src have no/lineno PKs; clear then re-insert so a re-recorded
	// job doesn't accumulate stale rows.
	if _, err := tx.Exec(`DELETE FROM job_settings WHERE job_id = ?`, j.JobID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM job_src WHERE job_id = ?`, j.JobID); err != nil {
		return err
	}
	for k, v := range j.Settings {
		if _, err := tx.Exec(`INSERT INTO job_settings(job_id, key, value) VALUES(?,?,?)`, j.JobID, k, v); err != nil {
			return err
		}
	}
	if strings.TrimSpace(j.Script) != "" {
		for i, line := range strings.Split(strings.TrimRight(j.Script, "\n"), "\n") {
			if _, err := tx.Exec(`INSERT INTO job_src(job_id, lineno, line) VALUES(?,?,?)`, j.JobID, i+1, line); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// OwnerOf returns the job id that currently owns (last produced) path, if any.
func (l *Ledger) OwnerOf(path string) (string, bool, error) {
	var id string
	err := l.db.QueryRow(`SELECT job_id FROM output_owner WHERE path = ?`, path).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// Vacuum drops every job that no longer owns any output (cascading its rows).
// The last owner of each path survives, even if it failed.
func (l *Ledger) Vacuum() error {
	_, err := l.db.Exec(`DELETE FROM jobs WHERE job_id NOT IN (SELECT job_id FROM output_owner)`)
	return err
}

// CountJobs returns the number of jobs recorded (for diagnostics/tests).
func (l *Ledger) CountJobs() (int, error) {
	var n int
	err := l.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n)
	return n, err
}

// Filter selects jobs in Search. Empty fields are ignored; set fields are ANDed.
// Name/Input/Output/Grep are substring (contains) matches; ID is exact.
type Filter struct {
	ID     string // exact job id
	Name   string // job name contains
	Input  string // some input path contains
	Output string // some output path contains
	Grep   string // some job-script line contains
}

// Search returns the ids of jobs matching the filter, ordered by submit time.
func (l *Ledger) Search(f Filter) ([]string, error) {
	var clauses []string
	var args []any
	if f.ID != "" {
		clauses = append(clauses, "j.job_id = ?")
		args = append(args, f.ID)
	}
	if f.Name != "" {
		clauses = append(clauses, "instr(j.name, ?) > 0")
		args = append(args, f.Name)
	}
	if f.Input != "" {
		clauses = append(clauses, "EXISTS(SELECT 1 FROM job_inputs i WHERE i.job_id=j.job_id AND instr(i.path, ?) > 0)")
		args = append(args, f.Input)
	}
	if f.Output != "" {
		clauses = append(clauses, "EXISTS(SELECT 1 FROM job_outputs o WHERE o.job_id=j.job_id AND instr(o.path, ?) > 0)")
		args = append(args, f.Output)
	}
	if f.Grep != "" {
		clauses = append(clauses, "EXISTS(SELECT 1 FROM job_src s WHERE s.job_id=j.job_id AND instr(s.line, ?) > 0)")
		args = append(args, f.Grep)
	}
	q := "SELECT j.job_id FROM jobs j"
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY j.submit_time, j.job_id"
	rows, err := l.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Dump writes the recorded jobs to w as the key/value TSV joblog format —
// "<jobid>\t<KEY>\t<value>" per line (SETTING adds a fourth column). With only
// non-nil, just those job ids are written; otherwise every job (by submit time).
func (l *Ledger) Dump(w io.Writer, only []string) error {
	onlySet := map[string]bool{}
	for _, id := range only {
		onlySet[id] = true
	}
	// child tables, grouped by job id
	outs, err := l.groupOutputs()
	if err != nil {
		return err
	}
	ins, err := l.groupCol(`SELECT job_id, path FROM job_inputs`)
	if err != nil {
		return err
	}
	deps, err := l.groupCol(`SELECT job_id, dep_id FROM job_deps`)
	if err != nil {
		return err
	}
	src, err := l.groupCol(`SELECT job_id, line FROM job_src ORDER BY job_id, lineno`)
	if err != nil {
		return err
	}
	settings, err := l.groupPairs(`SELECT job_id, key, value FROM job_settings ORDER BY job_id, key`)
	if err != nil {
		return err
	}

	rows, err := l.db.Query(`SELECT job_id, run_id, name, pipeline, working_dir, user,
		submit_time, start_time, end_time, return_code FROM jobs ORDER BY submit_time, job_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var run, name, pipeline, wd, user sql.NullString
		var submit, start, end, ret sql.NullInt64
		if err := rows.Scan(&id, &run, &name, &pipeline, &wd, &user, &submit, &start, &end, &ret); err != nil {
			return err
		}
		if len(onlySet) > 0 && !onlySet[id] {
			continue
		}
		kv := func(key, val string) {
			if val != "" {
				fmt.Fprintf(w, "%s\t%s\t%s\n", id, key, val)
			}
		}
		kvN := func(key string, n sql.NullInt64) {
			if n.Valid {
				fmt.Fprintf(w, "%s\t%s\t%d\n", id, key, n.Int64)
			}
		}
		kv("PIPELINE", pipeline.String)
		kv("WORKINGDIR", wd.String)
		kv("RUNID", run.String)
		kv("NAME", name.String)
		kv("USER", user.String)
		kvN("SUBMIT", submit)
		kvN("START", start)
		kvN("END", end)
		kvN("RETCODE", ret)
		for _, d := range deps[id] {
			kv("DEP", d)
		}
		for _, o := range outs[id] {
			if o.temp {
				kv("TEMP", o.path)
			} else {
				kv("OUTPUT", o.path)
			}
		}
		for _, in := range ins[id] {
			kv("INPUT", in)
		}
		for _, s := range src[id] {
			fmt.Fprintf(w, "%s\tSRC\t%s\n", id, s) // SRC kept even when blank, to preserve the script
		}
		for _, p := range settings[id] {
			fmt.Fprintf(w, "%s\tSETTING\t%s\t%s\n", id, p[0], p[1])
		}
	}
	return rows.Err()
}

type outRow struct {
	path string
	temp bool
}

func (l *Ledger) groupOutputs() (map[string][]outRow, error) {
	rows, err := l.db.Query(`SELECT job_id, path, is_temp FROM job_outputs ORDER BY job_id, path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string][]outRow{}
	for rows.Next() {
		var id, path string
		var temp int
		if err := rows.Scan(&id, &path, &temp); err != nil {
			return nil, err
		}
		m[id] = append(m[id], outRow{path, temp != 0})
	}
	return m, rows.Err()
}

func (l *Ledger) groupCol(query string) (map[string][]string, error) {
	rows, err := l.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string][]string{}
	for rows.Next() {
		var id, v string
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		m[id] = append(m[id], v)
	}
	return m, rows.Err()
}

func (l *Ledger) groupPairs(query string) (map[string][][2]string, error) {
	rows, err := l.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string][][2]string{}
	for rows.Next() {
		var id, k string
		var v sql.NullString
		if err := rows.Scan(&id, &k, &v); err != nil {
			return nil, err
		}
		m[id] = append(m[id], [2]string{k, v.String})
	}
	return m, rows.Err()
}
