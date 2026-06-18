# cgpipe is pure Go (modernc.org/sqlite); no CGO, no C cross-toolchain.
# CGO_ENABLED=0 makes Go produce a statically linked binary even without
# musl, which is what we want for portable distribution.

CGO_ENABLED ?= 0
export CGO_ENABLED

# The repo lives under a parent go.work that does not list this module, which
# breaks plain `go build`/`go test` here. Build/test the module standalone.
# Override (GOWORK=/path/to/go.work make ...) if you really want a workspace.
GOWORK ?= off
export GOWORK

SOURCES := $(shell find . -name '*.go')

# Version resolution: delegated to scripts/version.sh. See that script
# for the rules; the short version is "exact tag → tag name; else
# next-patch-dev-<sha>; else fallback". Override on the command line
# (VERSION=foo make ...) if you need a specific string.
VERSION ?= $(shell ./scripts/version.sh)

LDFLAGS := -ldflags "-X github.com/compgenlab/cgpipe/internal/buildinfo.Version=$(VERSION)"

# Default target is just the host binary — fast iteration during dev.
# `make all` cross-builds every supported target; that's what CI runs
# and what release tarballs are built from.
# go env GOOS reports darwin, but our binaries use the macos label, so
# normalize it. Otherwise HOST_BINARY on macOS would be cgpipe.darwin_arm64,
# which has no rule, and `make spec` fails with "no target to build".
HOST_OS := $(subst darwin,macos,$(shell go env GOOS))
HOST_ARCH := $(shell go env GOARCH)
HOST_BINARY := bin/cgpipe.$(HOST_OS)_$(HOST_ARCH)

.DEFAULT_GOAL := $(HOST_BINARY)

all: \
	bin/cgpipe.linux_amd64 \
	bin/cgpipe.linux_arm64 \
	bin/cgpipe.macos_amd64 \
	bin/cgpipe.macos_arm64

bin/cgpipe.macos_amd64: go.mod go.sum $(SOURCES) | bin
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $@ ./cmd/cgpipe

bin/cgpipe.macos_arm64: go.mod go.sum $(SOURCES) | bin
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $@ ./cmd/cgpipe

bin/cgpipe.linux_amd64: go.mod go.sum $(SOURCES) | bin
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $@ ./cmd/cgpipe

bin/cgpipe.linux_arm64: go.mod go.sum $(SOURCES) | bin
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $@ ./cmd/cgpipe

bin:
	mkdir -p bin

clean:
	rm -rf bin

run:
	go run $(LDFLAGS) ./cmd/cgpipe

test:
	go test ./...

# spec runs the standalone .cgp fixture suite (tests/run.sh) against the freshly
# built host binary. `make test` also covers it via the Go wrapper (TestFixtures
# in ./tests); this target is the human-facing entry point — pass FLAGS=-v for
# per-failure diffs or FLAGS=-u to refresh golden files.
spec: $(HOST_BINARY)
	CGPIPE_BIN=$(abspath $(HOST_BINARY)) ./tests/run.sh $(FLAGS)

.PHONY: all clean run test spec
