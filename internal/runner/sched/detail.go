package sched

import (
	"strconv"
	"strings"
	"time"
)

// JobDetail is a rich, normalized snapshot of one job's live status, produced by
// a scheduler's Detail probe and consumed by `cgp status`. Zero-valued fields are
// "not reported" — the command layer omits them from JSON. State uses the 6-value
// `cgp status` vocabulary (queued|running|done|failed|cancelled|unknown), a
// superset of the 4-value report vocabulary that Scheduler.State returns.
type JobDetail struct {
	NativeState string // raw scheduler word (e.g. "PENDING", "R", "PROXYQUEUED")
	State       string // normalized: queued|running|done|failed|cancelled|unknown
	Reason      string // pending/blocked reason (e.g. "Priority", "Dependency")
	ExitCode    int
	HasExit     bool // ExitCode is meaningful (job finished and reported one)

	SubmitTime int64 // Unix seconds; 0 = unknown
	StartTime  int64
	EndTime    int64
	Elapsed    string
	TimeLimit  string

	Nodes     string // exec node list
	Partition string
	CPUs      string
	MemReq    string
	MemUsed   string

	Account    string
	User       string
	WorkDir    string
	StdoutPath string
	StderrPath string
}

// normState lifts a 4-value report state to the 6-value `cgp status` vocabulary:
// a native word matching one of cancelledWords becomes "cancelled" (overriding
// the base, which folds cancellation into "failed"); an empty base becomes
// "unknown".
func normState(base, native string, cancelledWords ...string) string {
	for _, w := range cancelledWords {
		if strings.HasPrefix(native, w) {
			return "cancelled"
		}
	}
	if base == "" {
		return "unknown"
	}
	return base
}

// firstField returns the first whitespace-delimited field of s, or "".
func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// firstNonEmpty returns the first non-empty argument, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// cleanField blanks scheduler placeholders that stand in for "no value".
func cleanField(s string) string {
	switch strings.TrimSpace(s) {
	case "", "(null)", "None", "Unknown", "N/A":
		return ""
	}
	return strings.TrimSpace(s)
}

// stripParen removes a trailing "(...)" from a value, e.g. SLURM's
// "UserId=alice(1001)" -> "alice".
func stripParen(s string) string {
	if i := strings.IndexByte(s, '('); i >= 0 {
		return s[:i]
	}
	return s
}

