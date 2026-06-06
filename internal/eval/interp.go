package eval

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"github.com/compgen-io/cgp/internal/lexer"
	"github.com/compgen-io/cgp/internal/parser"
	"github.com/compgen-io/cgp/internal/token"
)

var identPathRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// tmplMode selects the escape policy for a template. The two template sources
// have opposite expectations for the backslash:
//
//   - modeString — a cgp "…" string literal. It is one escape domain: every
//     `\X` resolves to `X` (the lexer kept escapes verbatim), including inside a
//     ${…}/@{…}, so a nested string written `\"…\"` parses as `"…"`.
//   - modeBody — a {{ }} raw-shell body. Only `\$` and `\@` are special (they
//     suppress ${…}/@{…}/$(…)); every other backslash — `\"`, `\\`, `\n` — is
//     shell text and passes through verbatim.
type tmplMode int

const (
	modeString tmplMode = iota
	modeBody
)

// Interpolate resolves a template string against the given variables. Used by
// workflow orchestration to resolve stage names/files/args (which reference
// ${prior_stage.export}) as each stage runs.
func Interpolate(raw string, vars map[string]Value) (string, error) {
	sc := newScope()
	for k, v := range vars {
		sc.set(k, v)
	}
	ip := &interp{sc: sc, out: io.Discard, prog: &Program{Snippets: map[string]string{}, Exports: map[string]Value{}}}
	return ip.interpolate(raw, modeString)
}

// interpolate resolves a raw template to a single string (@{…} expansions are
// joined with spaces).
func (ip *interp) interpolate(raw string, mode tmplMode) (string, error) {
	parts, err := ip.expandTemplate(raw, mode)
	if err != nil {
		return "", err
	}
	return strings.Join(parts, " "), nil
}

