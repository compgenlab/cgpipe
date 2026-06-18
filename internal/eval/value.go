package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// maxLoopIterations caps while-form for-loops (`for cond { … }` in global code and
// `% for cond` in a body) as a runaway-loop backstop. Shared by the global
// statement evaluator (execFor in eval.go) and the body renderer (renderNodes in
// body.go).
//
// Future work: those two interpreters duplicate the if/for control-flow logic.
// Unifying them is deferred — the body path also interleaves shell-line emission,
// per-target render scope, and snippet expansion, so a full merge is high-risk.
// For now they share this constant plus the value helpers (asList, truthy).
const maxLoopIterations = 1_000_000

// Value is a cgpipe runtime value.
type Value interface{ typeName() string }

type (
	IntVal   int64
	FloatVal float64
	StrVal   string
	BoolVal  bool
	ListVal  []Value
	RangeVal struct{ Lo, Hi int64 }
	// UnsetVal is the value of an unset variable. It is falsy and stringifies to ""
	// in optional contexts; using it where a value is required is an error.
	UnsetVal struct{}
	// FileVal is a file handle returned by open(). It carries the path and an
	// access mode ("r" read, "w" truncate, "a" append). Reader methods (read_tsv,
	// …) hang off an "r" handle; write/writeln/close off a "w"/"a" handle. For a
	// write handle, w points to the shared open file so copies of the value all
	// write to and close the same file.
	FileVal struct {
		path string
		mode string
		w    *writeHandle
	}
	// MapVal is an ordered, string-keyed map (the type readers return per row and
	// the value of a {…} literal). It is a reference type: the ordered keys and the
	// values both live behind a shared *mapData pointer, so copying a MapVal (e.g.
	// `n = m`, a scope capture, or passing it around) aliases the same underlying
	// map — a mutation through any copy (index assignment, set) is seen by all of
	// them, key order included.
	MapVal struct{ *mapData }
)

// mapData is the shared backing of a MapVal: keys preserves insertion/column
// order, m holds the values.
type mapData struct {
	keys []string
	m    map[string]Value
}

func (IntVal) typeName() string   { return "int" }
func (FloatVal) typeName() string { return "float" }
func (StrVal) typeName() string   { return "string" }
func (BoolVal) typeName() string  { return "bool" }
func (ListVal) typeName() string  { return "list" }
func (RangeVal) typeName() string { return "range" }
func (UnsetVal) typeName() string { return "unset" }
func (FileVal) typeName() string  { return "file" }
func (MapVal) typeName() string   { return "map" }

// newMap returns an empty MapVal ready for set().
func newMap() MapVal { return MapVal{&mapData{m: map[string]Value{}}} }

// set stores v under key k, appending k to the key order if new. It mutates the
// shared backing, so every MapVal aliasing this data observes the change.
func (d *mapData) set(k string, v Value) {
	if _, ok := d.m[k]; !ok {
		d.keys = append(d.keys, k)
	}
	d.m[k] = v
}

func (r RangeVal) slice() []Value {
	var out []Value
	if r.Lo <= r.Hi {
		for i := r.Lo; i <= r.Hi; i++ {
			out = append(out, IntVal(i))
		}
	} else {
		for i := r.Lo; i >= r.Hi; i-- {
			out = append(out, IntVal(i))
		}
	}
	return out
}

// stringify renders a value as it appears in string substitution. Lists and
// ranges join with spaces.
func stringify(v Value) string {
	switch x := v.(type) {
	case IntVal:
		return strconv.FormatInt(int64(x), 10)
	case FloatVal:
		return strconv.FormatFloat(float64(x), 'g', -1, 64)
	case StrVal:
		return string(x)
	case BoolVal:
		if bool(x) {
			return "true"
		}
		return "false"
	case ListVal:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = stringify(e)
		}
		return strings.Join(parts, " ")
	case RangeVal:
		return stringify(ListVal(x.slice()))
	case FileVal:
		return x.path
	case MapVal:
		parts := make([]string, len(x.keys))
		for i, k := range x.keys {
			parts[i] = k + "=" + stringify(x.m[k])
		}
		return strings.Join(parts, " ")
	case UnsetVal:
		return ""
	}
	return ""
}

