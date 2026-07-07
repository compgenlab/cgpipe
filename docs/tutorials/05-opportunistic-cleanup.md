# Tutorial 5: Opportunistic cleanup

The map-reduce in [Tutorial 4](04-map-reduce.md) leaves per-chromosome VCFs lying
around. We want to delete them — but *only* once the merge has succeeded, and
without ever breaking restarts. That's exactly what an **opportunistic job** is
for.

## Why not just `rm` in the merge body?

You could append `rm` to the merge recipe, but then the cleanup is tied to the
merge running. If the merge was already done on a previous run and is skipped this
time, the cleanup never happens. And if you delete a temp that staleness still
needs to reason about, you can confuse a later restart.

An opportunistic job sidesteps both problems. It:

- has **no outputs** (a leading `:` and a list of inputs),
- runs **after** the rest of the pipeline is submitted,
- **never forces** its inputs to be built,
- runs **only if** all its inputs are already available.

So it cleans up when there's something to clean up, and quietly does nothing
otherwise.

## The cleanup rule

Add this to `call.cgp`:

```
# Once the merged VCF exists, remove the per-chromosome temps.
: ${out}.vcf.gz @{parts} {{
    if [ -e ${out}.vcf.gz ]; then
% for v in parts {
        rm -f ${v}
% }
    fi
}}
```

The job lists the merged VCF *and* every temp as inputs, so cgpipe only schedules it
once those exist. The `% for` loop ([in-body control
flow](../05-Build_Targets.md#in-body-control-flow--lines)) emits one `rm` per part,
and the `if [ -e … ]` guard is belt-and-suspenders: the file is deleted only if the
merge really produced its output.

## Render it

```console
$ cgp -dr call.cgp --bam sample.bam --ref ref.fa --out sample
...
# ---- sample.vcf.gz ----
bcftools concat -O z -o sample.vcf.gz sample.1.vcf.gz sample.2.vcf.gz sample.3.vcf.gz

# ---- (opportunistic) ----
if [ -e sample.vcf.gz ]; then
rm -f sample.1.vcf.gz
rm -f sample.2.vcf.gz
rm -f sample.3.vcf.gz
fi
```

The cleanup is labeled `(opportunistic)` and comes last. If you re-run after the
temps are already gone, the job's inputs aren't all available, so cgpipe skips it
entirely — no error.

## A note on temp files and deletion

Marking an output `^` (temporary) documents *intent* — it tells cgpipe's staleness
logic to look through the file when it's absent. It does **not** give cgpipe
permission to delete anything; cgpipe never auto-removes files. Deletion is always
explicit and user-written, which is exactly this pattern. The two features work
together: `^` keeps restarts correct after you delete, and the opportunistic job
does the deleting.

## Next

- **[Tutorial 6: Shared `@pre` / `@post`](06-pre-post.md)** — add logging and timing
  to every job.

Reference → [Build Targets § Opportunistic jobs](../05-Build_Targets.md#opportunistic-jobs),
[§ Temporary outputs](../05-Build_Targets.md#temporary-outputs-).
