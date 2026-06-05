package eval

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/compgen-io/cgp/internal/ast"
	"github.com/compgen-io/cgp/internal/container"
	"github.com/compgen-io/cgp/internal/parser"
)

// snippetRe matches a body line that is a lone @name snippet invocation.
var snippetRe = regexp.MustCompile(`^@([A-Za-z_][A-Za-z0-9_]*)$`)

// RenderTarget renders a target's shell body to executable script text,
// including the global @pre / @post wrappers (for non-reserved targets). The
// per-target ${input} / ${output} / ${stem} variables are made available.
func (p *Program) RenderTarget(t *Target) (string, error) {
	_, body, err := p.renderTargetScope(t)
	return body, err
}

// JobContext renders a target's body and returns the resulting job variables
// (the target's captured scope plus the directive settings, with global job.<x>
// promoted to bare <x>) alongside the rendered body. Scheduler runners use this
// to feed a submission template.
func (p *Program) JobContext(t *Target) (map[string]Value, string, error) {
	sc, body, err := p.renderTargetScope(t)
	if err != nil {
		return nil, "", err
	}
	vars := make(map[string]Value, len(sc.vars))
	for k, v := range sc.vars {
		vars[k] = v
	}
	return vars, body, nil
}

// RenderPostsubmit renders the @postsubmit body in the context of one submitted
// job: the job's ${input} / ${output} / ${stem} are exposed, plus ${jobid} (the
// scheduler id, empty for the shell runner). Returns "" when no @postsubmit is
// defined. The body runs once per submitted job, on the submit host.
func (p *Program) RenderPostsubmit(job *Target, jobID string) (string, error) {
	if p.Postsubmit == nil || !p.Postsubmit.HasBody {
		return "", nil
	}
	sc := newScope()
	if job.Scope != nil {
		sc = job.Scope.clone()
	}
	sc.set("input", StrList(job.Inputs))
	sc.set("output", StrList(job.Outputs))
	sc.set("stem", StrVal(job.Stem))
	sc.set("jobid", StrVal(jobID))
	ip := &interp{sc: sc, out: io.Discard, prog: p}
	return ip.renderBodyText(p.Postsubmit.Body)
}

// RenderText renders an arbitrary template string (a runner submission template)
// against the given variables, using the same body-rendering rules (${…}
// substitution and %-control lines).
func (p *Program) RenderText(tmpl string, vars map[string]Value) (string, error) {
	sc := newScope()
	for k, v := range vars {
		sc.set(k, v)
	}
	ip := &interp{sc: sc, out: io.Discard, prog: p}
	return ip.renderBodyText(tmpl)
}

// renderTargetScope builds the render scope for t (captured scope + input/output/
// stem + global job.* promoted to bare names), evaluates the directive block as a
// side effect, and returns the scope and the rendered body (with @pre/@post).
func (p *Program) renderTargetScope(t *Target) (*Scope, string, error) {
	sc := t.Scope.clone()
	sc.set("input", StrList(t.Inputs))
	sc.set("output", StrList(t.Outputs))
	sc.set("stem", StrVal(t.Stem))
	for k, v := range t.Scope.vars {
		if bare, ok := strings.CutPrefix(k, "job."); ok && !sc.has(bare) {
			sc.set(bare, v)
		}
	}
	ip := &interp{sc: sc, out: io.Discard, prog: p}

	// Render the main body first: its directive block sets per-job settings —
	// including nopre / nopost — into sc, which then decide @pre / @post wrapping.
	main, err := ip.renderBodyText(t.Body)
	if err != nil {
		return nil, "", err
	}

	var sections []string
	if t.Special == "" && p.Pre != nil && !scopeTruthy(sc, "nopre") {
		s, err := ip.renderBodyText(p.Pre.Body)
		if err != nil {
			return nil, "", fmt.Errorf("@pre: %w", err)
		}
		sections = append(sections, s)
	}
	sections = append(sections, main)
	if t.Special == "" && p.Post != nil && !scopeTruthy(sc, "nopost") {
		s, err := ip.renderBodyText(p.Post.Body)
		if err != nil {
			return nil, "", fmt.Errorf("@post: %w", err)
		}
		sections = append(sections, s)
	}
	body := strings.Join(sections, "\n")
	body = p.wrapContainer(sc, t, body)
	return sc, body, nil
}

