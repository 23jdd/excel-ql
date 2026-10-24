package ql

import (
	"cmp"
	"fmt"
)

// Condition is constructed with Col, Number, Predicate, And, Or or Not.
// Its zero value is invalid; Query validates it even on empty input.
type Condition struct {
	refs []string
	eval func(Row) (bool, error)
	err  error
}

// Predicate declares every column accessed by fn so empty queries can validate it.
func Predicate(columns []string, fn func(Row) (bool, error)) Condition {
	c := Condition{refs: append([]string(nil), columns...), eval: fn}
	if fn == nil {
		c.err = fmt.Errorf("nil predicate")
	}
	return c
}

func (c Condition) validate(columns []string) error {
	if c.err != nil {
		return c.err
	}
	if c.eval == nil {
		return fmt.Errorf("empty condition")
	}
	return checkColumns(columns, c.refs)
}

func And(conditions ...Condition) Condition { return combineConditions(false, conditions) }
func Or(conditions ...Condition) Condition  { return combineConditions(true, conditions) }

func combineConditions(or bool, conditions []Condition) Condition {
	conditions = append([]Condition(nil), conditions...)
	c := Condition{}
	if len(conditions) == 0 {
		c.err = fmt.Errorf("And/Or requires at least one condition")
	}
	for _, child := range conditions {
		c.refs = append(c.refs, child.refs...)
		if child.err != nil {
			c.err = child.err
		} else if child.eval == nil {
			c.err = fmt.Errorf("empty condition")
		}
	}
	c.eval = func(row Row) (bool, error) {
		for _, child := range conditions {
			ok, err := child.eval(row.clone())
			if err != nil {
				return false, err
			}
			if ok == or {
				return or, nil
			}
		}
		return !or, nil
	}
	return c
}

func Not(condition Condition) Condition {
	c := Condition{refs: append([]string(nil), condition.refs...), err: condition.err}
	if condition.eval == nil {
		c.err = fmt.Errorf("empty condition")
	}
	c.eval = func(row Row) (bool, error) { ok, err := condition.eval(row.clone()); return !ok, err }
	return c
}

// Column describes a string comparison, or an explicit numeric conversion.
type Column struct {
	name    string
	numeric bool
}

func Col(name string) Column    { return Column{name: name} }
func Number(name string) Column { return Column{name: name, numeric: true} }

func (c Column) Eq(value any) Condition  { return c.compare(value, func(n int) bool { return n == 0 }) }
func (c Column) Ne(value any) Condition  { return c.compare(value, func(n int) bool { return n != 0 }) }
func (c Column) Gt(value any) Condition  { return c.compare(value, func(n int) bool { return n > 0 }) }
func (c Column) Gte(value any) Condition { return c.compare(value, func(n int) bool { return n >= 0 }) }
func (c Column) Lt(value any) Condition  { return c.compare(value, func(n int) bool { return n < 0 }) }
func (c Column) Lte(value any) Condition { return c.compare(value, func(n int) bool { return n <= 0 }) }

func (c Column) IsNull() Condition {
	return Predicate([]string{c.name}, func(row Row) (bool, error) { v, err := row.Get(c.name); return v == nil, err })
}
func (c Column) IsNotNull() Condition { return Not(c.IsNull()) }

func (c Column) compare(value any, accept func(int) bool) Condition {
	condition := Condition{refs: []string{c.name}}
	var right float64
	if c.numeric && value != nil {
		switch v := value.(type) {
		case int:
			value = int64(v)
		case float32:
			value = float64(v)
		}
		var err error
		right, err = number(value)
		if err != nil {
			condition.err = fmt.Errorf("column %q: %w", c.name, err)
		}
	} else if value != nil {
		if _, ok := value.(string); !ok {
			condition.err = fmt.Errorf("column %q: string comparison requires a string literal; use Number or Predicate", c.name)
		}
	}
	condition.eval = func(row Row) (bool, error) {
		left, err := row.Get(c.name)
		if err != nil {
			return false, err
		}
		if left == nil || value == nil {
			return false, nil
		}
		if c.numeric {
			n, err := number(left)
			if err != nil {
				return false, fmt.Errorf("column %q: %w", c.name, err)
			}
			return accept(cmp.Compare(n, right)), nil
		}
		text, ok := left.(string)
		if !ok {
			return false, fmt.Errorf("column %q: %T requires Number or Predicate", c.name, left)
		}
		return accept(cmp.Compare(text, value.(string))), nil
	}
	return condition
}

func filter(t table, conditions []Condition) (table, error) {
	for _, condition := range conditions {
		if err := condition.validate(t.columns); err != nil {
			return table{}, err
		}
	}
	result := t
	result.rows = nil
	for _, rec := range t.rows {
		keep := true
		for _, condition := range conditions {
			ok, err := condition.eval(rec.row.clone())
			if err != nil {
				return table{}, fmt.Errorf("%s: %w", rec.origin, err)
			}
			if !ok {
				keep = false
				break
			}
		}
		if keep {
			result.rows = append(result.rows, rec)
		}
	}
	return result, nil
}
