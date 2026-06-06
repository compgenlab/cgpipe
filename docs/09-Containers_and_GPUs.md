# Containers and GPUs

A target's body can run **inside a container** without changing the body itself —
you write ordinary shell, and cgp wraps it in `docker run` or `singularity exec`.
GPUs are requested with a single setting that drives both the scheduler and the
container engine.

## Enabling container wrapping

Wrapping happens when **both** are set:

- **`cgp.container.engine`** — `docker`, `singularity`, or `apptainer` (in config
  or the script). Unset disables all wrapping.
- **`container = "<image>"`** — a per-target directive naming the image. A target
  with no `container` runs unwrapped even when an engine is configured.

```
cgp.container.engine = "docker"

out.bam: in.bam {{
    container = "biocontainers/samtools:1.9"
    --
    samtools sort ${input} > ${output}
}}
```

cgp writes the rendered body to a temp file and runs it inside the image,
**bind-mounting the working directory and the temp body, setting the working
directory, and (for Docker) mapping the host user** automatically:

```bash
__cgp_body=$(mktemp "/tmp/cgp-body.XXXXXX")
trap 'rm -f "$__cgp_body"' EXIT
cat > "$__cgp_body" <<'__CGP_BODY__'
samtools sort in.bam > out.bam
__CGP_BODY__

docker run --rm \
    -v /tmp:/tmp \
    -v __WORKDIR__:__WORKDIR__ \
    -w __WORKDIR__ \
    -u $(id -u):$(id -g) \
    biocontainers/samtools:1.9 \
    sh "$__cgp_body"
```

The body never mentions Docker — you could drop the `container` line and run it
bare. (A target with an engine set but **no** image renders unwrapped, exactly as
if containers weren't configured.)

## Singularity / Apptainer

The same target with `engine = "singularity"` uses `singularity exec` with `-B`
binds and `--pwd`:

```
cgp.container.engine = "singularity"

out.bam: in.bam {{
    container = "docker://biocontainers/bwa:0.7.17"
    --
    bwa ${input} > ${output}
}}
```
```bash
singularity exec \
    -B /tmp:/tmp \
    -B __WORKDIR__:__WORKDIR__ \
    --pwd __WORKDIR__ \
    docker://biocontainers/bwa:0.7.17 \
    sh "$__cgp_body"
```

## Tuning the invocation

Extra settings shape the engine command. Each is available globally as
`cgp.container.<name>` and/or per target as `container.<name>`:

| Setting | Purpose |
|---------|---------|
| `container.bind` | Extra bind mounts (list) |
| `container.env` | Environment variables to pass through |
| `container.opts` | Raw extra flags for the engine |
| `container.body_dir` | Where the temp body file is written/mounted (default `/tmp`) |
| `container.shell` | Shell used to run the body inside the image (default `sh`) |
| `cgp.container.user_map` | Docker: add `-u $(id -u):$(id -g)` (default on) |

```
out.bam: in.bam {{
    container       = "img:1"
    container.bind  = ["/data", "/refs"]
    container.env   = ["SAMPLE"]
    container.opts  = ["--shm-size=1g"]
    container.shell = "bash"
    --
    run ${input} > ${output}
}}
```
```bash
docker run --rm \
    -v /tmp:/tmp \
    -v __WORKDIR__:__WORKDIR__ \
    -v /data:/data \
    -v /refs:/refs \
    -w __WORKDIR__ \
    -u $(id -u):$(id -g) \
    -e SAMPLE \
    --shm-size=1g \
    img:1 \
    bash "$__cgp_body"
```

Each setting maps to its engine flag: `bind` → `-v`/`-B`, `env` → `-e`, `opts`
verbatim, `shell` as the in-image interpreter.

## GPUs

`gpu` requests GPUs and drives **both** layers at once:

```
train.model: data.tfrecord {{
    gpu = 2
    --
    train.py --data ${input} --out ${output}
}}
```

- `gpu = true` → one GPU; `gpu = N` → N; `gpu = false`/unset → none. Global default:
  `cgp.gpu`.
- On a scheduler it emits the resource request — on SLURM, `#SBATCH --gres=gpu:2`.
- Inside a container it adds the engine's GPU flag (Docker `--gpus`,
  Singularity/Apptainer `--nv`).

So one `gpu = 2` both asks the scheduler for two GPUs and exposes them to the
container — no separate flags to keep in sync.

## Next

- **[Tutorial 9: Containerized jobs](tutorials/09-containers.md)** — a worked
  container pipeline.
- **[Configuration Reference](13-Configuration_Reference.md)** — set the engine
  site-wide.

Reference → [language-spec.md §12](language-spec.md#12-containers-and-gpus).