// parseScontrolKV splits `scontrol -o show job` output (one line of
// space-separated key=value tokens) into a map. Later keys win.
func parseScontrolKV(s string) map[string]string {
	m := map[string]string{}
	for _, tok := range strings.Fields(s) {
		if kv := strings.SplitN(tok, "=", 2); len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	return m
}

// parseQstatF parses a `qstat -f` attribute block ("  key = value" lines) into a
// map, first unfolding qstat's tab-indented line-continuation wraps.
func parseQstatF(s string) map[string]string {
	s = strings.ReplaceAll(s, "\n\t", "")
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if kv := strings.SplitN(line, " = ", 2); len(kv) == 2 {
			m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return m
}

// --- SLURM ------------------------------------------------------------------

// slurmTime layout used by scontrol/sacct (local time).
const slurmTime = "2006-01-02T15:04:05"

func parseSlurmTime(s string) int64 {
	s = cleanField(s)
	if s == "" {
		return 0
	}
	if t, err := time.ParseInLocation(slurmTime, s, time.Local); err == nil {
		return t.Unix()
	}
	return 0
}

// slurmExit parses SLURM's "<code>:<signal>" ExitCode into its numeric code.
func slurmExit(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// slurmDetail queries a SLURM job, preferring `scontrol -o show job` and falling
// back to `sacct` for jobs that have aged out of the controller.
func slurmDetail(id string) (JobDetail, bool) {
	if out, _, err := probe("scontrol", "-o", "show", "job", id); err == nil {
		if d, ok := slurmDetailFromScontrol(string(out)); ok {
			return d, true
		}
	}
	return slurmDetailFromSacct(id)
}

func slurmDetailFromScontrol(out string) (JobDetail, bool) {
	m := parseScontrolKV(out)
	word := m["JobState"]
	if word == "" {
		return JobDetail{}, false
	}
	d := JobDetail{
		NativeState: word,
		State:       normState(slurmStateFor(word), word, "CANCELLED"),
		Reason:      cleanField(m["Reason"]),
		SubmitTime:  parseSlurmTime(m["SubmitTime"]),
		StartTime:   parseSlurmTime(m["StartTime"]),
		EndTime:     parseSlurmTime(m["EndTime"]),
		Elapsed:     cleanField(m["RunTime"]),
		TimeLimit:   cleanField(m["TimeLimit"]),
		Nodes:       cleanField(m["NodeList"]),
		Partition:   cleanField(m["Partition"]),
		CPUs:        cleanField(m["NumCPUs"]),
		MemReq:      cleanField(firstNonEmpty(m["ReqMem"], m["MinMemoryNode"], m["MinMemoryCPU"])),
		Account:     cleanField(m["Account"]),
		User:        cleanField(stripParen(m["UserId"])),
		WorkDir:     cleanField(m["WorkDir"]),
		StdoutPath:  cleanField(m["StdOut"]),
		StderrPath:  cleanField(m["StdErr"]),
	}
	if n, ok := slurmExit(m["ExitCode"]); ok {
		d.ExitCode, d.HasExit = n, true
	}
	return d, true
}

// sacctFields is the column order requested from sacct; keep it in sync with the
// index constants below.
var sacctFields = []string{
	"JobID", "State", "ExitCode", "Submit", "Start", "End", "Elapsed",
	"NodeList", "Partition", "ReqCPUS", "ReqMem", "MaxRSS", "Account", "User", "Timelimit",
}

func slurmDetailFromSacct(id string) (JobDetail, bool) {
	out, _, err := probe("sacct", "-P", "-n", "-j", id, "--format="+strings.Join(sacctFields, ","))
	if err != nil {
		return JobDetail{}, false
	}
	var d JobDetail
	found := false
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) < len(sacctFields) {
			continue
		}
		col := func(name string) string {
			for i, n := range sacctFields {
				if n == name {
					return f[i]
				}
			}
			return ""
		}
		// MaxRSS is reported on the ".batch"/".extern" step rows, not the primary
		// row, so scan every row for it.
		if mr := cleanField(col("MaxRSS")); mr != "" && d.MemUsed == "" {
			d.MemUsed = mr
		}
		// The primary row (JobID == id, no ".step" suffix) carries state/timing.
		if col("JobID") != id {
			continue
		}
		found = true
		word := firstField(col("State")) // "CANCELLED by 1001" -> "CANCELLED"
		d.NativeState = word
		d.State = normState(slurmStateFor(word), word, "CANCELLED")
		d.SubmitTime = parseSlurmTime(col("Submit"))
		d.StartTime = parseSlurmTime(col("Start"))
		d.EndTime = parseSlurmTime(col("End"))
		d.Elapsed = cleanField(col("Elapsed"))
		d.TimeLimit = cleanField(col("Timelimit"))
		d.Nodes = cleanField(col("NodeList"))
		d.Partition = cleanField(col("Partition"))
		d.CPUs = cleanField(col("ReqCPUS"))
		d.MemReq = cleanField(col("ReqMem"))
		d.Account = cleanField(col("Account"))
		d.User = cleanField(col("User"))
		if n, ok := slurmExit(col("ExitCode")); ok {
			d.ExitCode, d.HasExit = n, true
		}
	}
	return d, found
}

// --- PBS/Torque -------------------------------------------------------------

// parsePBSTime accepts both an epoch integer and PBS's ctime string
// ("Mon Jan _2 15:04:05 2006"); returns 0 when neither parses.
func parsePBSTime(s string) int64 {
	s = cleanField(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", s, time.Local); err == nil {
		return t.Unix()
	}
	return 0
}

func pbsDetail(id string) (JobDetail, bool) {
	out, _, err := probe("qstat", "-f", id)
	if err != nil {
		return JobDetail{}, false
	}
	m := parseQstatF(string(out))
	word := m["job_state"]
	if word == "" {
		return JobDetail{}, false
	}
	d := JobDetail{
		NativeState: word,
		State:       normState(pbsStateFor(word), word),
		Elapsed:     cleanField(m["resources_used.walltime"]),
		TimeLimit:   cleanField(m["Resource_List.walltime"]),
		MemUsed:     cleanField(m["resources_used.mem"]),
		MemReq:      cleanField(firstNonEmpty(m["Resource_List.mem"], m["Resource_List.pmem"])),
		CPUs:        cleanField(firstNonEmpty(m["Resource_List.ncpus"], m["Resource_List.nodect"], m["Resource_List.nodes"])),
		Nodes:       cleanField(m["exec_host"]),
		Partition:   cleanField(m["queue"]),
		Account:     cleanField(m["Account_Name"]),
		User:        cleanField(m["Job_Owner"]),
		StdoutPath:  cleanField(m["Output_Path"]),
		StderrPath:  cleanField(m["Error_Path"]),
		SubmitTime:  parsePBSTime(firstNonEmpty(m["qtime"], m["ctime"])),
		StartTime:   parsePBSTime(m["stime"]),
	}
	if n, err := strconv.Atoi(strings.TrimSpace(m["Exit_status"])); err == nil {
		d.ExitCode, d.HasExit = n, true
		d.EndTime = parsePBSTime(m["mtime"]) // mtime approximates completion once exited
	}
	return d, true
}

// --- SGE --------------------------------------------------------------------

// parseSGEColon parses `qstat -j` output ("key:   value" lines) into a map,
// replacing spaces in the key with underscores ("hard resource_list" ->
// "hard_resource_list").
func parseSGEColon(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ReplaceAll(strings.TrimSpace(kv[0]), " ", "_")
		if key != "" {
			m[key] = strings.TrimSpace(kv[1])
		}
	}
	return m
}

