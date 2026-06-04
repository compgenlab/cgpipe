package ledger

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLockExcludesSecondOpener(t *testing.T) {
	old := lockWait
	lockWait = 200 * time.Millisecond
	defer func() { lockWait = old }()

	path := filepath.Join(t.TempDir(), "l.db")
	l1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}

	// A second open must fail while the first holds the lock (held by this live
	// process, so not stealable -> waits then times out).
	if _, err := Open(path); err == nil {
		t.Fatal("second open should fail while lock is held")
	}

	l1.Close()
	l2, err := Open(path)
	if err != nil {
		t.Fatalf("open after close should succeed: %v", err)
	}
	l2.Close()
}

func TestStaleLockFromDeadPidReclaimed(t *testing.T) {
	old := lockWait
	lockWait = 200 * time.Millisecond
	defer func() { lockWait = old }()

	dir := t.TempDir()
	path := filepath.Join(dir, "l.db")

	// produce a definitely-dead pid on this host
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	content := fmt.Sprintf("%s\n%d\n%d\n", host, dead.Process.Pid, time.Now().Unix())
	if err := os.WriteFile(path+".lock", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := Open(path) // should reclaim the stale lock
	if err != nil {
		t.Fatalf("open should reclaim stale lock from dead pid: %v", err)
	}
	l.Close()
}

func TestUnlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.db")
	os.WriteFile(path+".lock", []byte("somehost\n12345\n0\n"), 0o644)
	if err := Unlock(path); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatal("lockfile should be gone after Unlock")
	}
	// unlocking when there's no lockfile is not an error
	if err := Unlock(path); err != nil {
		t.Fatalf("unlock (absent) should be a no-op: %v", err)
	}
}
