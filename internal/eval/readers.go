package eval

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// writeHandle is the shared, mutable state behind a "w"/"a" file handle: the open
// file plus its closed flag. Under dry-run it holds no file and every operation is
// a no-op. Copies of a FileVal share one writeHandle, so write/close act on the
// same file regardless of which copy is used.
type writeHandle struct {
	path   string
	f      *os.File // nil under dry-run
	closed bool
	dryRun bool
	owned  bool // adopted by a scope frame (closed when that frame exits)
}

func (h *writeHandle) write(s string) error {
	if h.dryRun || h.f == nil {
		return nil
	}
	if h.closed {
		return fmt.Errorf("write to closed file %q", h.path)
	}
	_, err := h.f.WriteString(s)
	return err
}

func (h *writeHandle) close() error {
	if h.closed || h.dryRun || h.f == nil {
		h.closed = true
		return nil
	}
	h.closed = true
	return h.f.Close()
}

// fileMethod implements the methods on a file handle (from open()). Read methods
// (read_tsv, …) act lazily when called and require an "r" handle; write/writeln/
// close require a "w"/"a" handle. open() itself only records the path (and, for a
// write handle, opens the file).
func fileMethod(f FileVal, name string, args []Value, kwargs map[string]Value) (Value, error) {
	// Every method except write/writeln takes no positional arguments.
	if name != "write" && name != "writeln" && len(args) != 0 {
		return nil, fmt.Errorf("file.%s() takes no positional arguments", name)
	}
	switch name {
	// ---- introspection (any mode) ----
	case "type":
		return StrVal("file"), nil
	case "path":
		return StrVal(f.path), nil
	case "exists":
		_, err := os.Stat(f.path)
		return BoolVal(err == nil), nil

	// ---- writing ("w"/"a" handle) ----
	case "write", "writeln":
		if f.w == nil {
			return nil, fmt.Errorf("file.%s(): %q is not open for writing (use open(path, \"w\"))", name, f.path)
		}
		if len(args) != 1 {
			return nil, fmt.Errorf("file.%s() takes 1 argument", name)
		}
		s := stringify(args[0])
		if name == "writeln" {
			s += "\n"
		}
		if err := f.w.write(s); err != nil {
			return nil, err
		}
		return f, nil // chainable: f.write(a).write(b)
	case "close":
		if f.w == nil {
			return nil, fmt.Errorf("file.close(): %q is not open for writing", f.path)
		}
		if err := f.w.close(); err != nil {
			return nil, err
		}
		return UnsetVal{}, nil
	}

	// ---- reading ("r" handle) ----
	if f.mode != "r" {
		return nil, fmt.Errorf("file.%s(): %q is open for writing; cannot read", name, f.path)
	}
	switch name {
	case "read":
		if err := validateKwargs("file.read", kwargs); err != nil {
			return nil, err
		}
		return readWhole(f.path)
	case "read_tsv":
		return readTabular(f.path, '\t', kwargs)
	case "read_csv":
		return readTabular(f.path, ',', kwargs)
	case "read_json":
		if err := validateKwargs("file.read_json", kwargs); err != nil {
			return nil, err
		}
		return readJSONRecords(f.path)
	case "read_lines":
		if err := validateKwargs("file.read_lines", kwargs, "comment", "skip", "blank"); err != nil {
			return nil, err
		}
		return readLinesFile(f.path, strKw(kwargs, "comment", ""), intKw(kwargs, "skip", 0), boolKw(kwargs, "blank", true))
	}
	return nil, fmt.Errorf("method not found: file.%s()", name)
}

// readTabular reads a delimited file (TSV when defComma is '\t', CSV when ',')
// into a list of maps, one per data row, keyed by header column name.
func readTabular(path string, defComma rune, kwargs map[string]Value) (Value, error) {
	if err := validateKwargs("read", kwargs, "header", "sep", "comment", "skip", "raw"); err != nil {
		return nil, err
	}
	comma := defComma
	if sv, ok := kwargs["sep"]; ok {
		rs := []rune(stringify(sv))
		if len(rs) != 1 {
			return nil, fmt.Errorf("read sep must be a single character, got %q", stringify(sv))
		}
		comma = rs[0]
	}
	return readDelimited(path, comma, boolKw(kwargs, "header", true), strKw(kwargs, "comment", "#"), intKw(kwargs, "skip", 0), boolKw(kwargs, "raw", false))
}

