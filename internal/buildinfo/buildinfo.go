// Package buildinfo carries version information stamped into the binaries at
// build time. The release workflow overrides Version via -ldflags; a plain
// `go build` leaves it as "dev".
package buildinfo

// Version is the cgpipe version string. Override with:
//
//	go build -ldflags "-X github.com/compgenlab/cgpipe/internal/buildinfo.Version=<v>"
var Version = "dev"
