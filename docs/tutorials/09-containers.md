# Tutorial 9: Containerized jobs

Run a job inside a container without rewriting the job. You add one directive
naming the image; cgpipe wraps the body in `docker run` (or `singularity exec`),
mounts the right paths, and runs it.

## Turn wrapping on

Two things enable wrapping: an **engine** and a per-target **image**.

`pipeline.cgp`:

```
#!/usr/bin/env cgp

cgp.container.engine = "docker"

out.bam: in.bam {{
    job.container = "biocontainers/samtools:1.9"
    --
    samtools sort ${input} > ${output}
}}
@default: out.bam
```

The engine is usually set once in `~/.cgp/config`
([Configuration Reference](../14-Configuration_Reference.md)) so individual
pipelines only name images.

## Render it

```console
$ cgp -dr pipeline.cgp
#!/usr/bin/env bash
set -euo pipefail

# ---- out.bam ----
__cgpipe_body=$(mktemp "/tmp/cgpipe-body.XXXXXX")
trap 'rm -f "$__cgpipe_body"' EXIT
cat > "$__cgpipe_body" <<'__CGP_BODY__'
samtools sort in.bam > out.bam
__CGP_BODY__

docker run --rm \
    -v /tmp:/tmp \
    -v __WORKDIR__:__WORKDIR__ \
    -w __WORKDIR__ \
    -u $(id -u):$(id -g) \
    biocontainers/samtools:1.9 \
    sh "$__cgpipe_body"
```

cgpipe wrote your body to a temp file and ran it inside the image, bind-mounting the
working directory (so `in.bam`/`out.bam` resolve) and mapping your user id (so the
output isn't owned by root). The body itself is unchanged — drop the `job.container`
line and it runs bare.

## Per-target images

Different jobs can use different images. Only the targets that name a `job.container`
are wrapped; others run on the host even with an engine configured:

```
align.bam: reads.fq ref.fa {{
    job.container = "biocontainers/bwa:0.7.17"
    --
    bwa mem ${ref} ${reads} > ${output}
}}

stats.txt: align.bam {{
    samtools flagstat ${input} > ${output}   # no container → runs on the host
}}
```

## Extra mounts, env, and flags

When a job needs a reference directory or an environment variable, add the
container settings:

```
out.bam: in.bam {{
    job.container      = "img:1"
    job.container.bind = ["/data", "/refs"]
    job.container.env  = ["SAMPLE"]
    job.container.opts = ["--shm-size=1g"]
    --
    run ${input} > ${output}
}}
```

`bind` adds `-v` mounts, `env` passes `-e SAMPLE` through, and `opts` are handed to
the engine verbatim. See
[Containers & GPUs](../10-Containers_and_GPUs.md#tuning-the-invocation) for the full
list.

## Running on GPUs

One setting requests GPUs for both the scheduler and the engine:

```
train.model: data.tfrecord {{
    job.container = "tensorflow/tensorflow:latest-gpu"
    job.gpu       = 2
    --
    train.py --data ${input} --out ${output}
}}
```

On SLURM this emits `#SBATCH --gres=gpu:2`; inside Docker it adds `--gpus`, and
inside Singularity/Apptainer `--nv`. You don't keep two GPU flags in sync — `job.gpu`
drives both.

## Next

- **[Tutorial 10: Custom job submission](10-custom-templates.md)** — tailor
  submissions to your cluster.

Reference → [Containers and GPUs](../10-Containers_and_GPUs.md),
[language-spec.md §12](../language-spec.md#12-containers-and-gpus).