// readDelimited parses a delimited file into a ListVal of MapVal rows. With a
// header, the first row names the columns; without one, columns are keyed
// positionally as c0, c1, …. Cells are auto-typed via ParseScalar unless raw.
func readDelimited(path string, comma rune, header bool, comment string, skip int, raw bool) (ListVal, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.Comma = comma
	if comment != "" {
		r.Comment = []rune(comment)[0]
	}
	r.FieldsPerRecord = -1 // tolerate ragged rows
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if skip > 0 {
		if skip >= len(records) {
			records = nil
		} else {
			records = records[skip:]
		}
	}
	if len(records) == 0 {
		return ListVal{}, nil
	}

	var keys []string
	data := records
	if header {
		keys = records[0]
		data = records[1:]
	} else {
		width := 0
		for _, rec := range records {
			if len(rec) > width {
				width = len(rec)
			}
		}
		keys = make([]string, width)
		for i := range keys {
			keys[i] = fmt.Sprintf("c%d", i)
		}
	}

	out := make(ListVal, 0, len(data))
	for _, rec := range data {
		mv := newMap()
		for i, col := range keys {
			cell := ""
			if i < len(rec) {
				cell = rec[i]
			}
			if raw {
				mv.set(col, StrVal(cell))
			} else {
				mv.set(col, ParseScalar(cell))
			}
		}
		out = append(out, mv)
	}
	return out, nil
}

// readJSONRecords reads a JSON array of objects into a ListVal of MapVal. Object
// keys are sorted deterministically (encoding/json does not preserve order).
func readJSONRecords(path string) (ListVal, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw []map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w (expected a JSON array of objects)", path, err)
	}
	out := make(ListVal, len(raw))
	for i, obj := range raw {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		mv := newMap()
		for _, k := range keys {
			mv.set(k, jsonValue(obj[k]))
		}
		out[i] = mv
	}
	return out, nil
}

// readLinesFile reads a file into a ListVal of StrVal lines. Lines whose first
// non-space text starts with comment are dropped (when comment != ""); blank
// lines are dropped unless keepBlank. skip drops that many leading lines first.
func readLinesFile(path, comment string, skip int, keepBlank bool) (ListVal, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(b)
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	// a trailing newline yields a spurious final empty element — drop it
	if n := len(lines); n > 0 && lines[n-1] == "" && strings.HasSuffix(text, "\n") {
		lines = lines[:n-1]
	}
	if skip > 0 {
		if skip >= len(lines) {
			lines = nil
		} else {
			lines = lines[skip:]
		}
	}
	out := ListVal{}
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if comment != "" && strings.HasPrefix(trimmed, comment) {
			continue
		}
		if !keepBlank && trimmed == "" {
			continue
		}
		out = append(out, StrVal(ln))
	}
	return out, nil
}

func readWhole(path string) (Value, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return StrVal(string(b)), nil
}

// jsonValue maps a decoded JSON scalar to a cgpipe value (whole floats become ints).
func jsonValue(v any) Value {
	switch x := v.(type) {
	case string:
		return StrVal(x)
	case bool:
		return BoolVal(x)
	case float64:
		if x == math.Trunc(x) {
			return IntVal(int64(x))
		}
		return FloatVal(x)
	case nil:
		return StrVal("")
	default:
		return StrVal(fmt.Sprintf("%v", x))
	}
}

// ---- keyword-argument helpers ----

// validateKwargs rejects any keyword not in allowed (typo protection).
func validateKwargs(fn string, kwargs map[string]Value, allowed ...string) error {
	for k := range kwargs {
		found := false
		for _, a := range allowed {
			if a == k {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s(): unknown keyword argument %q", fn, k)
		}
	}
	return nil
}

func strKw(kwargs map[string]Value, name, def string) string {
	if v, ok := kwargs[name]; ok {
		return stringify(v)
	}
	return def
}

func boolKw(kwargs map[string]Value, name string, def bool) bool {
	if v, ok := kwargs[name]; ok {
		return truthy(v)
	}
	return def
}

func intKw(kwargs map[string]Value, name string, def int) int {
	if v, ok := kwargs[name]; ok {
		return int(toInt(v))
	}
	return def
}
