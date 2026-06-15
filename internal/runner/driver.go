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
	"github.com/compgen-io/cgp/internal/token"
)

// arrayBackend is an optional Backend capability: submit a group of array-member
// targets as one scheduler job array, returning each member's per-task job id and
// the array's base id (for downstream whole-array dependencies). deps are shared
// afterok ids; aftercorr carries element-wise array→array partner ids. A backend
// that doesn't implement it (shell, graphviz, …) falls back to submitting each
// member individually.
type arrayBackend interface {
	SubmitArray(members []*eval.Target, indices []int, deps, aftercorr []string) (ids map[*eval.Target]string, baseID string, err error)
}

// arrayGroup is a set of targets from one declaration (same source position) that
// each set job.array to their task index — submitted together as one array.
type arrayGroup struct {
	name    string // a representative label, for error messages
	members []*eval.Target
	index   map[*eval.Target]int
}

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
	// PostSubmit runs the @postsubmit hook (if any) on the submit host, once for
	// the job just submitted, with its id available as ${jobid}.
	PostSubmit(job *eval.Target, jobID string) error
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
		arrayOf:       map[*eval.Target]*arrayGroup{},
		arrayDone:     map[*arrayGroup]bool{},
		arrayBase:     map[*arrayGroup]string{},
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

	if err := r.detectArrays(); err != nil {
		return err
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
		if _, err := r.doSubmit(p.Setup, nil); err != nil {
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
		if _, err := r.doSubmit(p.Teardown, nil); err != nil {
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

	arrayOf   map[*eval.Target]*arrayGroup // array members → their group
	arrayDone map[*arrayGroup]bool         // group already submitted
	arrayBase map[*arrayGroup]string       // group → base array job id (when submitted as one array)
}

// depEdge is a resolved dependency of a target: the wired job id and, for an
// in-run producer, the target that produced it (nil for external/ledger deps).
// The producer lets the driver recognize array-group structure (to collapse a
// whole-array dependency to its base id, or detect element-wise aftercorr edges).
type depEdge struct {
	id       string
	producer *eval.Target
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
// returning t's job id. Array members are submitted as a group (once).
func (r *driver) submit(t *eval.Target) (string, error) {
	if r.done[t] {
		return r.jobID[t], nil
	}
	if g := r.arrayOf[t]; g != nil {
		if err := r.submitArrayGroup(g); err != nil {
			return "", err
		}
		return r.jobID[t], nil
	}
	r.done[t] = true

	deps, err := r.collectDeps(t)
	if err != nil {
		return "", err
	}
	if !t.HasBody {
		return "", nil // bodyless aggregator
	}
	id, err := r.doSubmit(t, deps)
	if err != nil {
		return "", err
	}
	r.jobID[t] = id
	return id, nil
}

// collectDeps resolves t's inputs into the dependency job ids to wire, collapsing
// a fully-consumed array to its single base id (see collapseFullArrayDeps).
func (r *driver) collectDeps(t *eval.Target) ([]string, error) {
	edges, err := r.collectDepEdges(t)
	if err != nil {
		return nil, err
	}
	return r.collapseFullArrayDeps(edges), nil
}

// collectDepEdges resolves t's inputs, submitting any in-run producers and
// gathering their job ids (plus external/ledger producers for inputs not on
// disk), tracking the producing target behind each edge.
func (r *driver) collectDepEdges(t *eval.Target) ([]depEdge, error) {
	var edges []depEdge
	for _, in := range t.Inputs {
		pt := r.producerFor(in)
		if pt == nil {
			// No in-run producer: if it's not on disk but an external job (ledger)
			// will produce it, depend on that job.
			if !r.statExists(in) {
				if id, ok := r.backend.ExternalDep(in); ok && id != "" {
					edges = append(edges, depEdge{id: id})
				}
			}
			continue
		}
		sub, err := r.resolve(pt)
		if err != nil {
			return nil, err
		}
		if sub.willRun || !r.statExists(in) {
			id, err := r.submit(pt)
			if err != nil {
				return nil, err
			}
			if id != "" {
				edges = append(edges, depEdge{id: id, producer: pt})
			}
		}
	}
	return edges, nil
}

// collapseFullArrayDeps turns dependency edges into the job-id list to wire. When
// a consumer depends on a per-task id of *every* member of one array group (and
// that group was submitted as a real scheduler array, so its base id is known),
// the per-task ids are replaced with the array's single base id — one compact
// directive (e.g. afterok:<arrayid>) instead of N task addresses, which schedulers
// expand back to the same per-task edges.
func (r *driver) collapseFullArrayDeps(edges []depEdge) []string {
	seen := map[*arrayGroup]map[*eval.Target]bool{}
	for _, e := range edges {
		if e.producer == nil {
			continue
		}
		if g := r.arrayOf[e.producer]; g != nil {
			if seen[g] == nil {
				seen[g] = map[*eval.Target]bool{}
			}
			seen[g][e.producer] = true
		}
	}
	full := map[*arrayGroup]bool{}
	for g, members := range seen {
		if r.arrayBase[g] != "" && len(members) == len(g.members) {
			full[g] = true
		}
	}
	var ids []string
	added := map[*arrayGroup]bool{}
	for _, e := range edges {
		if e.producer != nil {
			if g := r.arrayOf[e.producer]; full[g] {
				if !added[g] {
					ids = append(ids, r.arrayBase[g])
					added[g] = true
				}
				continue
			}
		}
		ids = append(ids, e.id)
	}
	return ids
}

// detectArrays groups targets that mark themselves with a job.array task index
// (from one declaration) so the backend can submit them as one scheduler array.
func (r *driver) detectArrays() error {
	groups := map[token.Pos]*arrayGroup{}
	for _, t := range r.prog.Targets {
		// Only real (bodied, output-producing) targets whose body even mentions
		// job.array are candidates — this keeps non-array pipelines free of the
		// extra per-target render below.
		if !t.HasBody || len(t.Outputs) == 0 || !strings.Contains(t.Body, "job.array") {
			continue
		}
		idx, ok, err := r.prog.ArrayIndex(t)
		if err != nil {
			return fmt.Errorf("%s: %w", Label(t), err)
		}
		if !ok {
			continue
		}
		g := groups[t.Pos]
		if g == nil {
			g = &arrayGroup{name: Label(t), index: map[*eval.Target]int{}}
			groups[t.Pos] = g
		}
		for _, m := range g.members {
			if g.index[m] == idx {
				return fmt.Errorf("array %q: duplicate job.array index %d — each element's index must be unique", g.name, idx)
			}
		}
		g.members = append(g.members, t)
		g.index[t] = idx
		r.arrayOf[t] = g
	}
	return nil
}

// submitArrayGroup submits a whole array group at once: it resolves each stale
// member's dependencies, derives the shared afterok deps and any element-wise
// aftercorr directive (arrayDepDirectives), then hands the stale members to the
// backend as one array. Up-to-date members are skipped, so a restart submits only
// the gaps (sparse).
func (r *driver) submitArrayGroup(g *arrayGroup) error {
	if r.arrayDone[g] {
		return nil
	}
	r.arrayDone[g] = true

	var stale []*eval.Target
	var idxs []int
	edges := map[*eval.Target][]depEdge{}
	for _, m := range g.members {
		if r.done[m] {
			continue
		}
		r.done[m] = true
		e, err := r.collectDepEdges(m)
		if err != nil {
			return err
		}
		res, err := r.resolve(m)
		if err != nil {
			return err
		}
		// Sparse: include a member when it is stale OR any of its outputs is missing
		// (temp outputs don't count toward willRun, but a downstream still needs them
		// built). Up-to-date members are skipped.
		if !m.HasBody || (!res.willRun && r.outputsPresent(m)) {
			continue
		}
		edges[m] = e
		stale = append(stale, m)
		idxs = append(idxs, g.index[m])
	}
	if len(stale) == 0 {
		return nil
	}

	deps, aftercorr, err := r.arrayDepDirectives(g, stale, edges)
	if err != nil {
		return err
	}

	ids := map[*eval.Target]string{}
	if ab, ok := r.backend.(arrayBackend); ok {
		got, base, err := ab.SubmitArray(stale, idxs, deps, aftercorr)
		if err != nil {
			return err
		}
		ids = got
		if base != "" {
			r.arrayBase[g] = base
		}
	} else {
		// Backend without array support (shell, graphviz, …): one submit per member.
		if len(aftercorr) > 0 {
			return fmt.Errorf("array %q: element-wise (aftercorr) dependencies need an array-capable scheduler", g.name)
		}
		for _, m := range stale {
			id, err := r.backend.Submit(m, deps)
			if err != nil {
				return err
			}
			ids[m] = id
		}
	}
	for _, m := range stale {
		r.jobID[m] = ids[m]
		if r.prog.Postsubmit != nil && r.prog.Postsubmit.HasBody {
			if err := r.backend.PostSubmit(m, ids[m]); err != nil {
				return err
			}
		}
	}
	return nil
}

// arrayDepDirectives derives the dependency directives for an array group's stale
// members. The common case is a single shared afterok set (scatter/gather), which
// every member must agree on. An element-wise array→array edge — each member m
// (index i) depending only on partner group A's task i — instead becomes one
// aftercorr:<A> directive, with any remaining shared deps returned as afterok.
func (r *driver) arrayDepDirectives(g *arrayGroup, stale []*eval.Target, edges map[*eval.Target][]depEdge) (deps []string, aftercorr []string, err error) {
	// elemEdge finds m's edge to an array partner (≠ g) at m's own index — the
	// signature of an element-wise dependency. It must be the *only* edge into that
	// partner: a member depending on every task of an upstream array is a broadcast
	// (a whole-array afterok), not element-wise. Returns the partner group and the
	// edge's position in m's slice (-1 if none).
	elemEdge := func(m *eval.Target) (*arrayGroup, int) {
		i := g.index[m]
		perGroup := map[*arrayGroup]int{}
		for _, e := range edges[m] {
			if e.producer == nil {
				continue
			}
			if a := r.arrayOf[e.producer]; a != nil && a != g {
				perGroup[a]++
			}
		}
		for k, e := range edges[m] {
			if e.producer == nil {
				continue
			}
			a := r.arrayOf[e.producer]
			if a != nil && a != g && a.index[e.producer] == i && perGroup[a] == 1 {
				return a, k
			}
		}
		return nil, -1
	}

	// A partner group must be element-wise for *every* member, and the same group.
	var partner *arrayGroup
	allElem := true
	for _, m := range stale {
		a, _ := elemEdge(m)
		if a == nil {
			allElem = false
			break
		}
		if partner == nil {
			partner = a
		} else if partner != a {
			return nil, nil, fmt.Errorf("array %q: element-wise dependencies on more than one array are not supported", g.name)
		}
	}

	if allElem && partner != nil {
		// Partner must cover every member's index, and must have been submitted as a
		// real array (so it has a base id to wire the single aftercorr directive).
		base := r.arrayBase[partner]
		if base == "" {
			return nil, nil, fmt.Errorf("array %q: aftercorr partner %q was not submitted as a scheduler array", g.name, partner.name)
		}
		partnerIdx := map[int]bool{}
		for _, pm := range partner.members {
			partnerIdx[partner.index[pm]] = true
		}
		// The remaining (non-element-wise) deps must be identical across members.
		have := false
		for _, m := range stale {
			if !partnerIdx[g.index[m]] {
				return nil, nil, fmt.Errorf("array %q: aftercorr partner %q has no task at index %d", g.name, partner.name, g.index[m])
			}
			_, k := elemEdge(m)
			rem := make([]depEdge, 0, len(edges[m]))
			rem = append(rem, edges[m][:k]...)
			rem = append(rem, edges[m][k+1:]...)
			ids := r.collapseFullArrayDeps(rem)
			if !have {
				deps, have = ids, true
			} else if !sameDeps(deps, ids) {
				return nil, nil, fmt.Errorf("array %q: aftercorr members have differing non-element-wise dependencies", g.name)
			}
		}
		return deps, []string{base}, nil
	}

	// No element-wise pattern: one array submission carries one afterok directive,
	// so every stale member must resolve to the same dependency set.
	have := false
	for _, m := range stale {
		ids := r.collapseFullArrayDeps(edges[m])
		if !have {
			deps, have = ids, true
		} else if !sameDeps(deps, ids) {
			return nil, nil, fmt.Errorf("array %q has per-element dependencies (an element-wise array→array edge); "+
				"that needs aftercorr with matching index sets, or submit one job per target (drop job.array on one rule)", g.name)
		}
	}
	return deps, nil, nil
}

// outputsPresent reports whether every one of t's outputs is on disk.
func (r *driver) outputsPresent(t *eval.Target) bool {
	for _, o := range t.Outputs {
		if !r.statExists(o) {
			return false
		}
	}
	return true
}

// sameDeps reports whether two dependency-id lists are equal as sets.
func sameDeps(a, b []string) bool {
	sa, sb := map[string]bool{}, map[string]bool{}
	for _, x := range a {
		sa[x] = true
	}
	for _, x := range b {
		sb[x] = true
	}
	if len(sa) != len(sb) {
		return false
	}
	for k := range sa {
		if !sb[k] {
			return false
		}
	}
	return true
}

// doSubmit submits a target and then runs the @postsubmit hook (once per
// submitted job, on the submit host) with the job's id.
func (r *driver) doSubmit(t *eval.Target, deps []string) (string, error) {
	id, err := r.backend.Submit(t, deps)
	if err != nil {
		return "", err
	}
	if r.prog.Postsubmit != nil && r.prog.Postsubmit.HasBody {
		if err := r.backend.PostSubmit(t, id); err != nil {
			return "", err
		}
	}
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
	return r.doSubmit(t, deps)
}

// MatchWildcard reports whether rule's `%` pattern matches goal, returning the
// stem. Exported for graph builders that resolve producers outside the driver.
func MatchWildcard(rule *eval.Target, goal string) (string, bool) {
	return matchWildcard(rule, goal)
}

// Instantiate concretizes a wildcard rule for the given stem (substituting `%`
// in its outputs and inputs). Exported for graph builders.
func Instantiate(rule *eval.Target, stem string) *eval.Target {
	return instantiate(rule, stem)
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
