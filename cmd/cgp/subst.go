package main

import (
	"path/filepath"
	"strconv"
	"strings"
)

// substInput expands `{}` placeholders in s against a single fan-out input file
// (idx is its 1-based position in the file list). The placeholder grammar:
//
//	{}  {^}     the full input path
//	{@}         the basename (directory stripped)
//	{^SUF}      the full path with a trailing SUF removed (if it ends with SUF)
//	{@SUF}      the basename with a trailing SUF removed (if it ends with SUF)
//	{#}         the 1-based fan-out index
//	{{}}        a literal {}
//
// Any other `{…}` is left verbatim, so braces that aren't placeholders pass
// through untouched.
func substInput(s, file string, idx int) string {
	base := filepath.Base(file)
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "{{}}") { // escaped literal `{}`
			b.WriteString("{}")
			i += 4
			continue
		}
		if s[i] != '{' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '}')
		if end < 0 { // unterminated `{` — emit the rest verbatim
			b.WriteString(s[i:])
			break
		}
		inner := s[i+1 : i+end] // the text between { and }
		if repl, ok := expandPlaceholder(inner, file, base, idx); ok {
			b.WriteString(repl)
		} else {
			b.WriteString(s[i : i+end+1]) // unrecognized: keep `{…}` verbatim
		}
		i += end + 1
	}
	return b.String()
}

// expandPlaceholder resolves the inside of a `{…}` placeholder. ok is false for
// anything that isn't a recognized placeholder (so the caller keeps it verbatim).
func expandPlaceholder(inner, file, base string, idx int) (string, bool) {
	switch {
	case inner == "" || inner == "^":
		return file, true
	case inner == "@":
		return base, true
	case inner == "#":
		return strconv.Itoa(idx), true
	case strings.HasPrefix(inner, "^"):
		// TrimSuffix is a no-op when the suffix isn't present — exactly the
		// "strip if it ends with SUF, else unchanged" semantics we want.
		return strings.TrimSuffix(file, inner[1:]), true
	case strings.HasPrefix(inner, "@"):
		return strings.TrimSuffix(base, inner[1:]), true
	}
	return "", false
}

// substAll applies substInput to every string in ss against the same file/index.
func substAll(ss []string, file string, idx int) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = substInput(s, file, idx)
	}
	return out
}
