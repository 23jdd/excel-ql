package ql

import (
	"cmp"
	"fmt"
	"sort"
)

// Order is one stable sort key. Nulls always sort last.
type Order struct {
	column              string
	descending, numeric bool
	compare             func(any, any) (int, error)
	err                 error
}

func Asc(column string) Order         { return Order{column: column} }
func Desc(column string) Order        { return Order{column: column, descending: true} }
func NumericAsc(column string) Order  { return Order{column: column, numeric: true} }
func NumericDesc(column string) Order { return Order{column: column, numeric: true, descending: true} }

// CustomOrder compares non-null cells. Return a negative, zero or positive int.
func CustomOrder(column string, descending bool, compare func(any, any) (int, error)) Order {
	order := Order{column: column, descending: descending, compare: compare}
	if compare == nil {
		order.err = fmt.Errorf("column %q: nil sort function", column)
	}
	return order
}

func orderRows(t table, orders []Order) (table, error) {
	if len(orders) == 0 {
		return t, nil
	}
	for _, order := range orders {
		if order.err != nil {
			return table{}, order.err
		}
		if err := checkColumns(t.columns, []string{order.column}); err != nil {
			return table{}, err
		}
	}
	type item struct {
		rec  record
		keys []any
	}
	items := make([]item, len(t.rows))
	for i, rec := range t.rows {
		items[i] = item{rec: rec, keys: make([]any, len(orders))}
		for j, order := range orders {
			v, err := rec.row.Get(order.column)
			if err != nil {
				return table{}, err
			}
			if v != nil && order.compare == nil {
				if order.numeric {
					v, err = number(v)
				} else if _, ok := v.(string); !ok {
					err = fmt.Errorf("type %T requires numeric or custom sorting", v)
				}
				if err != nil {
					return table{}, fmt.Errorf("%s column %q: %w", rec.origin, order.column, err)
				}
			}
			items[i].keys[j] = v
		}
	}
	var sortErr error
	sort.SliceStable(items, func(i, j int) bool {
		if sortErr != nil {
			return false
		}
		for k, order := range orders {
			a, b := items[i].keys[k], items[j].keys[k]
			if a == nil && b == nil {
				continue
			}
			if a == nil {
				return false
			}
			if b == nil {
				return true
			}
			var n int
			var err error
			switch {
			case order.compare != nil:
				n, err = order.compare(a, b)
			case order.numeric:
				n = cmp.Compare(a.(float64), b.(float64))
			default:
				n = cmp.Compare(a.(string), b.(string))
			}
			if err != nil {
				sortErr = fmt.Errorf("%s / %s column %q: %w", items[i].rec.origin, items[j].rec.origin, order.column, err)
				return false
			}
			if n != 0 {
				if order.descending {
					return n > 0
				}
				return n < 0
			}
		}
		return false
	})
	if sortErr != nil {
		return table{}, sortErr
	}
	result := t
	result.rows = make([]record, len(items))
	for i, item := range items {
		result.rows[i] = item.rec
	}
	return result, nil
}
