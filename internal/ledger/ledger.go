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

	_ "modernc.org/sqlite"
)

// Ledger is a handle to a single ledger database (single-writer).
type Ledger struct{ db *sql.DB }

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

// Open opens (creating if needed) the ledger at path.
func Open(path string) (*Ledger, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("ledger schema: %w", err)
	}
	return &Ledger{db: db}, nil
}

func (l *Ledger) Close() error { return l.db.Close() }

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
