# Tutorial 2: gzip with a wildcard

One rule that compresses *any* file, plus an aggregator that asks for several at
once. This introduces wildcards and the `all:` convention.

## The script

`gzip.cgp`:

```
#!/usr/bin/env cgp
#
# Compress FASTQ files with gzip.

%.fastq.gz: %.fastq {{
    gzip -c ${input} > ${output}
}}

all: a.fastq.gz b.fastq.gz c.fastq.gz
@default: all
```

The wildcard rule reads: *"to make anything ending in `.fastq.gz`, take the
matching `.fastq` and gzip it."* The `%` matches the **stem**, which is reused on
the input side. One rule covers every file.

`all:` is a [bodyless aggregator](../05-Build_Targets.md#bodyless-aggregator-targets):
it has no recipe of its own; requesting it just builds its inputs. `@default: all`
makes that the thing built when you run `cgp gzip.cgp` with no target.

## Render it

```console
$ cgp -dr gzip.cgp
#!/usr/bin/env bash
set -euo pipefail

# ---- a.fastq.gz ----
gzip -c a.fastq > a.fastq.gz

# ---- b.fastq.gz ----
gzip -c b.fastq > b.fastq.gz

# ---- c.fastq.gz ----
gzip -c c.fastq > c.fastq.gz
```

The single wildcard rule expanded into one job per file, each with its own stem
substituted. cgp emits only the jobs whose outputs are missing or out of date, so
re-running after `a.fastq.gz` exists skips that one.

## Using the stem

The matched stem is available as `${stem}` inside the body — handy when the output
name needs more than a suffix swap:

```
%.sorted.bam: %.bam {{
    samtools sort -o ${output} ${input}
    echo "sorted sample ${stem}"
}}
```

For `sampleA.sorted.bam`, `${stem}` is `sampleA`.

## Next

- **[Tutorial 3: Resources and flags](03-resources-and-flags.md)** — give jobs CPUs
  and memory, and add optional flags.

Reference → [Build Targets § Wildcards](../05-Build_Targets.md#wildcards),
[language-spec.md §7.3](../language-spec.md#73-wildcards).
