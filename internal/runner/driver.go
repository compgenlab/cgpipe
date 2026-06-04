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
}

// Options configure a build.
type Options struct {
	Goals []string // explicit targets; empty => program default / first target
	Dir   string   // working directory for file-existence / mtime checks
	Cache *Cache   // shared stat cache (created per-build if nil)
}

// Build resolves the program's goals and drives the backend over the stale
// targets in dependency order.
func Build(p *eval.Program, b Backend, opts Options) error {
	if opts.Cache == nil {
		opts.Cache = NewCache()
	}
	r := &driver{
		prog:      p,
		backend:   b,
		opts:      opts,
		producer:  map[string]*eval.Target{},
		memo:      map[*eval.Target]resolved{},
		resolving: map[*eval.Target]bool{},
		jobID:     map[*eval.Target]string{},
		done:      map[*eval.Target]bool{},
	}
	for _, t := range p.Targets {
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
		for _, o := range t.Outputs {
			if _, dup := r.producer[o]; !dup {
				r.producer[o] = t
			}
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
	prog      *eval.Program
	backend   Backend
	opts      Options
	producer  map[string]*eval.Target
	wildcards []*eval.Target
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
			return resolved{}, fmt.Errorf("no rule to make %q (needed by %v)", in, t.Outputs)
		}
		if m > inNewest {
			inNewest = m
		}
	}

	willRun := anyInputRebuilt
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

func (r *driver) producerFor(path string) *eval.Target {
	if t, ok := r.producer[path]; ok {
		return t
	}
	for _, rule := range r.wildcards {
		if stem, ok := matchWildcard(rule, path); ok {
			inst := instantiate(rule, stem)
			for _, o := range inst.Outputs {
				if _, dup := r.producer[o]; !dup {
					r.producer[o] = inst
				}
			}
			return inst
		}
	}
	return nil
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