// wrapContainer wraps the body to run inside a container when cgp.container.engine
// and a per-target image (container = …) are both set.
func (p *Program) wrapContainer(sc *Scope, t *Target, body string) string {
	engine := p.settingStr("cgp.container.engine")
	image := scopeStr(sc, "container")
	if engine == "" || image == "" {
		return body
	}
	optsKey := "cgp.container.docker_opts"
	if e := strings.ToLower(engine); e == "singularity" || e == "apptainer" {
		optsKey = "cgp.container.singularity_opts"
	}
	gpu := scopeStr(sc, "gpu")
	if gpu == "" {
		gpu = p.settingStr("cgp.gpu")
	}
	if gpu == "true" {
		gpu = "1"
	} else if gpu == "false" {
		gpu = ""
	}
	userMap := true
	if v, ok := p.Get("cgp.container.user_map"); ok {
		userMap = truthy(v)
	}
	return container.Wrap(body, container.Spec{
		Engine:     engine,
		Image:      image,
		WorkingDir: scopeStr(sc, "wd"),
		BodyDir:    firstNonEmpty(scopeStr(sc, "container.body_dir"), p.settingStr("cgp.container.body_dir")),
		Shell:      firstNonEmpty(scopeStr(sc, "container.shell"), p.settingStr("cgp.container.shell")),
		GPU:        gpu,
		UserMap:    userMap,
		Binds:      append(scopeList(sc, "container.bind"), p.settingList("cgp.container.bind")...),
		Inputs:     t.Inputs,
		Outputs:    t.Outputs,
		Env:        append(scopeList(sc, "container.env"), p.settingList("cgp.container.env")...),
		Opts:       append(scopeList(sc, "container.opts"), p.settingList(optsKey)...),
	})
}

func (p *Program) settingStr(name string) string {
	if v, ok := p.Get(name); ok {
		return stringify(v)
	}
	return ""
}

func (p *Program) settingList(name string) []string {
	if v, ok := p.Get(name); ok {
		return valueList(v)
	}
	return nil
}

// scopeTruthy reports whether a (directive) setting is present and truthy in the
// render scope — used for the nopre / nopost / shexec-style boolean directives.
func scopeTruthy(sc *Scope, name string) bool {
	if v, ok := sc.get(name); ok {
		return truthy(v)
	}
	return false
}

func scopeStr(sc *Scope, name string) string {
	if v, ok := sc.get(name); ok {
		if _, unset := v.(UnsetVal); !unset {
			return stringify(v)
		}
	}
	return ""
}

func scopeList(sc *Scope, name string) []string {
	if v, ok := sc.get(name); ok {
		return valueList(v)
	}
	return nil
}

func valueList(v Value) []string {
	if items, ok := asList(v); ok {
		out := make([]string, len(items))
		for i, e := range items {
			out[i] = stringify(e)
		}
		return out
	}
	if _, unset := v.(UnsetVal); unset {
		return nil
	}
	return []string{stringify(v)}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// renderBodyText resolves one raw {{ }} body into shell text: directives before
// '--' are evaluated as cgp statements; the remainder is the shell template,
// where %-prefixed lines are cgp control flow and other lines are shell with
// ${…} substitution.
func (ip *interp) renderBodyText(raw string) (string, error) {
	lines := strings.Split(raw, "\n")
	sep := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "--" {
			sep = i
			break
		}
	}
	directives := []string(nil)
	shell := lines
	if sep >= 0 {
		directives = lines[:sep]
		shell = lines[sep+1:]
	}

	// Parse the directive block as one cgp program (not line by line) so ordinary
	// multi-line control flow — if/for setting job settings — is allowed (§6.2),
	// not just one assignment per line.
	if dirSrc := strings.Join(directives, "\n"); strings.TrimSpace(dirSrc) != "" {
		f, err := parser.Parse(dirSrc, "<directive>")
		if err != nil {
			return "", fmt.Errorf("directive: %w", err)
		}
		if err := ip.execStmts(f.Stmts); err != nil {
			return "", err
		}
	}

	nodes, err := parseBodyNodes(shell)
	if err != nil {
		return "", err
	}
	var out []string
	if err := ip.renderNodes(nodes, &out); err != nil {
		return "", err
	}
	return strings.Join(out, "\n"), nil
}

