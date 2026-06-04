// Package runner drives the dependency graph: it decides what is stale and, in
// dependency order, hands each target to a Backend to run or submit — threading
// the resulting job ids as dependencies. The shell runner and the scheduler
// (template) runners are Backends over this shared driver.
package runner

import (
	"fmt"
	"math"
	"strings"

	"github.com/compgen-io/cgp/internal/eval"
)

// Backend runs or submits a single target. deps are the job ids of upstream
// targets that ran/submitted in this build (for wiring scheduler dependencies);
// the returned id identifies this job ("" if the backend has none, e.g. the
// local shell). Submit is only called for targets that must run and have a body.
type Backend interface {
	Submit(t *eval.Target, deps []string) (jobID string, err error)
	// ExternalDep reports a job (from a prior run / earlier stage, via the ledger)
	// that will produce input — used when input has no in-run producer and isn't on
	// disk yet. Returns ("", false) when there is none (e.g. the shell backend).
	ExternalDep(input string) (jobID string, ok bool)
}

// Options configure a build.
type Options struct {
	Goals []string // explicit targets; empty => program default / first target
	Dir   string   // working directory for file-existence / mtime checks
	Cache *Cache   // shared stat cache (created per-build if nil)
	Force bool     // rebuild every target in the goal graph regardless of staleness
}

// Build resolves the program's goals and drives the backend over the stale
// targets in dependency order.
func Build(p *eval.Program, b Backend, opts Options) error {
	if opts.Cache == nil {
		opts.Cache = NewCache()
	}
	r := &driver{
		prog:          p,
		backend:       b,
		opts:          opts,
		candidates:    map[string][]*eval.Target{},
		candCache:     map[string][]*eval.Target{},
		wildInstances: map[string]*eval.Target{},
		chosen:        map[string]*eval.Target{},
		chosenSet:     map[string]bool{},
		sat:           map[*eval.Target]bool{},
		satActive:     map[*eval.Target]bool{},
		memo:          map[*eval.Target]resolved{},
		resolving:     map[*eval.Target]bool{},
		jobID:         map[*eval.Target]string{},
		done:          map[*eval.Target]bool{},
	}
	for _, t := range p.Targets {
		// A target with no outputs is opportunistic: it runs after the goals, only
		// if its inputs are already available, and never forces them to build.
		if len(t.Outputs) == 0 {
			if t.HasBody {
				r.opportunistic = append(r.opportunistic, t)
			}
			continue
		}
		wild := false
		for _, o := range t.Outputs {
			if strings.Contains(o, "%") {
				wild = true
			}
		}
		if wild {
			r.wildcards = append(r.wildcards, t)
			continue
		}
		// Keep every definition for an output, in source order: an output may have
		// more than one rule, and we pick the first whose inputs are satisfiable.
		for _, o := range t.Outputs {
			r.candidates[o] = append(r.candidates[o], t)
		}
	}

	goals := opts.Goals
	if len(goals) == 0 {
		goals = p.Default
	}
	if len(goals) == 0 && p.FirstOutput != "" {
		goals = []string{p.FirstOutput}
	}
	if len(goals) == 0 {
		return fmt.Errorf("no goal to build (no target requested and no @default or first target)")
	}

	if p.Setup != nil && p.Setup.HasBody {
		if _, err := b.Submit(p.Setup, nil); err != nil {
			return err
		}
	}
	for _, g := range goals {
		if err := r.buildGoal(g); err != nil {
			return err
		}
	}
	// Opportunistic jobs run after the goals are submitted (but before teardown):
	// each runs only when every input is already available — on disk, produced by
	// a job submitted this run, or owned by an active ledger job — and is silently
	// skipped otherwise. They never force an input to build.
	for _, opp := range r.opportunistic {
		if r.opportunisticReady(opp) {
			if _, err := r.runOpportunistic(opp); err != nil {
				return err
			}
		}
	}
	if p.Teardown != nil && p.Teardown.HasBody {
		if _, err := b.Submit(p.Teardown, nil); err != nil {
			return err
		}
	}
	return nil
}

type resolved struct {
	willRun bool
	eff     int64
}

type driver struct {
	prog    *eval.Program
	backend Backend
	opts    Options

	candidates    map[string][]*eval.Target // explicit rules per output, source order
	wildcards     []*eval.Target            // wildcard (%) rules
	opportunistic []*eval.Target            // no-output targets, run after the goals
	candCache     map[string][]*eval.Target // resolved candidate list per path (explicit + wildcard instances)
	wildInstances map[string]*eval.Target   // (rule,stem) -> instantiated target, shared across sibling outputs
	chosen        map[string]*eval.Target   // memoized chosen producer per path
	chosenSet     map[string]bool           // whether chosen[path] has been computed (nil is a valid choice)
	sat           map[*eval.Target]bool     // memoized satisfiability
	satActive     map[*eval.Target]bool     // satisfiability recursion guard

	memo      map[*eval.Target]resolved
	resolving map[*eval.Target]bool
	jobID     map[*eval.Target]string
	done      map[*eval.Target]bool
}

