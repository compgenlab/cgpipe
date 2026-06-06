# Reference preparation

Before any alignment or variant calling, a reference genome needs its indexes —
the `bwa` index, a `samtools` `.fai`, and a sequence dictionary. This recipe builds
all three; the other recipes depend on them existing.

> Requires: `bwa`, `samtools`, `gatk`.

```
#!/usr/bin/env cgp
#
# Build the indexes that downstream alignment/calling need from a reference FASTA.
#
# Options:
#     --ref FILE   reference genome FASTA (e.g. genome.fa)

if !ref { print "ERROR: --ref required"; exit 1 }

# bwa index (produces ref.fa.bwt and friends; track the .bwt as the sentinel)
${ref}.bwt: ${ref} {{
    name = "bwa-index"
    mem  = "8G"
    --
    bwa index ${input}
}}

# samtools faidx -> ref.fa.fai
${ref}.fai: ${ref} {{
    name = "faidx"
    --
    samtools faidx ${input}
}}

# GATK/Picard sequence dictionary -> ref.dict
${ref.sub("\\.fa(sta)?$", "")}.dict: ${ref} {{
    name = "seq-dict"
    --
    gatk CreateSequenceDictionary -R ${input}
}}

# `all` ties the three together as the default goal.
all: ${ref}.bwt ${ref}.fai ${ref.sub("\\.fa(sta)?$", "")}.dict
@default: all
```

## What it shows

- **Independent prerequisite targets.** The three indexes don't depend on each
  other, so on a scheduler they submit in parallel. Each downstream recipe can
  depend on the one it needs (e.g. an aligner on `${ref}.bwt`) and cgp will build
  it first.
- **An `all` aggregator** as the [`@default`](../06-Reserved_Targets.md#the-default-goal-default):
  requesting nothing builds all three.
- **A method call in the output name.** `${ref.sub("\\.fa(sta)?$", "")}.dict` turns
  `genome.fa` into `genome.dict` — GATK's expected dictionary name.

## Run it

```sh
cgp reference-preparation.cgp --ref genome.fa | bash    # local
cgp -r slurm reference-preparation.cgp --ref genome.fa  # on a cluster
```

## Adapt it

- **STAR** (for the [RNA-seq recipe](rnaseq-quantification.md)) needs its own index
  instead of bwa — add a target that runs `STAR --runMode genomeGenerate`.
- Add known-sites/VCF resources here if your BQSR step needs them downloaded and
  indexed.

See [Build Targets](../05-Build_Targets.md) and
[Reserved Targets](../06-Reserved_Targets.md).
