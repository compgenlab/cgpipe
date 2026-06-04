package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheExistsAndMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	os.WriteFile(path, []byte("x"), 0o644)

	c := NewCache()
	s := c.stat(path)
	if !s.exists || s.mtime == 0 {
		t.Fatalf("stat = %+v, want existing with mtime", s)
	}
	if m := c.stat(filepath.Join(dir, "nope")); m.exists {
		t.Fatal("missing file reported as existing")
	}
}

func TestCacheMemoizes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	os.WriteFile(path, []byte("x"), 0o644)

	c := NewCache()
	first := c.stat(path)

	// Change the file's mtime; the cache must keep returning the first value.
	future := time.Now().Add(time.Hour)
	os.Chtimes(path, future, future)

	if again := c.stat(path); again.mtime != first.mtime {
		t.Fatalf("cache not memoized: %d vs %d", again.mtime, first.mtime)
	}
}
