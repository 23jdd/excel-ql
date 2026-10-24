package ql

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Row keeps values in the same order as Columns. Missing cells contain nil.
type Row struct {
	Columns []string
	Values  []any
}

func (r Row) Get(column string) (any, error) {
	for i, name := range r.Columns {
		if name == column {
			if i >= len(r.Values) {
				return nil, fmt.Errorf("column %q: malformed row", column)
			}
			return r.Values[i], nil
		}
	}
	return nil, fmt.Errorf("unknown column %q", column)
}

func (r Row) clone() Row {
	return Row{Columns: append([]string(nil), r.Columns...), Values: append([]any(nil), r.Values...)}
}

type record struct {
	row    Row
	origin string
}
type table struct {
	columns []string
	rows    []record
	aliases []string
}

func checkColumns(columns, refs []string) error {
	for _, ref := range refs {
		found := false
		for _, column := range columns {
			if ref == column {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown column %q", ref)
		}
	}
	return nil
}

func uniqueNames(names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("empty column name")
		}
		if seen[name] {
			return fmt.Errorf("duplicate column %q", name)
		}
		seen[name] = true
	}
	return nil
}

func number(value any) (float64, error) {
	var n float64
	var err error
	switch v := value.(type) {
	case string:
		n, err = strconv.ParseFloat(strings.TrimSpace(v), 64)
	case float64:
		n = v
	case int64:
		n = float64(v)
	default:
		return 0, fmt.Errorf("cannot convert %T to number", value)
	}
	if err != nil {
		return 0, fmt.Errorf("invalid number %q: %w", value, err)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("non-finite number %v", value)
	}
	return n, nil
}

// tupleKey encodes types and boundaries explicitly, without delimiter collisions.
func tupleKey(values []any) (string, error) {
	var b strings.Builder
	for _, value := range values {
		switch v := value.(type) {
		case nil:
			b.WriteByte('n')
		case string:
			b.WriteByte('s')
			b.WriteString(strconv.Itoa(len(v)))
			b.WriteByte(':')
			b.WriteString(v)
		case int64:
			b.WriteByte('i')
			b.WriteString(strconv.FormatInt(v, 10))
			b.WriteByte(';')
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return "", fmt.Errorf("non-finite comparison value %v", v)
			}
			if v == 0 {
				v = 0
			} // Normalize signed zero.
			var bits [8]byte
			binary.BigEndian.PutUint64(bits[:], math.Float64bits(v))
			b.WriteByte('f')
			b.Write(bits[:])
		default:
			return "", fmt.Errorf("type %T requires a custom equality function", value)
		}
	}
	return b.String(), nil
}

func rowValues(row Row, columns []string) ([]any, error) {
	values := make([]any, len(columns))
	for i, column := range columns {
		value, err := row.Get(column)
		if err != nil {
			return nil, err
		}
		values[i] = value
	}
	return values, nil
}
