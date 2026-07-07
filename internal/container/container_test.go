package container

import (
	"strings"
	"testing"
)

func TestWrapDocker(t *testing.T) {
	got := Wrap("samtools sort in.bam > out.bam", Spec{
		Engine: "docker", Image: "biocontainers/samtools:1.9",
		GPU: "1", UserMap: true,
		Inputs: []string{"in.bam"}, Outputs: []string{"out.bam"},
	})
	for _, want := range []string{
		"__cgpipe_body=$(mktemp", "<<'__CGP_BODY__'", "samtools sort in.bam > out.bam",
		"docker run --rm", "-u $(id -u):$(id -g)", "--gpus all",
		"biocontainers/samtools:1.9", `sh "$__cgpipe_body"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("docker wrap missing %q in:\n%s", want, got)
		}
	}
}

func TestWrapSingularity(t *testing.T) {
	got := Wrap("echo hi", Spec{Engine: "singularity", Image: "biocontainers/bwa:0.7.17", GPU: "1"})
	for _, want := range []string{
		"singularity exec", "--nv", "docker://biocontainers/bwa:0.7.17", `sh "$__cgpipe_body"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("singularity wrap missing %q in:\n%s", want, got)
		}
	}
}

func TestSingularityKeepsSchemedOrSif(t *testing.T) {
	if got := normalizeSingularityImage("/refs/bwa.sif"); got != "/refs/bwa.sif" {
		t.Errorf("sif image rewritten: %q", got)
	}
	if got := normalizeSingularityImage("library://x/y"); got != "library://x/y" {
		t.Errorf("schemed image rewritten: %q", got)
	}
}

func TestNoWrapWithoutEngineOrImage(t *testing.T) {
	if got := Wrap("echo hi", Spec{Image: "x"}); got != "echo hi" {
		t.Errorf("wrapped despite empty engine: %q", got)
	}
	if got := Wrap("echo hi", Spec{Engine: "docker"}); got != "echo hi" {
		t.Errorf("wrapped despite empty image: %q", got)
	}
}

func TestMarkerCollisionAvoided(t *testing.T) {
	got := Wrap("echo __CGP_BODY__", Spec{Engine: "docker", Image: "x"})
	// the heredoc marker must differ from text in the body
	if strings.Contains(got, "<<'__CGP_BODY__'") {
		t.Errorf("marker collides with body content:\n%s", got)
	}
}