// ---- body node tree (%-control lines wrapping shell lines) ----

type bodyNode interface{ bodyNode() }

type shellNode struct{ line string }
type stmtNode struct{ src string } // a bare cgp statement on a % line
type forNode struct {
	varName string
	iter    ast.Expr // nil for while-form
	cond    ast.Expr // set for while-form
	body    []bodyNode
}
type ifNode struct {
	conds  []ast.Expr
	blocks [][]bodyNode
	els    []bodyNode
}

func (shellNode) bodyNode() {}
func (stmtNode) bodyNode()  {}
func (*forNode) bodyNode()  {}
func (*ifNode) bodyNode()   {}

// bracketDelta returns the net ( + [ minus ) + ] depth of a line, ignoring
// brackets inside "…" string literals and after a # comment. Used to detect when
// a % statement line has an open bracket and continues onto the next % line.
func bracketDelta(s string) int {
	depth := 0
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '#':
			return depth // comment runs to end of line
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		}
	}
	return depth
}

func isCtrlLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "%")
}

func ctrlContent(line string) string {
	t := strings.TrimLeft(line, " \t")
	return strings.TrimSpace(t[1:]) // drop the leading '%'
}

func parseBodyNodes(lines []string) ([]bodyNode, error) {
	idx := 0
	nodes, stop, err := parseBlockNodes(lines, &idx)
	if err != nil {
		return nil, err
	}
	if stop != "" {
		return nil, fmt.Errorf("unexpected '%% %s' in body (no matching open)", stop)
	}
	return nodes, nil
}

// parseBlockNodes reads body lines until a %-closer ("}", "} elif …", "} else …")
// at this nesting level (returned, not consumed) or end of input (stop == "").
func parseBlockNodes(lines []string, idx *int) (nodes []bodyNode, stop string, err error) {
	for *idx < len(lines) {
		line := lines[*idx]
		if !isCtrlLine(line) {
			nodes = append(nodes, shellNode{line})
			*idx++
			continue
		}
		ctrl := ctrlContent(line)
		switch {
		case strings.HasPrefix(ctrl, "}"):
			return nodes, ctrl, nil // closer; caller consumes
		case strings.HasPrefix(ctrl, "for "):
			*idx++
			fn, err := parseForHeader(ctrl)
			if err != nil {
				return nil, "", err
			}
			body, st, err := parseBlockNodes(lines, idx)
			if err != nil {
				return nil, "", err
			}
			if st != "}" {
				return nil, "", fmt.Errorf("unclosed '%% for' block")
			}
			*idx++ // consume '}'
			fn.body = body
			nodes = append(nodes, fn)
		case strings.HasPrefix(ctrl, "if "):
			*idx++
			n := &ifNode{}
			cond, err := parseExprFromHeader(ctrl[len("if "):])
			if err != nil {
				return nil, "", err
			}
			n.conds = append(n.conds, cond)
			for {
				body, st, err := parseBlockNodes(lines, idx)
				if err != nil {
					return nil, "", err
				}
				n.blocks = append(n.blocks, body)
				switch {
				case st == "}":
					*idx++
					nodes = append(nodes, n)
				case strings.HasPrefix(st, "} elif "):
					c, err := parseExprFromHeader(st[len("} elif "):])
					if err != nil {
						return nil, "", err
					}
					n.conds = append(n.conds, c)
					*idx++
					continue
				case strings.HasPrefix(st, "} else"):
					*idx++
					els, st2, err := parseBlockNodes(lines, idx)
					if err != nil {
						return nil, "", err
					}
					if st2 != "}" {
						return nil, "", fmt.Errorf("unclosed '%% if/else' block")
					}
					*idx++
					n.els = els
					nodes = append(nodes, n)
				default:
					return nil, "", fmt.Errorf("unclosed '%% if' block")
				}
				break
			}
		default:
			// A cgp statement on a % line. It may span several % lines when an
			// expression has an open ( or [ — the open bracket is the continuation
			// signal (no escape needed), so we gather following % lines until the
			// brackets balance and parse the whole thing together. A balanced line
			// (e.g. `for x {`, where braces don't count) is consumed alone, so this
			// never swallows a following control header.
			var src []string
			depth := 0
			for *idx < len(lines) && isCtrlLine(lines[*idx]) {
				c := ctrlContent(lines[*idx])
				src = append(src, c)
				depth += bracketDelta(c)
				*idx++
				if depth <= 0 {
					break
				}
			}
			nodes = append(nodes, stmtNode{strings.Join(src, "\n")})
		}
	}
	return nodes, "", nil
}

