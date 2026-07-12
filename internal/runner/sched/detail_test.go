package sched

import "testing"

func TestSlurmDetailFromScontrol(t *testing.T) {
	line := "JobId=42 JobName=align JobState=RUNNING Reason=None " +
		"SubmitTime=2024-01-02T02:59:00 StartTime=2024-01-02T03:00:00 EndTime=Unknown " +
		"Partition=general NodeList=node01 NumCPUs=4 ReqMem=8G " +
		"UserId=alice(1001) Account=lab WorkDir=/work StdOut=/work/o.log StdErr=/work/e.log " +
		"RunTime=00:05:00 TimeLimit=1-00:00:00"
	d, ok := slurmDetailFromScontrol(line)
	if !ok {
		t.Fatal("expected ok")
	}
	checks := map[string]string{
		"native": d.NativeState, "state": d.State, "reason": d.Reason,
		"part": d.Partition, "nodes": d.Nodes, "cpus": d.CPUs, "mem": d.MemReq,
		"user": d.User, "acct": d.Account, "wd": d.WorkDir, "out": d.StdoutPath,
		"elapsed": d.Elapsed, "limit": d.TimeLimit,
	}
	want := map[string]string{
		"native": "RUNNING", "state": "running", "reason": "", // "None" -> cleaned
		"part": "general", "nodes": "node01", "cpus": "4", "mem": "8G",
		"user": "alice", "acct": "lab", "wd": "/work", "out": "/work/o.log",
		"elapsed": "00:05:00", "limit": "1-00:00:00",
	}
	for k, w := range want {
		if checks[k] != w {
			t.Errorf("%s = %q, want %q", k, checks[k], w)
		}
	}
	if d.StartTime == 0 || d.SubmitTime == 0 {
		t.Errorf("expected start/submit times parsed, got start=%d submit=%d", d.StartTime, d.SubmitTime)
	}
	if d.EndTime != 0 {
		t.Errorf("EndTime should be 0 for Unknown, got %d", d.EndTime)
	}
}

// A cancelled SLURM job normalizes to "cancelled", not "failed".
func TestSlurmDetailCancelled(t *testing.T) {
	d, ok := slurmDetailFromScontrol("JobState=CANCELLED Reason=None ExitCode=0:15")
	if !ok {
		t.Fatal("expected ok")
	}
	if d.State != "cancelled" {
		t.Errorf("state = %q, want cancelled", d.State)
	}
	if !d.HasExit || d.ExitCode != 0 {
		t.Errorf("exit = %d (has=%v), want 0", d.ExitCode, d.HasExit)
	}
}

func TestParseQstatF(t *testing.T) {
	out := "Job Id: 12345.cluster\n" +
		"    Job_Name = myjob\n" +
		"    job_state = R\n" +
		"    Resource_List.ncpus = 8\n" +
		"    Variable_List = PBS_O_HOME=/home/a,\n\tPBS_O_LANG=en_US\n"
	m := parseQstatF(out)
	if m["job_state"] != "R" {
		t.Errorf("job_state = %q, want R", m["job_state"])
	}
	if m["Resource_List.ncpus"] != "8" {
		t.Errorf("ncpus = %q, want 8", m["Resource_List.ncpus"])
	}
	// The tab-continued value must be unfolded onto one line.
	if m["Variable_List"] != "PBS_O_HOME=/home/a,PBS_O_LANG=en_US" {
		t.Errorf("Variable_List = %q (continuation not unfolded)", m["Variable_List"])
	}
}

func TestNormState(t *testing.T) {
	cases := []struct{ base, native, want string }{
		{"running", "RUNNING", "running"},
		{"failed", "CANCELLED", "cancelled"}, // overridden
		{"", "", "unknown"},
		{"done", "COMPLETED", "done"},
	}
	for _, c := range cases {
		if got := normState(c.base, c.native, "CANCELLED"); got != c.want {
			t.Errorf("normState(%q,%q) = %q, want %q", c.base, c.native, got, c.want)
		}
	}
}
