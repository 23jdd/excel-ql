package ql

import (
	"fmt"
	"iter"
	"strings"
)

// Stmt is a mutable query builder. Do not mutate one builder concurrently.
// Builder methods accumulate errors; Query returns them before producing rows.
type Stmt struct {
	eq              *ExcelQuery
	sheet, alias    string
	selects         []string
	selectFilters   []func(string) (bool, error)
	wheres, havings []Condition
	groups          []string
	aggregates      []Aggregation
	mappings        []Mapping
	orders          []Order
	joins           []joinSpec
	sets            []setSpec
	offset          int
	limit           *int
	err             error
}

func (st *Stmt) fail(err error) {
	if st.err == nil {
		st.err = err
	}
}

func (st *Stmt) As(alias string) *Stmt {
	if strings.TrimSpace(alias) == "" || strings.Contains(alias, ".") {
		st.fail(fmt.Errorf("alias must be nonempty and contain no dot"))
	}
	st.alias = alias
	return st
}

func (st *Stmt) Select(columns []string) *Stmt {
	if len(columns) == 0 {
		st.fail(fmt.Errorf("Select requires at least one column"))
	}
	st.selects = append(st.selects, columns...)
	return st
}

// SelectFunc filters selected column names, or all columns if Select is omitted.
// Multiple filters are combined with AND and evaluated in declaration order.
func (st *Stmt) SelectFunc(fn func(string) (bool, error)) *Stmt {
	if fn == nil {
		st.fail(fmt.Errorf("nil column filter"))
	}
	st.selectFilters = append(st.selectFilters, fn)
	return st
}

func (st *Stmt) Where(conditions ...Condition) *Stmt {
	if len(conditions) == 0 {
		st.fail(fmt.Errorf("Where requires at least one condition"))
	}
	st.wheres = append(st.wheres, conditions...)
	return st
}

func (st *Stmt) Having(conditions ...Condition) *Stmt {
	if len(conditions) == 0 {
		st.fail(fmt.Errorf("Having requires at least one condition"))
	}
	st.havings = append(st.havings, conditions...)
	return st
}

func (st *Stmt) GroupBy(columns ...string) *Stmt {
	if len(columns) == 0 {
		st.fail(fmt.Errorf("GroupBy requires at least one column"))
	}
	st.groups = append(st.groups, columns...)
	return st
}

func (st *Stmt) Aggregate(aggregates ...Aggregation) *Stmt {
	if len(aggregates) == 0 {
		st.fail(fmt.Errorf("Aggregate requires at least one function"))
	}
	st.aggregates = append(st.aggregates, aggregates...)
	return st
}

// Mapping adds a computed output column. Dependencies refer to the input of Select.
type Mapping struct {
	name string
	refs []string
	fn   func(Row) (any, error)
}

func Map(name string, columns []string, fn func(Row) (any, error)) Mapping {
	return Mapping{name: name, refs: append([]string(nil), columns...), fn: fn}
}

// Map appends computed columns after the selected columns.
func (st *Stmt) Map(mappings ...Mapping) *Stmt {
	if len(mappings) == 0 {
		st.fail(fmt.Errorf("Map requires at least one mapping"))
	}
	st.mappings = append(st.mappings, mappings...)
	return st
}

func (st *Stmt) OrderBy(orders ...Order) *Stmt {
	if len(orders) == 0 {
		st.fail(fmt.Errorf("OrderBy requires at least one key"))
	}
	st.orders = append(st.orders, orders...)
	return st
}

// Offset skips n rows after sorting and before Limit. The last call wins.
// Negative offsets are reported by Query, like other builder errors.
func (st *Stmt) Offset(n int) *Stmt {
	if n < 0 {
		st.fail(fmt.Errorf("Offset must not be negative"))
	}
	st.offset = n
	return st
}

func (st *Stmt) Limit(n int) *Stmt {
	if n < 0 {
		st.fail(fmt.Errorf("Limit must not be negative"))
	}
	st.limit = &n
	return st
}

