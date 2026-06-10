package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// snapshotName is the compacted log written by Vacuum. It is read like any other
// log file, just first (it holds the oldest, folded baseline).
const snapshotName = "snapshot.jsonl"

// maxLine caps a single JSONL record (a job, including its rendered script). Far
// above any realistic job body; a longer line is treated as corrupt and skipped.
const maxLine = 16 * 1024 * 1024

// Ledger is a handle to a single ledger directory. A directory of append-only
// JSONL files; each writer process appends to its own file, so writers never
// share a file and no cross-process lock is taken (see doc.go). Safe for
// concurrent use within a process.
type Ledger struct {
	dir string

	mu   sync.Mutex
	st   *state   // in-memory folded view; updated on each Record
	f    *os.File // this process's append file (created lazily on first Record); nil read-only
	seq  int64    // per-record monotonic counter within this handle
	ro   bool
	host string
	pid  int
}

// Job is one recorded submission. The json tags define the on-disk record shape.
type Job struct {
	JobID      string            `json:"job_id"`
	RunID      string            `json:"run_id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Pipeline   string            `json:"pipeline,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	User       string            `json:"user,omitempty"`
	SubmitTime int64             `json:"submit_time,omitempty"`
	Outputs    []string          `json:"outputs,omitempty"`
	Temp       map[string]bool   `json:"temp,omitempty"`
	Inputs     []string          `json:"inputs,omitempty"`
	Deps       []string          `json:"deps,omitempty"`
	Script     string            `json:"script,omitempty"` // rendered job body (searchable)
	Settings   map[string]string `json:"settings,omitempty"`
}

// record is one line in a JSONL file: a Job plus the ordering fields that let
// readers fold many files (and re-records) into a single last-write-wins view.
type record struct {
	Ts   int64  `json:"ts"`   // write time, Unix nanoseconds
	Seq  int64  `json:"seq"`  // per-writer monotonic counter
	Host string `json:"host"` // writer hostname
	Pid  int    `json:"pid"`  // writer pid
	Job
}

func (r record) ord() ordKey { return ordKey{ts: r.Ts, host: r.Host, pid: r.Pid, seq: r.Seq} }

// ordKey is the total order over records: by time, then by writer identity, then
// by per-writer sequence. Within one writer (ts ties) seq gives exact append
// order; across writers ties break deterministically on host/pid.
type ordKey struct {
	ts   int64
	host string
	pid  int
	seq  int64
}

func (a ordKey) after(b ordKey) bool {
	if a.ts != b.ts {
		return a.ts > b.ts
	}
	if a.host != b.host {
		return a.host > b.host
	}
	if a.pid != b.pid {
		return a.pid > b.pid
	}
	return a.seq > b.seq
}

// state is the folded view of every record read from a ledger directory.
type state struct {
	jobs  map[string]jobEntry   // latest record per job id
	owner map[string]ownerEntry // path -> the job that last produced it
}

type jobEntry struct {
	job Job
	ord ordKey
}

type ownerEntry struct {
	jobID string
	ord   ordKey
}

// apply folds one record into the state, keeping the latest by ordering key.
func (st *state) apply(r record) {
	o := r.ord()
	if e, ok := st.jobs[r.JobID]; !ok || o.after(e.ord) {
		st.jobs[r.JobID] = jobEntry{job: r.Job, ord: o}
	}
	for _, p := range r.Outputs {
		if e, ok := st.owner[p]; !ok || o.after(e.ord) {
			st.owner[p] = ownerEntry{jobID: r.JobID, ord: o}
		}
	}
}

// Open opens (creating the directory if needed) the ledger at path for writing.
// No cross-process lock is taken; the writer appends only to its own file.
func Open(path string) (*Ledger, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("ledger %s: %w", path, err)
	}
	st, err := load(path)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "localhost"
	}
	return &Ledger{dir: path, st: st, host: host, pid: os.Getpid()}, nil
}

// OpenRead opens the ledger read-only. It folds the directory once into memory;
// later writes by other processes are not observed. Errors if path is missing.
func OpenRead(path string) (*Ledger, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("ledger %s is not a directory", path)
	}
	st, err := load(path)
	if err != nil {
		return nil, err
	}
	return &Ledger{dir: path, st: st, ro: true}, nil
}

// Close closes this process's append file (if any). The folded view is dropped.
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		err := l.f.Close()
		l.f = nil
		return err
	}
	return nil
}

