// Package convert is a best-effort migrator from legacy (JVM-cgpipe-era)
// scripts to the cgpipe language. It rewrites the mechanical, line-level
// differences and annotates anything it cannot safely convert with a
// "# cgpipe-convert:" comment so a human can finish the job.
//
// What it handles:
//   - shebang  #!.../cgpipe        -> #!/usr/bin/env cgp
//   - settings cgpipe.*            -> cgp.*  (and cgpipe.joblog -> cgp.ledger)
//   - control  if/elif/else/endif  -> brace blocks; for/done -> brace blocks
//   - targets  out: in  + indented body  -> out: in {{ ... }}
//   - special  __pre__:: etc.      -> @pre { ... } (and post/setup/teardown/postsubmit)
//   - snippets name::              -> snippet name {{ ... }}
//   - bodies   <% job.x = .. %>    -> directive block + "--"
//     <% if/for .. %>     -> %-prefixed control lines
//     <% import name %>   -> @name
//   - make vars $< $> $% $<N $>N   -> ${input} ${output} ${stem} ${input[N-1]} ...
//
// What it flags (inline "<% ... %>" mixed into a shell line, unknown special
// targets, job.* settings appearing after the shell starts) is passed through
// with a "# cgpipe-convert:" note rather than being silently mis-translated.
package convert

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Convert returns the converted source plus a list of human-readable warnings
// (also emitted inline as "# cgpipe-convert:" comments in the output).
func Convert(src string) (string, []string) {
	c := &converter{}
	lines := strings.Split(src, "\n")
	c.global(lines)
	return strings.Join(c.out, "\n"), c.warnings
}

type converter struct {
	out      []string
	warnings []string
	lineno   int
}

func (c *converter) emit(s string) { c.out = append(c.out, s) }
func (c *converter) warn(format string, a ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf("line %d: ", c.lineno)+fmt.Sprintf(format, a...))
}

var (
	shebangRe   = regexp.MustCompile(`^#!.*\bcgpipe\b`)
	assignRe    = regexp.MustCompile(`^\s*[A-Za-z_][\w.]*\s*(\?=|\+=|=)`)
	snippetRe   = regexp.MustCompile(`^([A-Za-z_][\w]*)::\s*$`)
	specialRe   = regexp.MustCompile(`^__([a-z]+)__::\s*$`)
	controlRe   = regexp.MustCompile(`^(if|elif|else|endif|for|done)\b`)
	keywordRe   = regexp.MustCompile(`^(if|elif|else|endif|for|done|while|print|println|exit|include|import|unset|eval|log|sleep|dumpvars|showhelp|export|return)\b`)
	inputIdxRe  = regexp.MustCompile(`\$<(\d+)`)
	outputIdxRe = regexp.MustCompile(`\$>(\d+)`)
	// a legacy parallel/zip for loop: "for a, b in xs, ys"
	multiVarForRe = regexp.MustCompile(`^for\s+[A-Za-z_]\w*\s*,`)
)

var specialTargets = map[string]string{
	"pre": "@pre", "post": "@post", "setup": "@setup",
	"teardown": "@teardown", "postsubmit": "@postsubmit",
}

