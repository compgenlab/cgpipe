package sched

import _ "embed"

// Submission templates live as standalone files under templates/ so they can be
// copied as a starting point for custom runner templates. They are embedded into
// the binary at build time. Each is written in cgp body syntax: %-prefixed lines
// are cgp control flow, other lines are emitted with ${…} substitution. They read
// per-job settings as bare names (mem, procs, walltime, name, queue, account, …),
// the rendered job body as ${_body}, and the dependency list as ${depids}.

//go:embed templates/slurm.template.cgp
var slurmTmpl string

//go:embed templates/sge.template.cgp
var sgeTmpl string

//go:embed templates/pbs.template.cgp
var pbsTmpl string

//go:embed templates/batchq.template.cgp
var batchqTmpl string

// DefaultTemplate returns the built-in submission template for a scheduler, so
// `cgp template <name>` can print it as a starting point for customization.
func DefaultTemplate(name string) (string, bool) {
	s, ok := schedulers[name]
	if !ok {
		return "", false
	}
	return s.Template, true
}
