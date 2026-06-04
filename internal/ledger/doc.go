// Package ledger is the optional, SQLite-backed job ledger (cgpipe's "joblog",
// renamed). It records which job owns (last produced) which output file, plus
// each job's inputs and dependency edges — and nothing else. It stores no job
// state (the scheduler owns liveness) and no file metadata (the filesystem
// owns staleness). The core query is "who owns output path X?".
//
// Backed by modernc.org/sqlite (pure Go, no CGO) — added as a dependency when
// this package is implemented in Phase 1. Single-writer per ledger.
//
// See docs/language-spec.md §10 (the ledger) for the schema and semantics.
package ledger