func (r *driver) buildGoal(goal string) error {
	t := r.producerFor(goal)
	if t == nil {
		if !r.statExists(goal) {
			return fmt.Errorf("no rule to make %q and it does not exist", goal)
		}
		return nil
	}
	res, err := r.resolve(t)
	if err != nil {
		return err
	}
	if res.willRun {
		_, err := r.submit(t)
		return err
	}
	return nil
}

// submit ensures target t (and any prerequisites it needs) are run/submitted,
// returning t's job id.
func (r *driver) submit(t *eval.Target) (string, error) {
	if r.done[t] {
		return r.jobID[t], nil
	}
	r.done[t] = true

	var deps []string
	for _, in := range t.Inputs {
		pt := r.producerFor(in)
		if pt == nil {
			// No in-run producer: if it's not on disk but an external job (ledger)
			// will produce it, depend on that job.
			if !r.statExists(in) {
				if id, ok := r.backend.ExternalDep(in); ok && id != "" {
					deps = append(deps, id)
				}
			}
			continue
		}
		sub, err := r.resolve(pt)
		if err != nil {
			return "", err
		}
		if sub.willRun || !r.statExists(in) {
			id, err := r.submit(pt)
			if err != nil {
				return "", err
			}
			if id != "" {
				deps = append(deps, id)
			}
		}
	}

	if !t.HasBody {
		return "", nil // bodyless aggregator
	}
	id, err := r.backend.Submit(t, deps)
	if err != nil {
		return "", err
	}
	r.jobID[t] = id
	return id, nil
}

func (r *driver) resolve(t *eval.Target) (resolved, error) {
	if res, ok := r.memo[t]; ok {
		return res, nil
	}
	if r.resolving[t] {
		return resolved{}, fmt.Errorf("dependency cycle involving %v", t.Outputs)
	}
	r.resolving[t] = true
	defer delete(r.resolving, t)

	var inNewest int64
	anyInputRebuilt := false
	for _, in := range t.Inputs {
		if pt := r.producerFor(in); pt != nil {
			sub, err := r.resolve(pt)
			if err != nil {
				return resolved{}, err
			}
			if sub.willRun {
				anyInputRebuilt = true
			} else if sub.eff > inNewest {
				inNewest = sub.eff
			}
			continue
		}
		m, ok := r.statMtime(in)
		if !ok {
			// Not on disk and no in-run producer: maybe a prior run / earlier stage
			// will produce it (tracked in the ledger). Treat it as a rebuilt input.
			if _, ext := r.backend.ExternalDep(in); ext {
				anyInputRebuilt = true
				continue
			}
			return resolved{}, fmt.Errorf("no rule to make %q (needed by %v)", in, t.Outputs)
		}
		if m > inNewest {
			inNewest = m
		}
	}

	// -force rebuilds every target in the goal graph regardless of mtime/existence.
	willRun := anyInputRebuilt || r.opts.Force
	if !willRun {
		for _, o := range t.Outputs {
			if !t.Temp[o] && !r.statExists(o) {
				willRun = true
				break
			}
		}
	}
	if !willRun {
		for _, o := range t.Outputs {
			if m, ok := r.statMtime(o); ok && m < inNewest {
				willRun = true
				break
			}
		}
	}

	res := resolved{willRun: willRun}
	if !willRun {
		minOut := int64(math.MaxInt64)
		any := false
		for _, o := range t.Outputs {
			if m, ok := r.statMtime(o); ok {
				any = true
				if m < minOut {
					minOut = m
				}
			}
		}
		if any {
			res.eff = minOut
		} else {
			res.eff = inNewest
		}
	}
	r.memo[t] = res
	return res, nil
}

// producerFor returns the rule chosen to build path: the first candidate (in
// source order, explicit rules before wildcards) whose inputs are all
// satisfiable. If none is satisfiable it falls back to the first candidate so
// the eventual "no rule to make" error points somewhere sensible; nil only when
// there is no candidate at all. The choice is memoized so resolve/submit/
// available all agree on the same producer.
func (r *driver) producerFor(path string) *eval.Target {
	if r.chosenSet[path] {
		return r.chosen[path]
	}
	cands := r.candidatesFor(path)
	var pick *eval.Target
	for _, c := range cands {
		if r.satisfiable(c) {
			pick = c
			break
		}
	}
	if pick == nil && len(cands) > 0 {
		pick = cands[0]
	}
	r.chosen[path] = pick
	r.chosenSet[path] = true
	return pick
}