// expandTemplate resolves ${…}/$(…)/@{…} and \ escapes in raw, returning one or
// more strings (more than one when @{…} expands a list — a cartesian product
// across multiple @{…} occurrences).
func (ip *interp) expandTemplate(raw string, mode tmplMode) ([]string, error) {
	var pieces [][]string
	var buf strings.Builder
	flush := func() {
		pieces = append(pieces, []string{buf.String()})
		buf.Reset()
	}
	// unesc resolves the cgp escape layer of a ${…}/@{…} interior — a string
	// literal had to escape any quote (\") to survive the outer "…", so the
	// interior reaches the expression parser still escaped. A body's interior has
	// real quotes already, so it is left verbatim.
	unesc := func(s string) string {
		if mode == modeString {
			return unescapeBackslashes(s)
		}
		return s
	}

	for i := 0; i < len(raw); {
		c := raw[i]
		switch {
		case c == '\\' && i+1 < len(raw):
			// modeString: every \X -> X. modeBody: only \$ and \@ are special
			// (suppress interpolation); any other backslash is literal shell text.
			n := raw[i+1]
			if mode == modeString || n == '$' || n == '@' {
				buf.WriteByte(n)
				i += 2
			} else {
				buf.WriteByte('\\')
				i++
			}
		case c == '$' && i+2 < len(raw) && raw[i+1] == '{' && raw[i+2] == '{':
			// ${{ var }} double-evaluation: substitute, then evaluate the result
			end := strings.Index(raw[i+3:], "}}")
			if end < 0 {
				return nil, fmt.Errorf("unterminated ${{ in %q", raw)
			}
			v, err := ip.evalString(unesc(raw[i+3 : i+3+end]))
			if err != nil {
				return nil, err
			}
			s, err := ip.interpolate(stringify(v), modeString)
			if err != nil {
				return nil, err
			}
			buf.WriteString(s)
			i = i + 3 + end + 2
		case c == '$' && i+1 < len(raw) && raw[i+1] == '{':
			end := braceSpan(raw[i+2:])
			if end < 0 {
				return nil, fmt.Errorf("unterminated ${ in %q", raw)
			}
			s, err := ip.resolveDollarBrace(unesc(raw[i+2 : i+2+end]))
			if err != nil {
				return nil, err
			}
			buf.WriteString(s)
			i = i + 2 + end + 1
		case c == '$' && i+1 < len(raw) && raw[i+1] == '(':
			end := parenSpan(raw[i+2:])
			if end < 0 {
				return nil, fmt.Errorf("unterminated $( in %q", raw)
			}
			out, err := ip.runShell(raw[i+2:i+2+end], mode)
			if err != nil {
				return nil, err
			}
			buf.WriteString(out)
			i = i + 2 + end + 1
		case c == '@' && i+1 < len(raw) && raw[i+1] == '{':
			end := braceSpan(raw[i+2:])
			if end < 0 {
				return nil, fmt.Errorf("unterminated @{ in %q", raw)
			}
			elems, err := ip.resolveAtBrace(unesc(raw[i+2 : i+2+end]))
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

// braceSpan returns the byte offset in s of the `}` that closes a ${…} or @{…}
// whose opening has already been consumed, or -1 if unterminated. A double-quoted
// substring is opaque (its braces are content, not structure), and \-escaped
// bytes are skipped. Crucially it toggles "in string" on either a real `"` or an
// escaped `\"`, so it finds the right `}` for both a body's `${if x; "a}b"}`
// (real quotes) and a string literal's `${if x; \"a}b\"}` (escaped quotes, not
// yet unescaped) — including a nested ${…} inside the quoted branch.
func braceSpan(s string) int {
	depth, inStr := 0, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			if s[i+1] == '"' {
				inStr = !inStr // an escaped quote is a string delimiter here
			}
			i++ // skip the escaped byte
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// unescapeBackslashes resolves `\X` -> `X` for every escape in s. Applied to a
// ${…}/@{…} interior lifted from a "…" string literal, so the expression parser
// sees real quotes (\" -> ") instead of the verbatim escapes the lexer preserved.
func unescapeBackslashes(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parenSpan returns the byte offset in s of the `)` that closes a $( … ) whose
// opening `$(` has already been consumed. The content is shell, not cgp, so it
// is scanned with shell quoting rules — single quotes are literal (no escapes),
// double quotes honor \-escapes, and a backslash escapes the next byte outside
// quotes — while nested `( )` are balanced. Returns -1 if unterminated. Exotic
// shell that bash also parses (here-docs, # comments, ${…}) is not handled; for
// those, use a cgp variable or \$(…) to defer to the runtime shell.
func parenSpan(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++ // skip the escaped byte
		case '\'':
			i++ // single-quoted: literal until the next single quote
			for i < len(s) && s[i] != '\'' {
				i++
			}
		case '"':
			i++ // double-quoted: \-escapes apply
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' {
					i++
				}
				i++
			}
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// splitClauses splits the body of a ${if cond; a; b} on the `;` separators that
// the lexer reports as SEMI tokens at brace depth zero — so a `;` inside a
// string literal (a single STRING token) or a nested ${…} does not split. The
// returned clause strings are slices of the original (their own ${…} are
// resolved later, when each clause is evaluated).
func splitClauses(s string) []string {
	lx := lexer.New(s, "<expr>")
	var parts []string
	depth, start := 0, 0
	for {
		t := lx.Next()
		switch t.Kind {
		case token.EOF:
			return append(parts, s[start:])
		case token.LBRACE:
			depth++
		case token.RBRACE:
			if depth > 0 {
				depth--
			}
		case token.SEMI:
			if depth == 0 {
				parts = append(parts, s[start:t.Pos.Off])
				start = t.Pos.Off + 1
			}
		}
		// ILLEGAL tokens are ignored (the lexer advances past them).
	}
}

func (ip *interp) resolveDollarBrace(inside string) (string, error) {
	trimmed := strings.TrimSpace(inside)

	// inline conditional: ${if cond; true; false}
	if strings.HasPrefix(trimmed, "if ") {
		clauses := splitClauses(trimmed[3:])
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
		if strings.HasPrefix(name, "job.") {
			return "", fmt.Errorf("undefined job setting ${%s}", name)
		}
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
func (ip *interp) runShell(inner string, mode tmplMode) (string, error) {
	cmdText, err := ip.interpolate(inner, mode)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("sh", "-c", cmdText).Output()
	if err != nil {
		return "", fmt.Errorf("$(%s): %w", cmdText, err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}