// load reads and folds every *.jsonl file in dir into a fresh state.
func load(dir string) (*state, error) {
	st := &state{jobs: map[string]jobEntry{}, owner: map[string]ownerEntry{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, err
	}
	var logs []string
	snap := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		switch {
		case n == snapshotName:
			snap = true
		case strings.HasSuffix(n, ".jsonl"):
			logs = append(logs, n)
		}
	}
	sort.Strings(logs)
	// Read the snapshot (folded baseline) first, then per-process logs. Order is
	// not required for correctness — folding uses each record's ordering key —
	// but it keeps the common path predictable.
	if snap {
		logs = append([]string{snapshotName}, logs...)
	}
	for _, n := range logs {
		if err := foldFile(st, filepath.Join(dir, n)); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// foldFile folds one JSONL file into st. A malformed line (e.g. a torn final
// record from a crashed writer) is skipped, never fatal — the rest of the
// ledger stays readable.
func foldFile(st *state, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // removed by a concurrent Vacuum
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var r record
		if err := json.Unmarshal(line, &r); err != nil {
			continue // tolerate a partial/corrupt line
		}
		st.apply(r)
	}
	return nil // ignore scanner errors (an over-long final line is treated as torn)
}

// fileCounter disambiguates append-file names created within one process.
var fileCounter int64

// newFile creates this process's append file: <host>-<pid>-<nanos>-<n>.jsonl.
func (l *Ledger) newFile() (*os.File, error) {
	n := atomic.AddInt64(&fileCounter, 1)
	name := fmt.Sprintf("%s-%d-%d-%d.jsonl", sanitize(l.host), l.pid, time.Now().UnixNano(), n)
	return os.OpenFile(filepath.Join(l.dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// Record appends a submitted job and claims its outputs (last job wins). The
// record is fsync'd before returning.
func (l *Ledger) Record(j Job) error {
	if l.ro {
		return errors.New("ledger opened read-only")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		f, err := l.newFile()
		if err != nil {
			return err
		}
		l.f = f
	}
	l.seq++
	r := record{Ts: time.Now().UnixNano(), Seq: l.seq, Host: l.host, Pid: l.pid, Job: j}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := l.f.Write(b); err != nil {
		return err
	}
	if err := l.f.Sync(); err != nil {
		return err
	}
	l.st.apply(r)
	return nil
}

// OwnerOf returns the job id that currently owns (last produced) path, if any.
func (l *Ledger) OwnerOf(path string) (string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.st.owner[path]; ok {
		return e.jobID, true, nil
	}
	return "", false, nil
}

// CountJobs returns the number of distinct jobs recorded (for diagnostics/tests).
func (l *Ledger) CountJobs() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.st.jobs), nil
}

// Vacuum compacts the directory: it re-folds every file from disk, writes the
// jobs that still own at least one output to a fresh snapshot.jsonl (atomic
// temp+rename), and removes the per-process logs it folded. The last owner of
// each path survives, even if it failed. Logs still being appended by a live
// local process are left in place (reclaimed by a later vacuum once idle).
func (l *Ledger) Vacuum() error {
	if l.ro {
		return errors.New("ledger opened read-only")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	// List the directory BEFORE writing, so we only ever remove files we have
	// fully folded — never one created by a writer after this point.
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return err
	}
	// Fold a fresh, authoritative view from disk (includes other processes).
	st, err := load(l.dir)
	if err != nil {
		return err
	}
	live := map[string]bool{}
	for _, e := range st.owner {
		live[e.jobID] = true
	}

	// Write the compacted snapshot atomically.
	ids := make([]string, 0, len(live))
	for id := range st.jobs {
		if live[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return lessJob(st.jobs[ids[i]].job, st.jobs[ids[j]].job) })

	tmp := filepath.Join(l.dir, snapshotName+".tmp")
	tf, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(tf)
	for _, id := range ids {
		e := st.jobs[id]
		r := record{Ts: e.ord.ts, Seq: e.ord.seq, Host: e.ord.host, Pid: e.ord.pid, Job: e.job}
		b, err := json.Marshal(r)
		if err != nil {
			tf.Close()
			os.Remove(tmp)
			return err
		}
		w.Write(b)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		tf.Close()
		os.Remove(tmp)
		return err
	}
	if err := tf.Sync(); err != nil {
		tf.Close()
		os.Remove(tmp)
		return err
	}
	if err := tf.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(l.dir, snapshotName)); err != nil {
		os.Remove(tmp)
		return err
	}

	// Our own append file is fully captured by the fresh load (each Record syncs)
	// and thus by the snapshot if still live — drop it so its orphans don't
	// reappear; a later Record opens a new file.
	if l.f != nil {
		l.f.Close()
		os.Remove(l.f.Name())
		l.f = nil
	}
	// Remove the per-process logs we folded. Skip the snapshot itself and any log
	// still owned by a live local process. Files created after the ReadDir above
	// aren't in `entries`, so they are safe by construction.
	for _, e := range entries {
		n := e.Name()
		if n == snapshotName || n == snapshotName+".tmp" || !strings.HasSuffix(n, ".jsonl") {
			continue
		}
		if logIsLiveLocal(n, l.host) {
			continue
		}
		os.Remove(filepath.Join(l.dir, n))
	}

	// Re-fold from the compacted directory so our in-memory view matches disk
	// (the snapshot plus any sibling logs left behind).
	st, err = load(l.dir)
	if err != nil {
		return err
	}
	l.st = st
	return nil
}

// Unlock is retained for the `cgp ledger unlock` subcommand but is now a no-op:
// the JSONL backend takes no cross-process lock, so there is nothing to clear.
func Unlock(path string) error {
	return nil
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
	l.mu.Lock()
	defer l.mu.Unlock()
	var ids []string
	for _, j := range l.sortedJobs() {
		if f.ID != "" && j.JobID != f.ID {
			continue
		}
		if f.Name != "" && !strings.Contains(j.Name, f.Name) {
			continue
		}
		if f.Input != "" && !anyContains(j.Inputs, f.Input) {
			continue
		}
		if f.Output != "" && !anyContains(j.Outputs, f.Output) {
			continue
		}
		if f.Grep != "" && !strings.Contains(j.Script, f.Grep) {
			continue
		}
		ids = append(ids, j.JobID)
	}
	return ids, nil
}

// Dump writes the recorded jobs to w as the key/value TSV joblog format —
// "<jobid>\t<KEY>\t<value>" per line (SETTING adds a fourth column). With only
// non-nil, just those job ids are written; otherwise every job (by submit time).
func (l *Ledger) Dump(w io.Writer, only []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	onlySet := map[string]bool{}
	for _, id := range only {
		onlySet[id] = true
	}
	for _, j := range l.sortedJobs() {
		if len(onlySet) > 0 && !onlySet[j.JobID] {
			continue
		}
		id := j.JobID
		kv := func(key, val string) {
			if val != "" {
				fmt.Fprintf(w, "%s\t%s\t%s\n", id, key, val)
			}
		}
		kv("PIPELINE", j.Pipeline)
		kv("WORKINGDIR", j.WorkingDir)
		kv("RUNID", j.RunID)
		kv("NAME", j.Name)
		kv("USER", j.User)
		if j.SubmitTime != 0 {
			fmt.Fprintf(w, "%s\tSUBMIT\t%d\n", id, j.SubmitTime)
		}
		for _, d := range j.Deps {
			kv("DEP", d)
		}
		outs := append([]string(nil), j.Outputs...)
		sort.Strings(outs)
		for _, o := range outs {
			if j.Temp[o] {
				kv("TEMP", o)
			} else {
				kv("OUTPUT", o)
			}
		}
		for _, in := range j.Inputs {
			kv("INPUT", in)
		}
		if strings.TrimSpace(j.Script) != "" {
			for _, line := range strings.Split(strings.TrimRight(j.Script, "\n"), "\n") {
				fmt.Fprintf(w, "%s\tSRC\t%s\n", id, line) // SRC kept even when blank, to preserve the script
			}
		}
		keys := make([]string, 0, len(j.Settings))
		for k := range j.Settings {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "%s\tSETTING\t%s\t%s\n", id, k, j.Settings[k])
		}
	}
	return nil
}

