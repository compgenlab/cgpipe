package spectest

import "testing"

// §12 With an engine and a per-target image both set, the body is wrapped to run
// inside the container; input/output paths are bind-mounted automatically.
func TestContainerWrapDocker(t *testing.T) {
	got := render(t, `cgp.container.engine = "docker"
out.bam: in.bam {{
    container = "biocontainers/samtools:1.9"
    --
    samtools sort ${input} > ${output}
}}`)
	mustContain(t, got, "docker run --rm", "biocontainers/samtools:1.9", "samtools sort in.bam > out.bam")
}

// §12 A target with no container = runs unwrapped even when an engine is set.
func TestNoContainerWithoutImage(t *testing.T) {
	got := render(t, `cgp.container.engine = "docker"
out.txt: in.txt {{
    cp ${input} ${output}
}}`)
	mustNotContain(t, got, "docker run")
	mustContain(t, got, "cp in.txt out.txt")
}

// §12 No engine ⇒ no wrapping, even with a container = image.
func TestNoEngineNoWrap(t *testing.T) {
	got := render(t, `out.bam: in.bam {{
    container = "biocontainers/samtools:1.9"
    --
    samtools sort ${input} > ${output}
}}`)
	mustNotContain(t, got, "docker run", "singularity")
	mustContain(t, got, "samtools sort in.bam > out.bam")
}

// §12.1 gpu adds the engine's GPU flag inside a container (Docker --gpus).
func TestContainerGPUDocker(t *testing.T) {
	got := render(t, `cgp.container.engine = "docker"
train.model: data.tf {{
    container = "tf/tf:latest"
    gpu = 2
    --
    train.py --data ${input} --out ${output}
}}`)
	mustContain(t, got, "--gpus")
}

// §12 Singularity uses `singularity exec` and the --nv GPU flag.
func TestContainerSingularityGPU(t *testing.T) {
	got := render(t, `cgp.container.engine = "singularity"
out.bam: in.bam {{
    container = "docker://biocontainers/bwa:0.7.17"
    gpu = true
    --
    bwa ${input} > ${output}
}}`)
	mustContain(t, got, "singularity exec", "--nv")
}

// §12 Per-target container settings shape the docker invocation: extra bind
// mounts, env passthrough, raw engine opts, the body-file dir, and the in-image
// shell.
func TestContainerDockerSettings(t *testing.T) {
	got := render(t, `cgp.container.engine = "docker"
out.bam: in.bam {{
    container        = "img:1"
    container.bind   = ["/data", "/refs"]
    container.env    = ["SAMPLE"]
    container.opts   = ["--shm-size=1g"]
    container.body_dir = "/scratch"
    container.shell  = "bash"
    --
    run ${input} > ${output}
}}`)
	mustContain(t, got,
		"-v /data:/data", "-v /refs:/refs", // binds
		"-e SAMPLE",          // env passthrough
		"--shm-size=1g",      // raw opts
		`mktemp "/scratch/`,  // body_dir
		`bash "$__cgp_body"`, // in-image shell
	)
}

// §12 Docker user mapping is on by default and disabled by cgp.container.user_map.
func TestContainerUserMapToggle(t *testing.T) {
	on := render(t, `cgp.container.engine = "docker"
out.bam: in.bam {{
    container = "img:1"
    --
    run ${input} > ${output}
}}`)
	mustContain(t, on, "-u $(id -u):$(id -g)")

	off := render(t, `cgp.container.engine = "docker"
cgp.container.user_map = false
out.bam: in.bam {{
    container = "img:1"
    --
    run ${input} > ${output}
}}`)
	mustNotContain(t, off, "-u $(id -u)")
}

// §12 Singularity bind/env use its own flag syntax (-B / --env NAME="$NAME").
func TestContainerSingularitySettings(t *testing.T) {
	got := render(t, `cgp.container.engine = "singularity"
out.bam: in.bam {{
    container      = "img.sif"
    container.bind = ["/data"]
    container.env  = ["SAMPLE"]
    --
    run ${input} > ${output}
}}`)
	mustContain(t, got, "-B /data:/data", `--env SAMPLE="$SAMPLE"`)
	// a .sif image is used as-is (no docker:// rewrite)
	mustContain(t, got, "img.sif")
}

// §12 Global cgp.container.* settings apply when no per-target override is given.
func TestContainerGlobalBind(t *testing.T) {
	got := render(t, `cgp.container.engine = "docker"
cgp.container.bind = ["/shared"]
out.bam: in.bam {{
    container = "img:1"
    --
    run ${input} > ${output}
}}`)
	mustContain(t, got, "-v /shared:/shared")
}
