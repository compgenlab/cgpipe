# Seed the ledger: a first submission records the array's tasks (1001_1..1001_3)
# as the owners of calls.chr{1,2,3}.vcf. (Its scheduler calls go to a scratch
# capture dir, not the one under test.)
touch aligned.bam
"$CGP" -r batchq array-reuse.cgp >/dev/null 2>&1 || true