// sortedJobs returns every folded job, ordered by submit time then job id.
func (l *Ledger) sortedJobs() []Job {
	jobs := make([]Job, 0, len(l.st.jobs))
	for _, e := range l.st.jobs {
		jobs = append(jobs, e.job)
	}
	sort.Slice(jobs, func(i, j int) bool { return lessJob(jobs[i], jobs[j]) })
	return jobs
}

func lessJob(a, b Job) bool {
	if a.SubmitTime != b.SubmitTime {
		return a.SubmitTime < b.SubmitTime
	}
	return a.JobID < b.JobID
}

func anyContains(haystack []string, sub string) bool {
	for _, s := range haystack {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// sanitize makes a hostname safe for use in a filename (keeps '-', which the
// log-name parser accounts for).
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ' ', '\t', '\n':
			return '_'
		}
		return r
	}, s)
}

// parseLogName splits a <host>-<pid>-<nanos>-<n>.jsonl name from the right (the
// host may itself contain '-'), returning the writer's host and pid.
func parseLogName(name string) (host string, pid int, ok bool) {
	s, found := strings.CutSuffix(name, ".jsonl")
	if !found {
		return "", 0, false
	}
	parts := strings.Split(s, "-")
	if len(parts) < 4 {
		return "", 0, false
	}
	tail := parts[len(parts)-3:] // pid, nanos, counter — all integers
	p, err := strconv.Atoi(tail[0])
	if err != nil {
		return "", 0, false
	}
	if _, err := strconv.Atoi(tail[1]); err != nil {
		return "", 0, false
	}
	if _, err := strconv.Atoi(tail[2]); err != nil {
		return "", 0, false
	}
	return strings.Join(parts[:len(parts)-3], "-"), p, true
}

// logIsLiveLocal reports whether name belongs to a still-running process on this
// host (so Vacuum must not remove it).
func logIsLiveLocal(name, myHost string) bool {
	host, pid, ok := parseLogName(name)
	if !ok || host != sanitize(myHost) {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