// global processes lines in the top-level (cgpipe-code) context.
func (c *converter) global(lines []string) {
	i := 0
	for i < len(lines) {
		c.lineno = i + 1
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// shebang
		if i == 0 && shebangRe.MatchString(line) {
			c.emit("#!/usr/bin/env cgp")
			i++
			continue
		}
		// blank / comment
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			c.emit(line)
			i++
			continue
		}
		// control flow -> braces
		if controlRe.MatchString(trimmed) {
			if multiVarForRe.MatchString(trimmed) {
				c.warn("multi-variable for loop %q — cgpipe's for takes a single variable; rewrite by hand (e.g. index with a counter)", trimmed)
				c.emit(indentOf(line) + "# cgpipe-convert: rewrite this multi-variable for loop")
			}
			c.emit(indentOf(line) + wrapBareCmdSubst(convertControl(trimmed)))
			i++
			continue
		}
		// special target:  __pre__:: etc. (checked before the snippet rule,
		// which would otherwise match the leading "__")
		if m := specialRe.FindStringSubmatch(trimmed); m != nil {
			head, ok := specialTargets[m[1]]
			if !ok {
				head = "@" + m[1]
				c.warn("unknown special target __%s__; emitted as %s", m[1], head)
			}
			c.emit(head + " {{")
			i = c.body(lines, i+1, indentWidth(line))
			c.emit("}}")
			continue
		}
		// snippet definition:  name::
		if m := snippetRe.FindStringSubmatch(trimmed); m != nil {
			c.emit("snippet " + m[1] + " {{")
			i = c.body(lines, i+1, indentWidth(line))
			c.emit("}}")
			continue
		}
		// assignment / statement (rewrite cgpipe.* -> cgp.*)
		if assignRe.MatchString(line) || keywordRe.MatchString(trimmed) {
			c.emit(wrapBareCmdSubst(rewriteSettings(line)))
			i++
			continue
		}
		// target header:  outputs : inputs
		if isTargetHeader(trimmed) {
			c.emit(rewriteSettings(line) + " {{")
			// bodyless aggregator? (no more-indented body follows)
			if !hasIndentedBody(lines, i+1, indentWidth(line)) {
				// no body: re-emit as a plain bodyless target (drop the {{)
				c.out[len(c.out)-1] = rewriteSettings(line)
				i++
				continue
			}
			i = c.body(lines, i+1, indentWidth(line))
			c.emit("}}")
			continue
		}
		// fallthrough: pass through untouched
		c.emit(line)
		i++
	}
}

// hasIndentedBody reports whether the first non-blank line at/after idx is
// indented more than the header (i.e. belongs to a target body). Targets can be
// nested inside for/if blocks, so the test is "more indented than the header",
// not "indented at all".
func hasIndentedBody(lines []string, idx, headerIndent int) bool {
	for j := idx; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		return indentWidth(lines[j]) > headerIndent
	}
	return false
}

// body consumes an indentation-delimited body starting at idx, emits the
// converted body (directives, "--", shell, %-lines), and returns the index of
// the first line that is not part of the body. A body line is one that is blank
// or indented more than the header; the body ends at the first non-blank line
// indented at or below the header (the next statement/target, or a closing
// done/endif of an enclosing block).
func (c *converter) body(lines []string, idx, headerIndent int) int {
	end := idx // one past the last confirmed body line
	i := idx
	for i < len(lines) {
		l := lines[i]
		t := strings.TrimSpace(l)
		if t == "" { // blank: tentative (kept only if more body follows)
			i++
			continue
		}
		if indentWidth(l) > headerIndent {
			end = i + 1
			i++
			continue
		}
		// dedented line. A comment can sit at column 0 inside an indented body
		// (legacy scripts do this), so a comment ends the body only when the
		// next real line is also dedented; otherwise it is mid-body.
		if strings.HasPrefix(t, "#") && !nextRealDedented(lines, i+1, headerIndent) {
			end = i + 1
			i++
			continue
		}
		break
	}
	raw := lines[idx:end]
	c.emitBody(raw, idx+1)
	return end
}

// nextRealDedented reports whether the first non-blank, non-comment line at or
// after idx is indented at or below headerIndent (i.e. the body has ended).
func nextRealDedented(lines []string, idx, headerIndent int) bool {
	for j := idx; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		return indentWidth(lines[j]) <= headerIndent
	}
	return true // EOF: nothing more belongs to the body
}

// indentWidth returns the visual indentation of a line (space = 1, tab rounds
// up to the next multiple of 8).
func indentWidth(line string) int {
	w := 0
	for _, r := range line {
		switch r {
		case ' ':
			w++
		case '\t':
			w += 8 - (w % 8)
		default:
			return w
		}
	}
	return w
}

// a parsed body item
type item struct {
	kind  string   // "shell" | "blank" | "region" | "inline"
	text  string   // shell/inline text
	inner []string // region: the cgpipe lines between <% and %>
	line  int      // 0-based index within the raw body
}

