package ql

import (
	"fmt"
	"strings"
)

// JoinKey pairs a left column with a right column. Bare names use source aliases.
type JoinKey struct{ Left, Right string }

func On(left, right string) JoinKey { return JoinKey{Left: left, Right: right} }

type joinSpec struct {
	right     *Stmt
	keys      []JoinKey
	keepRight bool
}

func (st *Stmt) LeftJoin(right *Stmt, keys ...JoinKey) *Stmt {
	return st.addJoin(right, false, keys)
}
func (st *Stmt) RightJoin(right *Stmt, keys ...JoinKey) *Stmt {
	return st.addJoin(right, true, keys)
}
func (st *Stmt) addJoin(right *Stmt, keepRight bool, keys []JoinKey) *Stmt {
	if right == nil || len(keys) == 0 {
		st.fail(fmt.Errorf("join requires a source and at least one key"))
	}
	st.joins = append(st.joins, joinSpec{right: right, keys: append([]JoinKey(nil), keys...), keepRight: keepRight})
	return st
}

func resolveKey(columns []string, alias, name string) string {
	for _, column := range columns {
		if column == name {
			return name
		}
	}
	return alias + "." + name
}

func (st *Stmt) join(left table, spec joinSpec, active map[*Stmt]bool) (table, error) {
	if st.alias == "" || spec.right.alias == "" {
		return table{}, fmt.Errorf("both join sources require As aliases")
	}
	right, err := spec.right.execute(active)
	if err != nil {
		return table{}, err
	}
	// Aggregates and computed columns get the same namespace as the right source.
	for i, column := range right.columns {
		qualified := false
		for _, alias := range right.aliases {
			if strings.HasPrefix(column, alias+".") {
				qualified = true
				break
			}
		}
		if !qualified {
			right.columns[i] = spec.right.alias + "." + column
			for j := range right.rows {
				right.rows[j].row.Columns[i] = right.columns[i]
			}
		}
	}
	for _, a := range left.aliases {
		for _, b := range right.aliases {
			if a == b {
				return table{}, fmt.Errorf("duplicate source alias %q", a)
			}
		}
	}
	columns := append(append([]string(nil), left.columns...), right.columns...)
	if err := uniqueNames(columns); err != nil {
		return table{}, err
	}
	leftKeys, rightKeys := make([]string, len(spec.keys)), make([]string, len(spec.keys))
	for i, key := range spec.keys {
		leftKeys[i] = resolveKey(left.columns, st.alias, key.Left)
		rightKeys[i] = resolveKey(right.columns, spec.right.alias, key.Right)
	}
	if err := checkColumns(left.columns, leftKeys); err != nil {
		return table{}, err
	}
	if err := checkColumns(right.columns, rightKeys); err != nil {
		return table{}, err
	}
	leftIDs, err := joinIDs(left, leftKeys)
	if err != nil {
		return table{}, err
	}
	rightIDs, err := joinIDs(right, rightKeys)
	if err != nil {
		return table{}, err
	}
	outer, inner := left, right
	outerIDs, innerIDs := leftIDs, rightIDs
	if spec.keepRight {
		outer, inner = right, left
		outerIDs, innerIDs = rightIDs, leftIDs
	}
	index := make(map[string][]int)
	for i, id := range innerIDs {
		if id != "" {
			index[id] = append(index[id], i)
		}
	}
	result := table{columns: columns, aliases: append(append([]string(nil), left.aliases...), right.aliases...)}
	for i, rec := range outer.rows {
		matches := index[outerIDs[i]]
		if len(matches) == 0 {
			matches = []int{-1}
		}
		for _, j := range matches {
			lv, rv := make([]any, len(left.columns)), make([]any, len(right.columns))
			origin := rec.origin
			if spec.keepRight {
				copy(rv, rec.row.Values)
			} else {
				copy(lv, rec.row.Values)
			}
			if j >= 0 {
				if spec.keepRight {
					copy(lv, inner.rows[j].row.Values)
				} else {
					copy(rv, inner.rows[j].row.Values)
				}
				origin += " / " + inner.rows[j].origin
			}
			result.rows = append(result.rows, record{row: Row{Columns: columns, Values: append(lv, rv...)}, origin: origin})
		}
	}
	return result, nil
}

func joinIDs(t table, columns []string) ([]string, error) {
	ids := make([]string, len(t.rows))
	for i, rec := range t.rows {
		values, err := rowValues(rec.row, columns)
		if err != nil {
			return nil, err
		}
		hasNull := false
		for _, value := range values {
			if value == nil {
				hasNull = true
				break
			}
		}
		if hasNull {
			continue
		}
		ids[i], err = tupleKey(values)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rec.origin, err)
		}
	}
	return ids, nil
}
