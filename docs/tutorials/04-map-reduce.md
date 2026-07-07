# Tutorial 4: Map-reduce across chromosomes

The classic scatter-gather: do the work in parallel pieces, then combine. Here we
call variants on each chromosome independently and merge the results. This
introduces **dynamic target generation** (a `for` loop that emits targets), an
**accumulator list**, and **temporary outputs**.

## The script

`call.cgp`:

```
#!/usr/bin/env cgp
#
# Per-chromosome variant calling with a merge.
#
# Options:
#     --bam FILE    indexed BAM
#     --ref FILE    reference FASTA
#     --out PREFIX  output prefix (produces <prefix>.vcf.gz)

if !bam { print "ERROR: --bam required"; exit 1 }
if !ref { print "ERROR: --ref required"; exit 1 }
if !out { print "ERROR: --out required"; exit 1 }

chroms = "1 2 3".split(" ")
parts = []

for c in chroms {
    parts += "${out}.${c}.vcf.gz"
    ^${out}.${c}.vcf.gz: ${bam} ${ref} {{
        job.name = "call-chr${c}"
        --
        bcftools mpileup -r chr${c} -f ${ref} ${bam} \
            | bcftools call -mv -O z -o ${output}
    }}
}

${out}.vcf.gz: @{parts} {{
    job.name = "merge"
    --
    bcftools concat -O z -o ${output} ${input}
}}
@default: ${out}.vcf.gz
```

How it works:

- **The `for` loop is cgpipe code at the top level**, so it runs at evaluation time
  and *emits one target per chromosome*. This is dynamic generation — the number of
  jobs follows the data, not the script.
- **`parts += …`** accumulates the per-chromosome output names into a list as we
  go. (`parts = []` starts it empty.)
- **`^${out}.${c}.vcf.gz`** marks each per-chromosome VCF as
  [temporary](../05-Build_Targets.md#temporary-outputs-) — an intermediate that
  only exists to feed the merge.
- **The merge target** lists `@{parts}` as its inputs. `@{…}` expands the list into
  separate input items, so the merge depends on every chromosome.

## Render it

```console
$ cgp -dr call.cgp --bam sample.bam --ref ref.fa --out sample
#!/usr/bin/env bash
set -euo pipefail

# ---- sample.1.vcf.gz ----
bcftools mpileup -r chr1 -f ref.fa sample.bam \
| bcftools call -mv -O z -o sample.1.vcf.gz

# ---- sample.2.vcf.gz ----
bcftools mpileup -r chr2 -f ref.fa sample.bam \
| bcftools call -mv -O z -o sample.2.vcf.gz

# ---- sample.3.vcf.gz ----
bcftools mpileup -r chr3 -f ref.fa sample.bam \
| bcftools call -mv -O z -o sample.3.vcf.gz

# ---- sample.vcf.gz ----
bcftools concat -O z -o sample.vcf.gz sample.1.vcf.gz sample.2.vcf.gz sample.3.vcf.gz
```

Three independent calling jobs, then one merge that depends on all of them. On a
scheduler (`-r slurm`) the three would submit in parallel and the merge would be
wired to wait for them — cgpipe derives the dependency from the `@{parts}` inputs.

## `${...}` vs `@{...}` — the key distinction

This pattern hinges on two sigils that look similar but do opposite things:

- **`@{parts}`** (in a *declaration*) expands into **separate items** — three
  distinct inputs to the merge.
- **`${input}`** (in a *body*) joins them into **one space-separated value** —
  exactly what `bcftools concat` wants as its argument list.

So you accumulate with `@{}` at the rule level and consume with `${}` in the shell.

## Next

- **[Tutorial 5: Opportunistic cleanup](05-opportunistic-cleanup.md)** — delete those
  per-chromosome temps safely once the merge succeeds.

Reference → [Build Targets § List expansion](../05-Build_Targets.md#list-expansion-list),
[§ Temporary outputs](../05-Build_Targets.md#temporary-outputs-).
