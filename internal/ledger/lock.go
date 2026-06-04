package ledger

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// lockWait is how long acquireLock waits for a held lock before giving up.
// A package var so tests can shorten it.
var lockWait = 30 * time.Second

// lockHandle holds an acquired ledger lockfile.
type lockHandle struct{ path string }

// acquireLock takes an exclusive lock via atomic O_CREAT|O_EXCL creation of the
// lockfile — reliable on NFS, unlike fcntl/flock. A lock held by a dead process
// on this host is reclaimed immediately; a lock held elsewhere is waited on, then
// reported (clear it with `cgp ledger unlock` if the holder really crashed).
func acquireLock(path string) (*lockHandle, error) {
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			host, _ := os.Hostname()
			fmt.Fprintf(f, "%s\n%d\n%d\n", host, os.Getpid(), time.Now().Unix())
			f.Close()
			return &lockHandle{path: path}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if stealable(path) {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("ledger is locked by %s; if that process is gone, run `cgp ledger unlock`", ownerDesc(path))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (l *lockHandle) release() {
	if l != nil {
		os.Remove(l.path)
	}
}

// stealable reports whether the lock is held by a dead process on this host.
func stealable(path string) bool {
	host, pid, ok := readOwner(path)
	if !ok {
		return false
	}
	myHost, _ := os.Hostname()
	if host != myHost {
		return false // can't check liveness on another host
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	return p.Signal(syscall.Signal(0)) != nil // signal error => process gone
}

func readOwner(path string) (host string, pid int, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", 0, false
	}
	lines := strings.SplitN(strings.TrimSpace(string(b)), "\n", 3)
	if len(lines) < 2 {
		return "", 0, false
	}
	pid, err = strconv.Atoi(strings.TrimSpace(lines[1]))
	if err != nil {
		return "", 0, false
	}
	return strings.TrimSpace(lines[0]), pid, true
}

func ownerDesc(path string) string {
	if host, pid, ok := readOwner(path); ok {
		return fmt.Sprintf("%s (pid %d)", host, pid)
	}
	return "another process"
}

// Unlock removes the lockfile for the ledger at path. For manually clearing a
// stale lock left by a crashed process on another host.
func Unlock(path string) error {
	err := os.Remove(path + ".lock")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
