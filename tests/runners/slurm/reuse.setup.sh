# Seed the ledger: a first submission records job 1001 as the owner of a.bam.
# (Its scheduler calls go to the harness's scratch capture, not the one under test.)
"$CGPIPE" -r slurm reuse.cgp >/dev/null 2>&1 || true