// emitBody converts a collected raw body and emits it.
func (c *converter) emitBody(raw []string, base int) {
	items := scanBody(raw)

	// Leading phase: blank lines and all-assignment regions become directives.
	var directives []string
	k := 0
	for k < len(items) {
		it := items[k]
		if it.kind == "blank" {
			k++
			continue
		}
		if it.kind == "region" && regionAllAssign(it.inner) {
			for _, d := range it.inner {
				d = strings.TrimSpace(d)
				if d == "" {
					continue
				}
				directives = append(directives, "    "+wrapBareCmdSubst(makeVars(rewriteSettings(d))))
			}
			k++
			continue
		}
		break
	}
	if len(directives) > 0 {
		for _, d := range directives {
			c.emit(d)
		}
		c.emit("    --")
	}

	// Body phase: emit remaining items in order.
	for ; k < len(items); k++ {
		it := items[k]
		c.lineno = base + it.line
		switch it.kind {
		case "blank":
			c.emit("")
		case "shell":
			c.emit(makeVars(it.text))
		case "inline":
			c.warn("inline <%% ... %%> on a shell line — convert by hand (e.g. ${if cond; ...})")
			c.emit("    # cgpipe-convert: review inline <% %> below")
			c.emit(makeVars(it.text))
		case "region":
			c.emitRegion(it.inner)
		}
	}
}

// emitRegion converts the cgpipe lines of a non-leading <% %> region into
// %-control lines (control flow), @name (import), or %-assignments.
func (c *converter) emitRegion(inner []string) {
	for _, l := range inner {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		switch {
		case controlRe.MatchString(t):
			if hasMakeVar(t) {
				c.warn("make-var ($< / $> / $%%) in a control condition %q — review (use input[N] / input.length() in code context)", t)
			}
			c.emit("    % " + wrapBareCmdSubst(convertControl(t)))
		case strings.HasPrefix(t, "import "):
			name := strings.TrimSpace(strings.TrimPrefix(t, "import"))
			c.emit("    @" + name)
		default:
			// an assignment appearing after the shell started: keep it as a
			// %-line but flag it, since job.* settings belong in the directive
			// block before "--".
			c.warn("setting %q appears after the body started; moved to a %%-line (review)", t)
			c.emit("    % " + wrapBareCmdSubst(makeVars(rewriteSettings(t))))
		}
	}
}

// scanBody splits raw body lines into items, recognizing <% ... %> regions
// (single- and multi-line) and inline mixes.
func scanBody(raw []string) []item {
	var items []item
	i := 0
	for i < len(raw) {
		start := i
		line := raw[i]
		t := strings.TrimSpace(line)
		if t == "" {
			items = append(items, item{kind: "blank", line: start})
			i++
			continue
		}
		if !strings.Contains(t, "<%") {
			items = append(items, item{kind: "shell", text: line, line: start})
			i++
			continue
		}
		// has <%
		open := strings.Index(t, "<%")
		close := strings.Index(t, "%>")
		if close >= 0 {
			// single-line region; standalone only if nothing meaningful outside
			before := strings.TrimSpace(t[:open])
			after := strings.TrimSpace(t[close+2:])
			if before == "" && after == "" && strings.Count(t, "<%") == 1 {
				inner := t[open+2 : close]
				items = append(items, item{kind: "region", inner: splitRegion(inner), line: start})
				i++
				continue
			}
			// inline
			items = append(items, item{kind: "inline", text: line, line: start})
			i++
			continue
		}
		// multi-line region: collect until a line containing %>
		before := strings.TrimSpace(t[:open])
		var inner []string
		if rest := strings.TrimSpace(t[open+2:]); rest != "" {
			inner = append(inner, rest)
		}
		i++
		for i < len(raw) {
			l := raw[i]
			if ci := strings.Index(l, "%>"); ci >= 0 {
				if head := strings.TrimSpace(l[:ci]); head != "" {
					inner = append(inner, head)
				}
				i++
				break
			}
			inner = append(inner, l)
			i++
		}
		if before != "" {
			// shell text preceding a multi-line region opener — unusual; flag it
			items = append(items, item{kind: "inline", text: before, line: start})
		}
		items = append(items, item{kind: "region", inner: inner, line: start})
	}
	return items
}

// splitRegion splits a single-line region's inner text into statements. A
// single-line settings region may pack several "a=b" on one line is uncommon;
// we treat the whole inner as one statement unless it is clearly multiple.
func splitRegion(inner string) []string {
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return nil
	}
	return []string{inner}
}

// regionAllAssign reports whether every non-blank inner line is an assignment
// (so the region is a settings/directive block, not control flow or import).
func regionAllAssign(inner []string) bool {
	any := false
	for _, l := range inner {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		any = true
		if controlRe.MatchString(t) || strings.HasPrefix(t, "import ") {
			return false
		}
		if !assignRe.MatchString(t) {
			return false
		}
	}
	return any
}

