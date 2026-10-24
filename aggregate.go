package ql

import (
	"errors"
	"fmt"
	"math"
)

type aggregateInputError struct {
	index int
	err   error
}

func (e *aggregateInputError) Error() string { return e.err.Error() }
func (e *aggregateInputError) Unwrap() error { return e.err }

// Aggregation computes one named value for a group of rows.
type Aggregation struct {
	name string
	refs []string
	fn   func([]Row) (any, error)
	err  error
}

// CustomAggregate declares columns used by fn. fn also receives empty groups
// when an ungrouped aggregate has no input rows.
func CustomAggregate(name string, columns []string, fn func([]Row) (any, error)) Aggregation {
	return Aggregation{name: name, refs: append([]string(nil), columns...), fn: fn}
}

func (a Aggregation) As(name string) Aggregation { a.name = name; return a }

// Count counts all rows, or non-null cells in one column.
func Count(columns ...string) Aggregation {
	columns = append([]string(nil), columns...)
	a := Aggregation{name: "count(*)", refs: append([]string(nil), columns...)}
	if len(columns) > 1 {
		a.err = fmt.Errorf("Count accepts at most one column")
	}
	if len(columns) == 1 {
		a.name = "count(" + columns[0] + ")"
	}
	a.fn = func(rows []Row) (any, error) {
		var count int64
		for _, row := range rows {
			if len(columns) == 0 {
				count++
				continue
			}
			v, err := row.Get(columns[0])
			if err != nil {
				return nil, err
			}
			if v != nil {
				count++
			}
		}
		return count, nil
	}
	return a
}

func Sum(column string) Aggregation { return numericAggregate("sum", column) }
func Avg(column string) Aggregation { return numericAggregate("avg", column) }
func Max(column string) Aggregation { return numericAggregate("max", column) }
func Min(column string) Aggregation { return numericAggregate("min", column) }

func numericAggregate(kind, column string) Aggregation {
	return CustomAggregate(kind+"("+column+")", []string{column}, func(rows []Row) (any, error) {
		var value float64
		var count int64
		for i, row := range rows {
			v, err := row.Get(column)
			if err != nil {
				return nil, err
			}
			if v == nil {
				continue
			}
			n, err := number(v)
			if err != nil {
				return nil, &aggregateInputError{index: i, err: fmt.Errorf("column %q: %w", column, err)}
			}
			switch kind {
			case "sum":
				value += n
			case "avg":
				// Keep the mean bounded even when summing finite inputs would overflow.
				value = value*(float64(count)/float64(count+1)) + n/float64(count+1)
			case "min":
				if count == 0 || n < value {
					value = n
				}
			case "max":
				if count == 0 || n > value {
					value = n
				}
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, &aggregateInputError{index: i, err: fmt.Errorf("column %q: numeric aggregate overflow", column)}
			}
			count++
		}
		if count == 0 {
			return nil, nil
		}
		return value, nil
	})
}

func aggregate(t table, keys []string, functions []Aggregation) (table, error) {
	if err := checkColumns(t.columns, keys); err != nil {
		return table{}, err
	}
	columns := append([]string(nil), keys...)
	for _, fn := range functions {
		if fn.err != nil {
			return table{}, fn.err
		}
		if fn.fn == nil {
			return table{}, fmt.Errorf("aggregate %q: nil function", fn.name)
		}
		if err := checkColumns(t.columns, fn.refs); err != nil {
			return table{}, err
		}
		columns = append(columns, fn.name)
	}
	if err := uniqueNames(columns); err != nil {
		return table{}, err
	}
	type group struct {
		values  []any
		records []record
	}
	groups := make([]group, 0)
	index := make(map[string]int)
	if len(keys) == 0 {
		groups = append(groups, group{records: t.rows})
	}
	for _, rec := range t.rows {
		if len(keys) == 0 {
			break
		}
		values, err := rowValues(rec.row, keys)
		if err != nil {
			return table{}, err
		}
		key, err := tupleKey(values)
		if err != nil {
			return table{}, fmt.Errorf("%s: %w", rec.origin, err)
		}
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, group{values: values})
		}
		groups[i].records = append(groups[i].records, rec)
	}
	result := table{columns: columns, aliases: t.aliases}
	for _, g := range groups {
		values := append([]any(nil), g.values...)
		origin := "empty aggregate input"
		if len(g.records) > 0 {
			origin = g.records[0].origin
		}
		for _, fn := range functions {
			rows := make([]Row, len(g.records))
			for i, rec := range g.records {
				rows[i] = rec.row.clone()
			}
			value, err := fn.fn(rows)
			if err != nil {
				var inputErr *aggregateInputError
				if errors.As(err, &inputErr) && inputErr.index >= 0 && inputErr.index < len(g.records) {
					return table{}, fmt.Errorf("aggregate %q %s: %w", fn.name, g.records[inputErr.index].origin, err)
				}
				return table{}, fmt.Errorf("aggregate %q group starting at %s: %w", fn.name, origin, err)
			}
			values = append(values, value)
		}
		result.rows = append(result.rows, record{row: Row{Columns: columns, Values: values}, origin: origin})
	}
	return result, nil
}
