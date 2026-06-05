package lsp

import (
	"errors"

	"github.com/compgen-io/cgp/internal/parser"
)

// diagnostics parses src and returns LSP diagnostics: an empty slice when the
// file parses cleanly (which clears existing squiggles), or a single
// diagnostic for the first parse error, squiggled from the error position to
// the end of that line.
func diagnostics(src, uri string) []Diagnostic {
	if _, err := parser.Parse(src, uri); err != nil {
		return []Diagnostic{errorToDiagnostic(src, err)}
	}
	return []Diagnostic{}
}

func errorToDiagnostic(src string, err error) Diagnostic {
	starts := lineStarts(src)

	var pe *parser.Error
	if errors.As(err, &pe) {
		line0 := pe.Pos.Line - 1
		if line0 < 0 {
			line0 = 0
		}
		startChar := utf16Char(src, starts, line0, pe.Pos.Off)
		endChar := startChar + 1
		if line0 < len(starts) {
			le := len(src)
			if line0+1 < len(starts) {
				le = starts[line0+1] - 1
			}
			if ec := utf16Len(trimCR(src[starts[line0]:le])); ec > startChar {
				endChar = ec
			}
		}
		return Diagnostic{
			Range: Range{
				Start: Position{Line: line0, Character: startChar},
				End:   Position{Line: line0, Character: endChar},
			},
			Severity: 1, // Error
			Source:   "cgp",
			Message:  pe.Msg,
		}
	}

	// Fallback for an error without position information.
	return Diagnostic{
		Range:    Range{Start: Position{0, 0}, End: Position{0, 1}},
		Severity: 1,
		Source:   "cgp",
		Message:  err.Error(),
	}
}