// candidatesFor returns every rule that can produce path: the explicit
// definitions (source order) followed by matching wildcard instantiations. A
// wildcard instance is created once per (rule, stem) and shared, so sibling
// outputs of a multi-output wildcard resolve to the same target.
func (r *driver) candidatesFor(path string) []*eval.Target {
	if c, ok := r.candCache[path]; ok {
		return c
	}
	c := append([]*eval.Target{}, r.candidates[path]...)
	for _, rule := range r.wildcards {
		if stem, ok := matchWildcard(rule, path); ok {
			key := fmt.Sprintf("%p\x00%s", rule, stem)
			inst, ok := r.wildInstances[key]
			if !ok {
				inst = instantiate(rule, stem)
				r.wildInstances[key] = inst
			}
			c = append(c, inst)
		}
	}
	r.candCache[path] = c
	return c
}

// satisfiable reports whether every input of t can be provided — on disk, by a
// satisfiable producer (recursively), or by an active ledger job. A dependency
// cycle is treated as unsatisfiable.
func (r *driver) satisfiable(t *eval.Target) bool {
	if v, ok := r.sat[t]; ok {
		return v
	}
	if r.satActive[t] {
		return false
	}
	r.satActive[t] = true
	ok := true
	for _, in := range t.Inputs {
		if !r.inputSatisfiable(in) {
			ok = false
			break
		}
	}
	delete(r.satActive, t)
	r.sat[t] = ok
	return ok
}

func (r *driver) inputSatisfiable(in string) bool {
	if r.statExists(in) {
		return true
	}
	for _, c := range r.candidatesFor(in) {
		if r.satisfiable(c) {
			return true
		}
	}
	if _, ext := r.backend.ExternalDep(in); ext {
		return true
	}
	return false
}

// opportunisticReady reports whether all of an opportunistic target's inputs are
// already available (without building anything).
func (r *driver) opportunisticReady(t *eval.Target) bool {
	for _, in := range t.Inputs {
		if !r.available(in) {
			return false
		}
	}
	return true
}

// available reports whether input is on disk, was produced by a job submitted
// this run, or is owned by an active ledger job.
func (r *driver) available(in string) bool {
	if r.statExists(in) {
		return true
	}
	if pt := r.producerFor(in); pt != nil && r.done[pt] {
		return true
	}
	if _, ext := r.backend.ExternalDep(in); ext {
		return true
	}
	return false
}

// runOpportunistic submits an opportunistic target, depending on the in-run jobs
// (or active ledger jobs) that produce its inputs so a scheduler runs it last.
func (r *driver) runOpportunistic(t *eval.Target) (string, error) {
	var deps []string
	for _, in := range t.Inputs {
		if pt := r.producerFor(in); pt != nil {
			if id := r.jobID[pt]; id != "" {
				deps = append(deps, id)
			}
		} else if !r.statExists(in) {
			if id, ok := r.backend.ExternalDep(in); ok && id != "" {
				deps = append(deps, id)
			}
		}
	}
	return r.backend.Submit(t, deps)
}

func matchWildcard(rule *eval.Target, goal string) (string, bool) {
	for _, p := range rule.Outputs {
		i := strings.IndexByte(p, '%')
		if i < 0 {
			continue
		}
		prefix, suffix := p[:i], p[i+1:]
		if len(goal) > len(prefix)+len(suffix) &&
			strings.HasPrefix(goal, prefix) && strings.HasSuffix(goal, suffix) {
			return goal[len(prefix) : len(goal)-len(suffix)], true
		}
	}
	return "", false
}

func instantiate(rule *eval.Target, stem string) *eval.Target {
	inst := &eval.Target{
		Special: rule.Special,
		Body:    rule.Body,
		HasBody: rule.HasBody,
		Stem:    stem,
		Scope:   rule.Scope,
		Temp:    map[string]bool{},
	}
	for _, o := range rule.Outputs {
		ro := strings.ReplaceAll(o, "%", stem)
		inst.Outputs = append(inst.Outputs, ro)
		inst.Temp[ro] = rule.Temp[o]
	}
	for _, in := range rule.Inputs {
		inst.Inputs = append(inst.Inputs, strings.ReplaceAll(in, "%", stem))
	}
	return inst
}

func (r *driver) path(p string) string {
	if r.opts.Dir == "" || strings.HasPrefix(p, "/") {
		return p
	}
	return r.opts.Dir + "/" + p
}

// Label is a human-readable name for a target (its outputs, or its special name).
func Label(t *eval.Target) string {
	if t.Special != "" {
		return "@" + t.Special
	}
	if len(t.Outputs) > 0 {
		return strings.Join(t.Outputs, " ")
	}
	return "(opportunistic)"
}

func (r *driver) statExists(p string) bool { return r.opts.Cache.stat(r.path(p)).exists }

func (r *driver) statMtime(p string) (int64, bool) {
	s := r.opts.Cache.stat(r.path(p))
	return s.mtime, s.exists
}
