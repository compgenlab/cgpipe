package runner

import (
	"os"
	"path/filepath"
	"sync"
)

// Cache memoizes file existence/mtime for the duration of an invocation. When a
// path is first looked up, its parent directory is stat'd once — on network
// filesystems this forces the client to refresh that directory's metadata, so
// subsequent file stats in it are current. A single Cache shared across the
// per-manifest runs means a common input (e.g. ref.fa) is stat'd once, not N×.
//
// It is safe under the manifest fan-out model where runs are independent (no run
// reads another run's freshly produced outputs).
type Cache struct {
	mu    sync.Mutex
	dirs  map[string]bool
	files map[string]fileStat
}

type fileStat struct {
	mtime  int64
	exists bool
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{dirs: map[string]bool{}, files: map[string]fileStat{}}
}

func (c *Cache) stat(path string) fileStat {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.files[path]; ok {
		return r
	}
	if d := filepath.Dir(path); !c.dirs[d] {
		_, _ = os.Stat(d) // refresh directory metadata (NFS), result unused
		c.dirs[d] = true
	}
	var r fileStat
	if fi, err := os.Stat(path); err == nil {
		r.exists = true
		r.mtime = fi.ModTime().UnixNano()
	}
	c.files[path] = r
	return r
}