func parseForHeader(ctrl string) (*forNode, error) {
	rest := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(ctrl[len("for "):]), "{"))
	fn := &forNode{}
	if i := strings.Index(rest, " in "); i >= 0 {
		fn.varName = strings.TrimSpace(rest[:i])
		e, err := parser.ParseExpr(strings.TrimSpace(rest[i+len(" in "):]))
		if err != nil {
			return nil, err
		}
		fn.iter = e
		return fn, nil
	}
	e, err := parser.ParseExpr(rest)
	if err != nil {
		return nil, err
	}
	fn.cond = e
	return fn, nil
}

func parseExprFromHeader(s string) (ast.Expr, error) {
	return parser.ParseExpr(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "{")))
}

func (ip *interp) renderNodes(nodes []bodyNode, out *[]string) error {
	for _, n := range nodes {
		switch x := n.(type) {
		case shellNode:
			if m := snippetRe.FindStringSubmatch(strings.TrimSpace(x.line)); m != nil {
				body, ok := ip.prog.Snippets[m[1]]
				if !ok {
					return fmt.Errorf("unknown snippet: @%s", m[1])
				}
				sub, err := parseBodyNodes(strings.Split(body, "\n"))
				if err != nil {
					return err
				}
				if err := ip.renderNodes(sub, out); err != nil {
					return err
				}
				continue
			}
			s, err := ip.interpolate(strings.TrimLeft(x.line, " \t"), modeBody)
			if err != nil {
				return err
			}
			*out = append(*out, s)
		case stmtNode:
			f, err := parser.Parse(x.src, "<body>")
			if err != nil {
				return err
			}
			if err := ip.execStmts(f.Stmts); err != nil {
				return err
			}
		case *forNode:
			if x.iter != nil {
				v, err := ip.eval(x.iter)
				if err != nil {
					return err
				}
				items, ok := asList(v)
				if !ok {
					return fmt.Errorf("%% for…in requires a list/range")
				}
				for _, e := range items {
					ip.sc.set(x.varName, e)
					if err := ip.renderNodes(x.body, out); err != nil {
						return err
					}
				}
			} else {
				const cap = 1_000_000
				for i := 0; ; i++ {
					if i >= cap {
						return fmt.Errorf("%% for-loop exceeded %d iterations", cap)
					}
					v, err := ip.eval(x.cond)
					if err != nil {
						return err
					}
					if !truthy(v) {
						break
					}
					if err := ip.renderNodes(x.body, out); err != nil {
						return err
					}
				}
			}
		case *ifNode:
			done := false
			for i, c := range x.conds {
				v, err := ip.eval(c)
				if err != nil {
					return err
				}
				if truthy(v) {
					if err := ip.renderNodes(x.blocks[i], out); err != nil {
						return err
					}
					done = true
					break
				}
			}
			if !done && x.els != nil {
				if err := ip.renderNodes(x.els, out); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
