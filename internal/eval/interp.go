package eval

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"github.com/compgen-io/cgp/internal/parser"
)

var identPathRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// Interpolate resolves a template string against the given variables. Used by
// workflow orchestration to resolve stage names/files/args (which reference
// ${prior_stage.export}) as each stage runs.
func Interpolate(raw string, vars map[string]Value) (string, error) {
	sc := newScope()
	for k, v := range vars {
		sc.set(k, v)
	}
	ip := &interp{sc: sc, out: io.Discard, prog: &Program{Snippets: map[string]string{}, Exports: map[string]Value{}}}
	return ip.interpolate(raw)
}

// interpolate resolves a raw template to a single string (@{…} expansions are
// joined with spaces).
func (ip *interp) interpolate(raw string) (string, error) {
	parts, err := ip.expandTemplate(raw)
	if err != nil {
		return "", err
	}
	return strings.Join(parts, " "), nil
}

// expandTemplate resolves ${…}/$(…)/@{…} and \ escapes in raw, returning one or
// more strings (more than one when @{…} expands a list — a cartesian product
// across multiple @{…} occurrences).
func (ip *interp) expandTemplate(raw string) ([]string, error) {
	var pieces [][]string
	var buf strings.Builder
	flush := func() {
		pieces = append(pieces, []string{buf.String()})
		buf.Reset()
	}

	for i := 0; i < len(raw); {
		c := raw[i]
		switch {
		case c == '\\' && i+1 < len(raw):
			buf.WriteByte(raw[i+1])
			i += 2
		case c == '$' && i+2 < len(raw) && raw[i+1] == '{' && raw[i+2] == '{':
			// ${{ var }} double-evaluation: substitute, then evaluate the result
			end := strings.Index(raw[i+3:], "}}")
			if end < 0 {
				return nil, fmt.Errorf("unterminated ${{ in %q", raw)
			}
			v, err := ip.evalString(raw[i+3 : i+3+end])
			if err != nil {
				return nil, err
			}
			s, err := ip.interpolate(stringify(v))
			if err != nil {
				return nil, err
			}
			buf.WriteString(s)
			i = i + 3 + end + 2
		case c == '$' && i+1 < len(raw) && raw[i+1] == '{':
			end := strings.IndexByte(raw[i+2:], '}')
			if end < 0 {
				return nil, fmt.Errorf("unterminated ${ in %q", raw)
			}
			s, err := ip.resolveDollarBrace(raw[i+2 : i+2+end])
			if err != nil {
				return nil, err
			}
			buf.WriteString(s)
			i = i + 2 + end + 1
		case c == '$' && i+1 < len(raw) && raw[i+1] == '(':
			end := strings.IndexByte(raw[i+2:], ')')
			if end < 0 {
				return nil, fmt.Errorf("unterminated $( in %q", raw)
			}
			out, err := ip.runShell(raw[i+2 : i+2+end])
			if err != nil {
				return nil, err
			}
			buf.WriteString(out)
			i = i + 2 + end + 1
		case c == '@' && i+1 < len(raw) && raw[i+1] == '{':
			end := strings.IndexByte(raw[i+2:], '}')
			if end < 0 {
				return nil, fmt.Errorf("unterminated @{ in %q", raw)
			}
			elems, err := ip.resolveAtBrace(raw[i+2 : i+2+end])
			if err != nil {
				return nil, err
			}
			flush()
			pieces = append(pieces, elems)
			i = i + 2 + end + 1
		default:
			buf.WriteByte(c)
			i++
		}
	}
	flush()

	result := []string{""}
	for _, piece := range pieces {
		var next []string
		for _, pre := range result {
			for _, e := range piece {
				next = append(next, pre+e)
			}
		}
		result = next
	}
	return result, nil
}

func (ip *interp) resolveDollarBrace(inside string) (string, error) {
	trimmed := strings.TrimSpace(inside)

	// inline conditional: ${if cond; true; false}
	if strings.HasPrefix(trimmed, "if ") {
		clauses := strings.Split(trimmed[3:], ";")
		if len(clauses) < 2 {
			return "", fmt.Errorf("malformed ${if …}: %q", inside)
		}
		cond, err := ip.evalString(clauses[0])
		if err != nil {
			return "", err
		}
		if truthy(cond) {
			return ip.evalToString(clauses[1])
		}
		if len(clauses) >= 3 {
			return ip.evalToString(clauses[2])
		}
		return "", nil
	}

	// optional variable: ${name?}
	if strings.HasSuffix(trimmed, "?") {
		name := strings.TrimSpace(trimmed[:len(trimmed)-1])
		if identPathRe.MatchString(name) {
			if v, ok := ip.sc.get(name); ok {
				if _, unset := v.(UnsetVal); !unset {
					return stringify(v), nil
				}
			}
			return "", nil
		}
	}

	v, err := ip.evalString(inside)
	if err != nil {
		return "", err
	}
	if _, unset := v.(UnsetVal); unset {
		name := strings.TrimSpace(inside)
		if stage, export, ok := strings.Cut(name, "."); ok && identPathRe.MatchString(stage) && identPathRe.MatchString(export) {
			return "", fmt.Errorf("${%s}: stage %q has no value %q (it may not have run, or did not export it)", name, stage, export)
		}
		return "", fmt.Errorf("undefined variable in ${%s}", name)
	}
	return stringify(v), nil
}

func (ip *interp) resolveAtBrace(inside string) ([]string, error) {
	v, err := ip.evalString(inside)
	if err != nil {
		return nil, err
	}
	items, ok := asList(v)
	if !ok {
		return []string{stringify(v)}, nil
	}
	out := make([]string, len(items))
	for i, e := range items {
		out[i] = stringify(e)
	}
	return out, nil
}

func (ip *interp) evalString(src string) (Value, error) {
	e, err := parser.ParseExpr(src)
	if err != nil {
		// name the offending expression — a bare "<expr>:1:N" is too cryptic.
		return nil, fmt.Errorf("bad expression %q: %w", src, err)
	}
	return ip.eval(e)
}

func (ip *interp) evalToString(src string) (string, error) {
	v, err := ip.evalString(src)
	if err != nil {
		return "", err
	}
	return stringify(v), nil
}

// runShell evaluates a $(…) command substitution at parse time: it interpolates
// the inner template, runs it via `sh -c`, and returns trimmed stdout.
func (ip *interp) runShell(inner string) (string, error) {
	cmdText, err := ip.interpolate(inner)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("sh", "-c", cmdText).Output()
	if err != nil {
		return "", fmt.Errorf("$(%s): %w", cmdText, err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}
