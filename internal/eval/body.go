package eval

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/compgen-io/cgp/internal/ast"
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
	sc.set("input", toStrList(t.Inputs))
	sc.set("output", toStrList(t.Outputs))
	sc.set("stem", StrVal(t.Stem))
	for k, v := range t.Scope.vars {
		if bare, ok := strings.CutPrefix(k, "job."); ok && !sc.has(bare) {
			sc.set(bare, v)
		}
	}
	ip := &interp{sc: sc, out: io.Discard, prog: p}

	var sections []string
	if t.Special == "" && p.Pre != nil {
		s, err := ip.renderBodyText(p.Pre.Body)
		if err != nil {
			return nil, "", fmt.Errorf("@pre: %w", err)
		}
		sections = append(sections, s)
	}
	main, err := ip.renderBodyText(t.Body)
	if err != nil {
		return nil, "", err
	}
	sections = append(sections, main)
	if t.Special == "" && p.Post != nil {
		s, err := ip.renderBodyText(p.Post.Body)
		if err != nil {
			return nil, "", fmt.Errorf("@post: %w", err)
		}
		sections = append(sections, s)
	}
	return sc, strings.Join(sections, "\n"), nil
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

	for _, d := range directives {
		if strings.TrimSpace(d) == "" {
			continue
		}
		f, err := parser.Parse(d, "<directive>")
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

func toStrList(ss []string) ListVal {
	out := make(ListVal, len(ss))
	for i, s := range ss {
		out[i] = StrVal(s)
	}
	return out
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
			// a bare cgp statement on a % line (e.g. an assignment)
			nodes = append(nodes, stmtNode{ctrl})
			*idx++
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
			s, err := ip.interpolate(strings.TrimLeft(x.line, " \t"))
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
