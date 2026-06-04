package shell

import (
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"

	"github.com/compgen-io/cgp/internal/eval"
)

// Options configures a shell run.
type Options struct {
	Goals  []string  // explicit targets to build; empty => program default
	DryRun bool      // render scripts instead of executing
	Dir    string    // working directory for jobs (default: current)
	Out    io.Writer // dry-run output / informational (default os.Stdout)
	Stdout io.Writer // job stdout (default os.Stdout)
	Stderr io.Writer // job stderr (default os.Stderr)
}

// Run builds the program's goals using the local shell: it resolves the
// dependency graph, decides what is stale (mtime-based, with the temp
// look-through), and executes each stale target's rendered body with bash.
func Run(p *eval.Program, opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	r := &runner{
		prog:      p,
		opts:      opts,
		producer:  map[string]*eval.Target{},
		memo:      map[*eval.Target]resolved{},
		resolving: map[*eval.Target]bool{},
		executed:  map[*eval.Target]bool{},
	}
	for _, t := range p.Targets {
		isWild := false
		for _, o := range t.Outputs {
			if strings.Contains(o, "%") {
				isWild = true
			}
		}
		if isWild {
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

	if p.Setup != nil {
		if err := r.execSpecial(p.Setup); err != nil {
			return err
		}
	}
	for _, g := range goals {
		if err := r.buildGoal(g); err != nil {
			return err
		}
	}
	if p.Teardown != nil {
		if err := r.execSpecial(p.Teardown); err != nil {
			return err
		}
	}
	return nil
}

type resolved struct {
	willRun bool
	eff     int64 // effective mtime (nanoseconds); meaningless when willRun
}

type runner struct {
	prog      *eval.Program
	opts      Options
	producer  map[string]*eval.Target
	wildcards []*eval.Target
	memo      map[*eval.Target]resolved
	resolving map[*eval.Target]bool
	executed  map[*eval.Target]bool
}

// producerFor returns the target that produces path: an exact match, or a
// freshly instantiated wildcard (%) rule. nil if nothing produces it.
func (r *runner) producerFor(path string) *eval.Target {
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

func (r *runner) buildGoal(goal string) error {
	t := r.producerFor(goal)
	if t == nil {
		if !exists(r.path(goal)) {
			return fmt.Errorf("no rule to make %q and it does not exist", goal)
		}
		return nil // source file already present
	}
	res, err := r.resolve(t)
	if err != nil {
		return err
	}
	if res.willRun {
		return r.execute(t)
	}
	return nil
}

// resolve decides whether target t must run, and its effective mtime for
// dependents. A missing temp output is transparent: staleness looks through it
// to its inputs.
func (r *runner) resolve(t *eval.Target) (resolved, error) {
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
		m, ok := mtime(r.path(in))
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
			if !t.Temp[o] && !exists(r.path(o)) {
				willRun = true
				break
			}
		}
	}
	if !willRun {
		for _, o := range t.Outputs {
			if m, ok := mtime(r.path(o)); ok && m < inNewest {
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
			if m, ok := mtime(r.path(o)); ok {
				any = true
				if m < minOut {
					minOut = m
				}
			}
		}
		if any {
			res.eff = minOut
		} else {
			res.eff = inNewest // transparent (missing temp / phony)
		}
	}
	r.memo[t] = res
	return res, nil
}

func (r *runner) execute(t *eval.Target) error {
	if r.executed[t] {
		return nil
	}
	r.executed[t] = true
	// Ensure inputs that will run, or are missing, are built first.
	for _, in := range t.Inputs {
		if pt := r.producerFor(in); pt != nil {
			sub, err := r.resolve(pt)
			if err != nil {
				return err
			}
			if sub.willRun || !exists(r.path(in)) {
				if err := r.execute(pt); err != nil {
					return err
				}
			}
		}
	}
	if !t.HasBody {
		return nil // bodyless aggregator: nothing to run
	}
	script, err := r.prog.RenderTarget(t)
	if err != nil {
		return err
	}
	return r.runScript(label(t), script)
}

func (r *runner) execSpecial(t *eval.Target) error {
	script, err := r.prog.RenderTarget(t)
	if err != nil {
		return err
	}
	return r.runScript("@"+t.Special, script)
}

func (r *runner) runScript(label, script string) error {
	if r.opts.DryRun {
		fmt.Fprintf(r.opts.Out, "# ---- %s ----\n%s\n", label, script)
		return nil
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = r.opts.Dir
	cmd.Stdout = r.opts.Stdout
	cmd.Stderr = r.opts.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

func (r *runner) path(p string) string {
	if r.opts.Dir == "" || strings.HasPrefix(p, "/") {
		return p
	}
	return r.opts.Dir + "/" + p
}

func label(t *eval.Target) string {
	if len(t.Outputs) > 0 {
		return strings.Join(t.Outputs, " ")
	}
	return "(opportunistic)"
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mtime(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return fi.ModTime().UnixNano(), true
}