// convertControl rewrites a legacy control line into brace form.
func convertControl(t string) string {
	switch {
	case t == "endif" || t == "done":
		return "}"
	case t == "else":
		return "} else {"
	case strings.HasPrefix(t, "elif"):
		return "} elif " + strings.TrimSpace(t[len("elif"):]) + " {"
	case strings.HasPrefix(t, "if"):
		return "if " + strings.TrimSpace(t[len("if"):]) + " {"
	case strings.HasPrefix(t, "for"):
		return "for " + strings.TrimSpace(t[len("for"):]) + " {"
	}
	return t
}

// rewriteSettings maps legacy JVM-era setting names to their cgp.* names:
// the whole cgpipe.* namespace becomes cgp.*, and cgpipe.joblog is renamed
// to cgp.ledger.
func rewriteSettings(line string) string {
	line = strings.ReplaceAll(line, "cgpipe.joblog", "cgp.ledger")
	line = strings.ReplaceAll(line, "cgpipe.", "cgp.")
	return line
}

// makeVars substitutes the legacy make-style build variables.
func makeVars(s string) string {
	s = inputIdxRe.ReplaceAllStringFunc(s, func(m string) string {
		n := m[2:] // digits after $<
		return "${input[" + decr(n) + "]}"
	})
	s = outputIdxRe.ReplaceAllStringFunc(s, func(m string) string {
		n := m[2:]
		return "${output[" + decr(n) + "]}"
	})
	s = strings.ReplaceAll(s, "$<", "${input}")
	s = strings.ReplaceAll(s, "$>", "${output}")
	s = strings.ReplaceAll(s, "$%", "${stem}")
	return s
}

// wrapBareCmdSubst wraps any bare $(...) command substitution that appears
// outside a double-quoted string in double quotes, so it becomes a valid cgpipe
// string-valued expression (legacy scripts use bare $(cmd) in conditions and
// assignments; cgpipe's $(cmd) lives inside a string literal). Internal `\` and `"`
// are escaped so the command text survives cgpipe string parsing intact. This is
// only applied in cgpipe-code contexts — never to shell body lines, where $(...)
// is real shell command substitution and must stay bare.
func wrapBareCmdSubst(s string) string {
	var b strings.Builder
	inStr := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inStr {
			b.WriteByte(ch)
			if ch == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
				continue
			}
			if ch == '"' {
				inStr = false
			}
			continue
		}
		if ch == '"' {
			inStr = true
			b.WriteByte(ch)
			continue
		}
		if ch == '$' && i+1 < len(s) && s[i+1] == '(' {
			depth := 0
			j := i + 1
			for j < len(s) {
				switch s[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
				j++
				if depth == 0 {
					break
				}
			}
			cmd := s[i:j] // includes the $( ... )
			esc := strings.ReplaceAll(cmd, `\`, `\\`)
			esc = strings.ReplaceAll(esc, `"`, `\"`)
			b.WriteByte('"')
			b.WriteString(esc)
			b.WriteByte('"')
			i = j - 1
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// hasMakeVar reports whether s contains a legacy make-style build variable.
func hasMakeVar(s string) bool {
	return strings.Contains(s, "$<") || strings.Contains(s, "$>") || strings.Contains(s, "$%")
}

// decr returns n-1 for a positive integer string (1-based -> 0-based); anything
// else (0 or non-numeric) is returned unchanged.
func decr(n string) string {
	if v, err := strconv.Atoi(n); err == nil && v > 0 {
		return strconv.Itoa(v - 1)
	}
	return n
}

// isTargetHeader reports whether a trimmed line is a target declaration
// (outputs : inputs), as opposed to an assignment, statement, or comment.
func isTargetHeader(t string) bool {
	if t == "" || strings.HasPrefix(t, "#") {
		return false
	}
	if keywordRe.MatchString(t) {
		return false
	}
	if assignRe.MatchString(t) {
		return false
	}
	if strings.Contains(t, "::") {
		return false
	}
	// must contain a top-level ':' separating outputs from inputs
	return strings.Contains(t, ":")
}

func indentOf(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}
