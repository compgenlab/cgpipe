package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Value is a cgp runtime value.
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
)

func (IntVal) typeName() string   { return "int" }
func (FloatVal) typeName() string { return "float" }
func (StrVal) typeName() string   { return "string" }
func (BoolVal) typeName() string  { return "bool" }
func (ListVal) typeName() string  { return "list" }
func (RangeVal) typeName() string { return "range" }
func (UnsetVal) typeName() string { return "unset" }

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
	case UnsetVal:
		return ""
	}
	return ""
}

// truthy implements cgp's notion of truth (used by if/for-cond and !).
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
		return len(x.slice()) > 0
	case UnsetVal:
		return false
	}
	return true
}

// ParseScalar parses an external scalar string (a command-line value or a
// manifest cell) into a typed value: true/false become bools, integer and float
// literals become numbers, and anything else stays a string.
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

// asList coerces lists and ranges to a slice of values for iteration.
func asList(v Value) ([]Value, bool) {
	switch x := v.(type) {
	case ListVal:
		return []Value(x), true
	case RangeVal:
		return x.slice(), true
	}
	return nil, false
}

// ---- scope ----

// Scope is a flat variable environment.
type Scope struct{ vars map[string]Value }

func newScope() *Scope { return &Scope{vars: map[string]Value{}} }

func (s *Scope) get(name string) (Value, bool) { v, ok := s.vars[name]; return v, ok }
func (s *Scope) set(name string, v Value)      { s.vars[name] = v }
func (s *Scope) has(name string) bool          { _, ok := s.vars[name]; return ok }
func (s *Scope) del(name string)               { delete(s.vars, name) }

func (s *Scope) clone() *Scope {
	c := newScope()
	for k, v := range s.vars {
		c.vars[k] = v
	}
	return c
}

// ---- methods ----

func callMethod(recv Value, name string, args []Value) (Value, error) {
	switch r := recv.(type) {
	case StrVal:
		return stringMethod(string(r), name, args)
	case ListVal:
		return listMethod([]Value(r), name, args)
	case RangeVal:
		return rangeMethod(r, name, args)
	}
	if name == "type" && len(args) == 0 {
		return StrVal(recv.typeName()), nil
	}
	return nil, fmt.Errorf("method not found: %s.%s()", recv.typeName(), name)
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