// truthy implements cgpipe's notion of truth (used by if/for-cond and !).
func truthy(v Value) bool {
	switch x := v.(type) {
	case BoolVal:
		return bool(x)
	case IntVal:
		return x != 0
	case FloatVal:
		return x != 0
	case StrVal:
		return x != ""
	case ListVal:
		return len(x) > 0
	case RangeVal:
		return x.count() > 0 // a range always yields ≥1 value; avoids materializing it
	case MapVal:
		return len(x.keys) > 0
	case UnsetVal:
		return false
	}
	return true
}

// ParseScalar parses an external scalar string (a command-line value or a
// sample-sheet cell) into a typed value: true/false become bools, integer and
// float literals become numbers, and anything else stays a string.
func ParseScalar(s string) Value {
	switch s {
	case "true":
		return BoolVal(true)
	case "false":
		return BoolVal(false)
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return IntVal(i)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return FloatVal(f)
	}
	return StrVal(s)
}

// StrList converts a slice of strings into a ListVal of StrVals.
func StrList(ss []string) ListVal {
	out := make(ListVal, len(ss))
	for i, s := range ss {
		out[i] = StrVal(s)
	}
	return out
}

// asList coerces lists, ranges, and maps to a slice of values for iteration. A
// map yields its keys (as strings) in order, so `for k in m` iterates keys.
func asList(v Value) ([]Value, bool) {
	switch x := v.(type) {
	case ListVal:
		return []Value(x), true
	case RangeVal:
		return x.slice(), true
	case MapVal:
		out := make([]Value, len(x.keys))
		for i, k := range x.keys {
			out[i] = StrVal(k)
		}
		return out, true
	}
	return nil, false
}

// ---- scope ----

// Scope is one frame of a lexical scope chain. parent links to the enclosing
// frame (nil for the root). handles holds write handles bound in THIS frame, which
// are flushed/closed when the frame exits (see interp.popScope). A `{ }` block —
// if/for body, target render — pushes a child frame; declarations (`var`) and
// new-name assignments bind locally, while a bare assignment to an existing name
// writes through to the frame that already holds it.
type Scope struct {
	vars    map[string]Value
	parent  *Scope
	handles []*writeHandle
}

func newScope() *Scope { return &Scope{vars: map[string]Value{}} }

// child returns a new frame nested inside s.
func (s *Scope) child() *Scope { return &Scope{vars: map[string]Value{}, parent: s} }

// root returns the bottom-most frame of the chain (the run/render root). Used as
// the implicit home of the reserved job.*/cgpipe.* setting namespaces.
func (s *Scope) root() *Scope {
	for s.parent != nil {
		s = s.parent
	}
	return s
}