func (st *Stmt) Query() (iter.Seq[Row], error) {
	t, err := st.execute(make(map[*Stmt]bool))
	if err != nil {
		return nil, err
	}
	return func(yield func(Row) bool) {
		for _, rec := range t.rows {
			if !yield(rec.row.clone()) {
				return
			}
		}
	}, nil
}

func (st *Stmt) execute(active map[*Stmt]bool) (result table, err error) {
	if st == nil {
		return table{}, fmt.Errorf("nil query")
	}
	defer func() {
		if err != nil {
			err = fmt.Errorf("sheet %q: %w", st.sheet, err)
		}
	}()
	if active[st] {
		return table{}, fmt.Errorf("cyclic query reference")
	}
	active[st] = true
	defer delete(active, st)
	if st.err != nil {
		return table{}, st.err
	}
	t, err := st.eq.read(st.sheet, st.alias)
	if err != nil {
		return table{}, err
	}
	for _, spec := range st.joins {
		t, err = st.join(t, spec, active)
		if err != nil {
			return table{}, err
		}
	}
	t, err = filter(t, st.wheres)
	if err != nil {
		return table{}, err
	}
	if len(st.groups) > 0 || len(st.aggregates) > 0 {
		t, err = aggregate(t, st.groups, st.aggregates)
		if err != nil {
			return table{}, err
		}
	} else if len(st.havings) > 0 {
		return table{}, fmt.Errorf("Having requires grouping or aggregation")
	}
	t, err = filter(t, st.havings)
	if err != nil {
		return table{}, err
	}
	t, err = st.project(t)
	if err != nil {
		return table{}, err
	}
	t, err = orderRows(t, st.orders)
	if err != nil {
		return table{}, err
	}
	t.rows = t.rows[min(st.offset, len(t.rows)):]
	if st.limit != nil && *st.limit < len(t.rows) {
		t.rows = t.rows[:*st.limit]
	}
	for _, spec := range st.sets {
		t, err = combineTables(t, spec, active)
		if err != nil {
			return table{}, err
		}
	}
	return t, nil
}

func (st *Stmt) project(t table) (table, error) {
	selected := st.selects
	if len(selected) == 0 {
		selected = t.columns
	}
	if err := checkColumns(t.columns, selected); err != nil {
		return table{}, err
	}
	if err := uniqueNames(selected); err != nil {
		return table{}, err
	}
	if len(st.selectFilters) > 0 {
		filtered := make([]string, 0, len(selected))
		for _, column := range selected {
			keep := true
			for _, fn := range st.selectFilters {
				ok, err := fn(column)
				if err != nil {
					return table{}, fmt.Errorf("column %q selection: %w", column, err)
				}
				if !ok {
					keep = false
					break
				}
			}
			if keep {
				filtered = append(filtered, column)
			}
		}
		selected = filtered
		if len(selected) == 0 && len(st.mappings) == 0 {
			return table{}, fmt.Errorf("column filters selected no output columns")
		}
	}
	columns := append([]string(nil), selected...)
	for _, mapping := range st.mappings {
		if mapping.fn == nil {
			return table{}, fmt.Errorf("column %q: nil mapping", mapping.name)
		}
		if err := checkColumns(t.columns, mapping.refs); err != nil {
			return table{}, err
		}
		columns = append(columns, mapping.name)
	}
	if err := uniqueNames(columns); err != nil {
		return table{}, err
	}
	result := table{columns: columns, aliases: t.aliases}
	for _, rec := range t.rows {
		values, err := rowValues(rec.row, selected)
		if err != nil {
			return table{}, err
		}
		for _, mapping := range st.mappings {
			value, err := mapping.fn(rec.row.clone())
			if err != nil {
				return table{}, fmt.Errorf("%s column %q: %w", rec.origin, mapping.name, err)
			}
			values = append(values, value)
		}
		result.rows = append(result.rows, record{row: Row{Columns: columns, Values: values}, origin: rec.origin})
	}
	return result, nil
}
