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