// get resolves name up the chain, returning the nearest binding.
func (s *Scope) get(name string) (Value, bool) {
	for f := s; f != nil; f = f.parent {
		if v, ok := f.vars[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// has reports whether name is bound anywhere in the chain.
func (s *Scope) has(name string) bool { _, ok := s.get(name); return ok }

// set binds name in THIS frame (used to seed a frame's own variables — defaults,
// loop vars, input/output/stem — and as `var`'s local declaration).
func (s *Scope) set(name string, v Value) { s.vars[name] = v }

// frameOf returns the nearest frame that already binds name, or nil if none.
func (s *Scope) frameOf(name string) *Scope {
	for f := s; f != nil; f = f.parent {
		if _, ok := f.vars[name]; ok {
			return f
		}
	}
	return nil
}

// assign implements a bare `name = v`: write to the nearest enclosing frame that
// already binds name; if it is bound nowhere, create it in the current frame —
// except reserved settings (job.* / cgpipe.*), which are implicitly declared at the
// root so a conditional `job.mem`/`cgpipe.runner` inside a block still reaches the
// engine. It returns the frame the binding landed in (for write-handle ownership).
func (s *Scope) assign(name string, v Value) *Scope {
	if f := s.frameOf(name); f != nil {
		f.vars[name] = v
		return f
	}
	target := s
	if isReservedSetting(name) {
		target = s.root()
	}
	target.vars[name] = v
	return target
}

// isReservedSetting reports whether name is in the job.*/cgpipe.* setting namespace.
func isReservedSetting(name string) bool {
	return strings.HasPrefix(name, "job.") || strings.HasPrefix(name, "cgpipe.")
}

// del removes name from the nearest frame that binds it.
func (s *Scope) del(name string) {
	if f := s.frameOf(name); f != nil {
		delete(f.vars, name)
	}
}

// clone flattens the chain into a single detached root frame (inner frames shadow
// outer), used to snapshot the scope at a target's definition for later render.
// Handles are not carried over — they are owned by their live frame, not the snapshot.
func (s *Scope) clone() *Scope {
	c := newScope()
	var frames []*Scope
	for f := s; f != nil; f = f.parent {
		frames = append(frames, f)
	}
	for i := len(frames) - 1; i >= 0; i-- { // root first so inner frames overwrite
		for k, v := range frames[i].vars {
			c.vars[k] = v
		}
	}
	return c
}

// ---- methods ----

func callMethod(recv Value, name string, args []Value, kwargs map[string]Value) (Value, error) {
	switch r := recv.(type) {
	case FileVal:
		return fileMethod(r, name, args, kwargs)
	case MapVal:
		if err := noKwargs("map", name, kwargs); err != nil {
			return nil, err
		}
		return mapMethod(r, name, args)
	case StrVal:
		if err := noKwargs("string", name, kwargs); err != nil {
			return nil, err
		}
		return stringMethod(string(r), name, args)
	case ListVal:
		if err := noKwargs("list", name, kwargs); err != nil {
			return nil, err
		}
		return listMethod([]Value(r), name, args)
	case RangeVal:
		if err := noKwargs("range", name, kwargs); err != nil {
			return nil, err
		}
		return rangeMethod(r, name, args)
	}
	if name == "type" && len(args) == 0 {
		return StrVal(recv.typeName()), nil
	}
	return nil, fmt.Errorf("method not found: %s.%s()", recv.typeName(), name)
}

// noKwargs reports an error if kwargs were passed to a method that takes none.
func noKwargs(typ, name string, kwargs map[string]Value) error {
	if len(kwargs) > 0 {
		return fmt.Errorf("%s.%s() does not take keyword arguments", typ, name)
	}
	return nil
}

// mapMethod implements methods on a map. Field access is via indexing
// (m["k"] / m[0]); these methods cover inspection and conversion.
func mapMethod(mv MapVal, name string, args []Value) (Value, error) {
	switch name {
	case "type":
		return StrVal("map"), nil
	case "length":
		return IntVal(len(mv.keys)), nil
	case "keys":
		return StrList(mv.keys), nil
	case "values":
		out := make(ListVal, len(mv.keys))
		for i, k := range mv.keys {
			out[i] = mv.m[k]
		}
		return out, nil
	case "items":
		out := make(ListVal, len(mv.keys))
		for i, k := range mv.keys {
			out[i] = ListVal{StrVal(k), mv.m[k]}
		}
		return out, nil
	case "has":
		if len(args) != 1 {
			return nil, fmt.Errorf("map.has() takes 1 argument")
		}
		_, ok := mv.m[stringify(args[0])]
		return BoolVal(ok), nil
	case "get":
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("map.get() takes a key and an optional default")
		}
		var def Value = UnsetVal{}
		if len(args) == 2 {
			def = args[1]
		}
		if iv, ok := args[0].(IntVal); ok { // positional lookup
			i := int(iv)
			if i < 0 {
				i += len(mv.keys)
			}
			if i < 0 || i >= len(mv.keys) {
				return def, nil
			}
			return mv.m[mv.keys[i]], nil
		}
		if v, ok := mv.m[stringify(args[0])]; ok {
			return v, nil
		}
		return def, nil
	}
	return nil, fmt.Errorf("method not found: map.%s()", name)
}

// count returns the number of values a range yields without materializing them.
func (r RangeVal) count() int64 {
	n := r.Hi - r.Lo
	if n < 0 {
		n = -n
	}
	return n + 1
}

// rangeMethod implements methods on a range. type/length/contains are answered
// from the two bounds alone — a range stores only Lo and Hi, never one value
// per element, so these stay O(1) even for 1..1_000_000. Other list-compatible
// methods (a range "passes anywhere a list is accepted", §9.4) fall back to the
// materialized slice.
func rangeMethod(r RangeVal, name string, args []Value) (Value, error) {
	switch name {
	case "type":
		return StrVal("range"), nil
	case "length":
		return IntVal(r.count()), nil
	case "contains":
		if len(args) != 1 {
			return nil, fmt.Errorf("range.contains() takes 1 argument")
		}
		iv, ok := args[0].(IntVal)
		if !ok {
			return BoolVal(false), nil
		}
		x, lo, hi := int64(iv), r.Lo, r.Hi
		if lo > hi {
			lo, hi = hi, lo
		}
		return BoolVal(x >= lo && x <= hi), nil
	}
	return listMethod(r.slice(), name, args)
}

func stringMethod(s, name string, args []Value) (Value, error) {
	switch name {
	case "type":
		return StrVal("string"), nil
	case "upper":
		return StrVal(strings.ToUpper(s)), nil
	case "lower":
		return StrVal(strings.ToLower(s)), nil
	case "length":
		return IntVal(len(s)), nil
	case "basename":
		return StrVal(filepath.Base(s)), nil
	case "dirname":
		return StrVal(filepath.Dir(s)), nil
	case "abspath":
		p, err := filepath.Abs(s)
		if err != nil {
			return nil, err
		}
		return StrVal(p), nil
	case "exists":
		_, err := os.Stat(s)
		return BoolVal(err == nil), nil
	case "isfile":
		fi, err := os.Stat(s)
		return BoolVal(err == nil && fi.Mode().IsRegular()), nil
	case "isdir":
		fi, err := os.Stat(s)
		return BoolVal(err == nil && fi.IsDir()), nil
	case "contains":
		if len(args) != 1 {
			return nil, fmt.Errorf("string.contains() takes 1 argument")
		}
		return BoolVal(strings.Contains(s, stringify(args[0]))), nil
	case "split":
		sep := ""
		if len(args) == 1 {
			sep = stringify(args[0])
		}
		var parts []string
		if sep == "" {
			for _, c := range s {
				parts = append(parts, string(c))
			}
		} else {
			parts = strings.Split(s, sep)
		}
		out := make(ListVal, len(parts))
		for i, p := range parts {
			out[i] = StrVal(p)
		}
		return out, nil
	case "sub":
		if len(args) != 2 {
			return nil, fmt.Errorf("string.sub() takes 2 arguments")
		}
		re, err := regexp.Compile(stringify(args[0]))
		if err != nil {
			return nil, fmt.Errorf("string.sub(): bad regex: %w", err)
		}
		return StrVal(re.ReplaceAllString(s, stringify(args[1]))), nil
	case "join":
		// receiver is the separator; argument is the list
		if len(args) != 1 {
			return nil, fmt.Errorf("string.join() takes 1 argument")
		}
		items, ok := asList(args[0])
		if !ok {
			return nil, fmt.Errorf("string.join() argument must be a list")
		}
		return StrVal(joinValues(items, s)), nil
	}
	return nil, fmt.Errorf("method not found: string.%s()", name)
}

func listMethod(items []Value, name string, args []Value) (Value, error) {
	switch name {
	case "type":
		return StrVal("list"), nil
	case "length":
		return IntVal(len(items)), nil
	case "contains":
		if len(args) != 1 {
			return nil, fmt.Errorf("list.contains() takes 1 argument")
		}
		target := stringify(args[0])
		for _, e := range items {
			if stringify(e) == target {
				return BoolVal(true), nil
			}
		}
		return BoolVal(false), nil
	case "join":
		if len(args) != 1 {
			return nil, fmt.Errorf("list.join() takes 1 argument")
		}
		return StrVal(joinValues(items, stringify(args[0]))), nil
	}
	return nil, fmt.Errorf("method not found: list.%s()", name)
}

func joinValues(items []Value, sep string) string {
	parts := make([]string, len(items))
	for i, e := range items {
		parts[i] = stringify(e)
	}
	return strings.Join(parts, sep)
}
