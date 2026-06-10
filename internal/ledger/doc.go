// Package ledger is the optional, append-only job ledger (cgpipe's "joblog",
// renamed). It records which job owns (last produced) which output file, plus
// each job's inputs and dependency edges — and nothing else. It stores no job
// state (the scheduler owns liveness) and no file metadata (the filesystem owns
// staleness). The core query is "who owns output path X?".
//
// Storage is a directory of newline-delimited JSON ("JSONL") files. Each writer
// process appends complete job records to its OWN file
// (<host>-<pid>-<nanos>-<n>.jsonl); files are never shared between writers and no
// cross-process lock is taken. Readers fold every file into an in-memory view —
// last write wins, ordered by each record's (time, host, pid, seq) key — to
// answer ownership queries. Vacuum compacts the directory into a single
// snapshot.jsonl. This layout is robust on shared filesystems (NFS/Lustre),
// where a single mmap'd database file and its advisory locks are not.
//
// See docs/language-spec.md §10 (the ledger) for the format and semantics.
package ledger
