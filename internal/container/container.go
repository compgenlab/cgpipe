// Package container wraps a rendered job body so it runs inside a container
// (Docker or Singularity/Apptainer). The wrapped body writes the script to a
// temp file and executes it inside the image with the right mounts, working
// directory, GPU flag, and env passthrough.
package container

import (
	"path/filepath"
	"strings"
)

// Spec describes how to wrap a body. Engine and Image being empty means "don't
// wrap" (Wrap returns the body unchanged).
type Spec struct {
	Engine     string // docker | singularity | apptainer
	Image      string
	WorkingDir string
	BodyDir    string // where the temp body file is written/mounted (default /tmp)
	Shell      string // shell to run the body inside the container (default sh)
	GPU        string // "", "1"/"true" (=> all/--nv), a count, or a device spec
	UserMap    bool   // docker: add -u $(id -u):$(id -g)
	Binds      []string
	Inputs     []string
	Outputs    []string
	Env        []string
	Opts       []string
}

// Wrap returns body wrapped to execute inside the container, or body unchanged
// when no engine/image is configured.
func Wrap(body string, s Spec) string {
	if body == "" || s.Engine == "" || s.Image == "" {
		return body
	}
	bodyDir := s.BodyDir
	if bodyDir == "" {
		bodyDir = "/tmp"
	}
	wd := absDir(s.WorkingDir)
	shell := s.Shell
	if shell == "" {
		shell = "sh"
	}
	mounts := computeMounts(s, bodyDir, wd)
	marker := pickMarker(body)

	var b strings.Builder
	b.WriteString("__cgpipe_body=$(mktemp \"" + bodyDir + "/cgpipe-body.XXXXXX\")\n")
	b.WriteString("trap 'rm -f \"$__cgpipe_body\"' EXIT\n")
	b.WriteString("cat > \"$__cgpipe_body\" <<'" + marker + "'\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(marker + "\n\n")

	switch strings.ToLower(s.Engine) {
	case "singularity", "apptainer":
		b.WriteString(renderSingularity(s, mounts, wd, shell))
	default: // docker
		b.WriteString(renderDocker(s, mounts, wd, shell))
	}
	b.WriteString("\n")
	return b.String()
}

func renderDocker(s Spec, mounts []string, wd, shell string) string {
	var b strings.Builder
	b.WriteString("docker run --rm")
	for _, m := range mounts {
		b.WriteString(" \\\n    -v " + m + ":" + m)
	}
	b.WriteString(" \\\n    -w " + wd)
	if s.UserMap {
		b.WriteString(" \\\n    -u $(id -u):$(id -g)")
	}
	if s.GPU != "" {
		spec := s.GPU
		if spec == "1" || spec == "true" {
			spec = "all"
		}
		b.WriteString(" \\\n    --gpus " + spec)
	}
	for _, e := range s.Env {
		b.WriteString(" \\\n    -e " + e)
	}
	for _, o := range s.Opts {
		b.WriteString(" \\\n    " + o)
	}
	b.WriteString(" \\\n    " + s.Image)
	b.WriteString(" \\\n    " + shell + " \"$__cgpipe_body\"")
	return b.String()
}

func renderSingularity(s Spec, mounts []string, wd, shell string) string {
	var b strings.Builder
	b.WriteString("singularity exec")
	for _, m := range mounts {
		b.WriteString(" \\\n    -B " + m + ":" + m)
	}
	b.WriteString(" \\\n    --pwd " + wd)
	if s.GPU != "" {
		b.WriteString(" \\\n    --nv")
	}
	for _, e := range s.Env {
		b.WriteString(" \\\n    --env " + e + "=\"$" + e + "\"")
	}
	for _, o := range s.Opts {
		b.WriteString(" \\\n    " + o)
	}
	b.WriteString(" \\\n    " + normalizeSingularityImage(s.Image))
	b.WriteString(" \\\n    " + shell + " \"$__cgpipe_body\"")
	return b.String()
}

// normalizeSingularityImage prepends docker:// to a bare Docker Hub reference so
// it pulls automatically; leaves schemed refs and local .sif files alone.
func normalizeSingularityImage(image string) string {
	if strings.Contains(image, "://") || strings.HasSuffix(image, ".sif") ||
		strings.HasPrefix(image, "/") || strings.HasPrefix(image, "./") {
		return image
	}
	return "docker://" + image
}

// computeMounts gathers the directories to bind: the body dir, working dir,
// explicit binds, and the parent directories of declared inputs/outputs.
func computeMounts(s Spec, bodyDir, wd string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	add(bodyDir)
	add(absDir(wd))
	for _, b := range s.Binds {
		add(b)
	}
	for _, p := range append(append([]string{}, s.Inputs...), s.Outputs...) {
		add(absDir(filepath.Dir(p)))
	}
	return out
}

func absDir(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func pickMarker(body string) string {
	marker := "__CGPIPE_BODY__"
	for i := 0; strings.Contains(body, marker); i++ {
		marker = "__CGPIPE_BODY_" + string(rune('A'+i)) + "__"
	}
	return marker
}
