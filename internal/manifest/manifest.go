// Package manifest loads tabular and JSON manifests into rows of cgp variables,
// for the --manifest fan-out (one pipeline run per row).
package manifest

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/compgen-io/cgp/internal/eval"
)

// LoadDelimited reads a TSV (comma='\t') or CSV (comma=',') file. The first
// non-comment row is the header naming the columns; each subsequent row becomes
// one variable set (column name -> value). Lines beginning with '#' are ignored.
func LoadDelimited(path string, comma rune) ([]map[string]eval.Value, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.Comma = comma
	r.Comment = '#'
	r.FieldsPerRecord = -1 // tolerate ragged rows
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := records[0]
	var rows []map[string]eval.Value
	for _, rec := range records[1:] {
		row := make(map[string]eval.Value, len(header))
		for i, col := range header {
			if i < len(rec) {
				row[col] = eval.ParseScalar(rec[i])
			} else {
				row[col] = eval.StrVal("")
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// LoadJSON reads a JSON file containing an array of objects; each object becomes
// one variable set.
func LoadJSON(path string) ([]map[string]eval.Value, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw []map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("manifest %s: %w (expected a JSON array of objects)", path, err)
	}
	rows := make([]map[string]eval.Value, len(raw))
	for i, obj := range raw {
		row := make(map[string]eval.Value, len(obj))
		for k, v := range obj {
			row[k] = jsonValue(v)
		}
		rows[i] = row
	}
	return rows, nil
}

func jsonValue(v any) eval.Value {
	switch x := v.(type) {
	case string:
		return eval.StrVal(x)
	case bool:
		return eval.BoolVal(x)
	case float64:
		if x == math.Trunc(x) {
			return eval.IntVal(int64(x))
		}
		return eval.FloatVal(x)
	case nil:
		return eval.StrVal("")
	default:
		return eval.StrVal(fmt.Sprintf("%v", x))
	}
}