// parseSGEWhitespace parses `qacct -j` output ("key<spaces>value" lines) into a
// map. The first whitespace-delimited field is the key; the remainder is the value.
func parseSGEWhitespace(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "==============================================================" {
			continue
		}
		m[f[0]] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), f[0]))
	}
	return m
}

func sgeDetail(id string) (JobDetail, bool) {
	// A listed job (queued/running/error) still has a live state code from bare
	// qstat; enrich it from `qstat -j`.
	if word := sgeStatus(id); word != "" {
		d := JobDetail{NativeState: word, State: normState(sgeStateFor(word), word)}
		if out, _, err := probe("qstat", "-j", id); err == nil {
			m := parseSGEColon(string(out))
			d.User = cleanField(m["owner"])
			d.WorkDir = cleanField(m["sge_o_workdir"])
			d.Account = cleanField(m["account"])
			d.MemReq = cleanField(m["hard_resource_list"])
			d.StdoutPath = cleanField(m["stdout_path_list"])
			d.StderrPath = cleanField(m["stderr_path_list"])
		}
		return d, true
	}
	// A finished job has dropped off qstat; recover it from accounting.
	if out, _, err := probe("qacct", "-j", id); err == nil {
		m := parseSGEWhitespace(string(out))
		if len(m) == 0 {
			return JobDetail{}, false
		}
		d := JobDetail{
			Nodes:     cleanField(m["hostname"]),
			Partition: cleanField(m["qname"]),
			User:      cleanField(m["owner"]),
			Account:   cleanField(m["account"]),
			MemUsed:   cleanField(m["maxvmem"]),
			Elapsed:   cleanField(m["ru_wallclock"]),
		}
		if n, err := strconv.Atoi(firstField(m["exit_status"])); err == nil {
			d.ExitCode, d.HasExit = n, true
			if n == 0 {
				d.NativeState, d.State = "done", "done"
			} else {
				d.NativeState, d.State = "failed", "failed"
			}
		} else {
			d.State = "unknown"
		}
		return d, true
	}
	return JobDetail{}, false
}

// --- BatchQ -----------------------------------------------------------------

func batchqDetail(id string) (JobDetail, bool) {
	word := batchqStatus(id)
	if word == "" {
		return JobDetail{}, false
	}
	d := JobDetail{
		NativeState: word,
		State:       normState(batchqStateFor(word), word, "CANCELED"),
	}
	if end, ok := batchqEndTime(id); ok {
		d.EndTime = end
	}
	return d, true
}
