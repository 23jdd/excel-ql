package ql

import (
	"cmp"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestColumnFiltersAndCustomOrder(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {{"id", "private", "name"}, {"01", "secret", "zed"}, {"02", "secret", "Ada"}}})
	filter := func(column string) (bool, error) { return column != "private", nil }
	rows := collect(t, eq.From("Data").SelectFunc(filter).OrderBy(CustomOrder("name", false, func(a, b any) (int, error) {
		return cmp.Compare(strings.ToLower(a.(string)), strings.ToLower(b.(string))), nil
	})))
	wantValues(t, rows, [][]any{{"02", "Ada"}, {"01", "zed"}})
	if !slices.Equal(rows[0].Columns, []string{"id", "name"}) {
		t.Fatal(rows[0].Columns)
	}
	wantError(t, eq.From("Data").SelectFunc(nil), "nil column filter")
	wantError(t, eq.From("Data").SelectFunc(func(string) (bool, error) { return false, nil }), "no output columns")
	wantError(t, eq.From("Data").SelectFunc(func(string) (bool, error) { return false, errors.New("selection failure") }), "selection failure")
	wantError(t, eq.From("Data").OrderBy(CustomOrder("name", false, nil)), "nil sort function")
}

func TestTypedSetEqualityAndCustomSetVariants(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {{"v"}, {"x"}}})
	constant := func(value any) *Stmt {
		return eq.From("Data").SelectFunc(func(string) (bool, error) { return false, nil }).Map(Map("v", nil, func(Row) (any, error) { return value, nil }))
	}
	rows := collect(t, constant("1").Union(constant(int64(1))).Union(constant(float64(1))))
	wantValues(t, rows, [][]any{{"1"}, {int64(1)}, {float64(1)}})
	wantValues(t, collect(t, constant(math.Copysign(0, -1)).Union(constant(float64(0)))), [][]any{{float64(0)}})
	wantError(t, constant(math.NaN()).Union(constant(float64(0))), "non-finite")
	wantError(t, constant(float64(1)).OrderBy(Asc("v")), "numeric or custom")
	eq2 := workbook(t, map[string][][]any{"L": {{"v"}, {"A"}, {"a"}, {"B"}}, "R": {{"v"}, {"a"}}})
	equal := func(a, b Row) (bool, error) {
		return strings.EqualFold(a.Values[0].(string), b.Values[0].(string)), nil
	}
	wantValues(t, collect(t, eq2.From("L").Intersect(eq2.From("R"), equal)), [][]any{{"A"}})
	wantValues(t, collect(t, eq2.From("L").Except(eq2.From("R"), equal)), [][]any{{"B"}})
}

func TestAggregateErrorsAndTupleGroups(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {
		{"a", "b", "n"}, {"x|y", "z", "2"}, {"x", "y|z", "3"}, {"x|y", "z", "bad"},
	}})
	wantValues(t, collect(t, eq.From("Data").GroupBy("a", "b").Aggregate(Count())), [][]any{{"x|y", "z", int64(2)}, {"x", "y|z", int64(1)}})
	wantError(t, eq.From("Data").GroupBy("a").Aggregate(Sum("n")), "row 4")
	cause := errors.New("aggregate failed")
	_, err := eq.From("Data").Aggregate(CustomAggregate("failure", nil, func([]Row) (any, error) { return nil, cause })).Query()
	if !errors.Is(err, cause) {
		t.Fatalf("wrapped error lost: %v", err)
	}
	large := workbook(t, map[string][][]any{"Data": {{"n"}, {"1e308"}, {"1e308"}}})
	wantError(t, large.From("Data").Aggregate(Sum("n")), "overflow")
	wantValues(t, collect(t, large.From("Data").Aggregate(Avg("n"))), [][]any{{float64(1e308)}})
	wantValues(t, collect(t, eq.From("Data").Where(Col("n").Lt("3"))), [][]any{{"x|y", "z", "2"}})
	wantValues(t, collect(t, eq.From("Data").Where(Col("n").Lte("3"))), [][]any{{"x|y", "z", "2"}, {"x", "y|z", "3"}})
}

func TestChainedJoinsAndRightAggregates(t *testing.T) {
	eq := workbook(t, map[string][][]any{
		"A": {{"id"}, {"1"}, {"2"}},
		"B": {{"id", "n"}, {"1", "2"}, {"1", "3"}},
		"C": {{"id", "tag"}, {"1", "first"}},
	})
	grouped := eq.From("B").As("b").GroupBy("b.id").Aggregate(Sum("b.n"))
	st := eq.From("A").As("a").LeftJoin(grouped, On("id", "b.id")).
		LeftJoin(eq.From("C").As("c"), On("a.id", "c.id")).Select([]string{"a.id", "b.sum(b.n)", "c.tag"})
	wantValues(t, collect(t, st), [][]any{{"1", float64(5), "first"}, {"2", nil, nil}})
	wantValues(t, collect(t, st), [][]any{{"1", float64(5), "first"}, {"2", nil, nil}})
}

func TestCallbackIsolationAndSequenceSnapshot(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {{"id"}, {"a"}, {"b"}}})
	st := eq.From("Data").Where(Predicate([]string{"id"}, func(row Row) (bool, error) {
		row.Columns[0], row.Values[0] = "changed", "changed"
		return true, nil
	}))
	seq, err := st.Query()
	if err != nil {
		t.Fatal(err)
	}
	st.Limit(1)
	wantValues(t, slices.Collect(seq), [][]any{{"a"}, {"b"}})
	wantValues(t, collect(t, st), [][]any{{"a"}})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			rows := slices.Collect(seq)
			if len(rows) != 2 {
				t.Error("sequence changed")
			}
			rows[0].Values[0] = "mutated"
		})
	}
	wg.Wait()
	wantValues(t, slices.Collect(seq), [][]any{{"a"}, {"b"}})
}

func TestOpenAndZeroValues(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "absent.xlsx")); err == nil {
		t.Fatal("missing file accepted")
	}
	path := filepath.Join(t.TempDir(), "invalid.xlsx")
	if err := os.WriteFile(path, []byte("invalid workbook"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("invalid file accepted")
	}
	var eq *ExcelQuery
	if eq.Sheets() != nil || eq.Close() != nil {
		t.Fatal("nil workbook lifecycle")
	}
	wantError(t, eq.From("Data"), "nil workbook")
	zero := &ExcelQuery{}
	if zero.Sheets() != nil || zero.Close() != nil {
		t.Fatal("zero workbook lifecycle")
	}
	wantError(t, zero.From("Data"), "closed")
	var st *Stmt
	wantError(t, st, "nil query")
}
