package ql

import (
	"fmt"
	"slices"
)

// EqualFunc compares whole rows, including values returned by custom mappings.
type EqualFunc func(Row, Row) (bool, error)

// SetFunc implements a collection operator. It must preserve the input schema.
type SetFunc func(left, right []Row) ([]Row, error)

type setSpec struct {
	right  *Stmt
	kind   string
	equal  EqualFunc
	custom SetFunc
}

func (st *Stmt) Union(right *Stmt, equal ...EqualFunc) *Stmt { return st.addSet("union", right, equal) }
func (st *Stmt) Intersect(right *Stmt, equal ...EqualFunc) *Stmt {
	return st.addSet("intersect", right, equal)
}
func (st *Stmt) Except(right *Stmt, equal ...EqualFunc) *Stmt {
	return st.addSet("except", right, equal)
}
func (st *Stmt) addSet(kind string, right *Stmt, equal []EqualFunc) *Stmt {
	if right == nil {
		st.fail(fmt.Errorf("%s requires a right query", kind))
	}
	if len(equal) > 1 {
		st.fail(fmt.Errorf("at most one equality function is allowed"))
	}
	spec := setSpec{right: right, kind: kind}
	if len(equal) > 0 {
		if equal[0] == nil {
			st.fail(fmt.Errorf("nil equality function"))
		}
		spec.equal = equal[0]
	}
	st.sets = append(st.sets, spec)
	return st
}

func (st *Stmt) Combine(right *Stmt, fn SetFunc) *Stmt {
	if right == nil || fn == nil {
		st.fail(fmt.Errorf("Combine requires a right query and a function"))
	}
	st.sets = append(st.sets, setSpec{right: right, kind: "custom", custom: fn})
	return st
}

func combineTables(left table, spec setSpec, active map[*Stmt]bool) (table, error) {
	right, err := spec.right.execute(active)
	if err != nil {
		return table{}, err
	}
	if !slices.Equal(left.columns, right.columns) {
		return table{}, fmt.Errorf("%s: column names and order must match", spec.kind)
	}
	result := table{columns: left.columns, aliases: left.aliases}
	if spec.custom != nil {
		cloneRows := func(t table) []Row {
			rows := make([]Row, len(t.rows))
			for i, rec := range t.rows {
				rows[i] = rec.row.clone()
			}
			return rows
		}
		rows, err := spec.custom(cloneRows(left), cloneRows(right))
		if err != nil {
			return table{}, fmt.Errorf("custom collection operator: %w", err)
		}
		for _, row := range rows {
			if !slices.Equal(row.Columns, left.columns) || len(row.Values) != len(left.columns) {
				return table{}, fmt.Errorf("custom collection operator changed the row schema")
			}
			result.rows = append(result.rows, record{row: row.clone(), origin: "custom collection result"})
		}
		return result, nil
	}
	if spec.equal != nil {
		return combineCustom(left, right, spec)
	}
	leftIDs, err := setIDs(left)
	if err != nil {
		return table{}, err
	}
	rightIDs, err := setIDs(right)
	if err != nil {
		return table{}, err
	}
	rightSet := make(map[string]bool, len(rightIDs))
	for _, id := range rightIDs {
		rightSet[id] = true
	}
	seen := make(map[string]bool)
	for i, rec := range left.rows {
		id := leftIDs[i]
		keep := spec.kind == "union" || (spec.kind == "intersect" && rightSet[id]) || (spec.kind == "except" && !rightSet[id])
		if keep && !seen[id] {
			result.rows = append(result.rows, rec)
			seen[id] = true
		}
	}
	if spec.kind == "union" {
		for i, rec := range right.rows {
			id := rightIDs[i]
			if !seen[id] {
				result.rows = append(result.rows, rec)
				seen[id] = true
			}
		}
	}
	return result, nil
}

func setIDs(t table) ([]string, error) {
	ids := make([]string, len(t.rows))
	for i, rec := range t.rows {
		id, err := tupleKey(rec.row.Values)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rec.origin, err)
		}
		ids[i] = id
	}
	return ids, nil
}

func combineCustom(left, right table, spec setSpec) (table, error) {
	result := table{columns: left.columns, aliases: left.aliases}
	contains := func(records []record, candidate record) (bool, error) {
		for _, rec := range records {
			ok, err := spec.equal(rec.row.clone(), candidate.row.clone())
			if err != nil {
				return false, fmt.Errorf("%s / %s equality: %w", rec.origin, candidate.origin, err)
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	appendUnique := func(rec record) error {
		exists, err := contains(result.rows, rec)
		if err != nil {
			return err
		}
		if !exists {
			result.rows = append(result.rows, rec)
		}
		return nil
	}
	for _, rec := range left.rows {
		match := false
		var err error
		if spec.kind != "union" {
			match, err = contains(right.rows, rec)
			if err != nil {
				return table{}, err
			}
		}
		if spec.kind == "union" || (spec.kind == "intersect" && match) || (spec.kind == "except" && !match) {
			if err := appendUnique(rec); err != nil {
				return table{}, err
			}
		}
	}
	if spec.kind == "union" {
		for _, rec := range right.rows {
			if err := appendUnique(rec); err != nil {
				return table{}, err
			}
		}
	}
	return result, nil
}
