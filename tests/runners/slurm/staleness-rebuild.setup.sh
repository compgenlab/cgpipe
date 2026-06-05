# output first, then input -> input is newer -> stale -> rebuild.
touch out.bam
sleep 1
touch in.fastq
