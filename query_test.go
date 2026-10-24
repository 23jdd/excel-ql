package ql

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func workbook(t *testing.T, sheets map[string][][]any, edit ...func(*excelize.File)) *ExcelQuery {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	names := make([]string, 0, len(sheets))
	for name := range sheets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name != "Sheet1" {
			if _, err := f.NewSheet(name); err != nil {
				t.Fatal(err)
			}
		}
		for i, row := range sheets[name] {
			cell := fmt.Sprintf("A%d", i+1)
			if err := f.SetSheetRow(name, cell, &row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, keep := sheets["Sheet1"]; !keep {
		if err := f.DeleteSheet("Sheet1"); err != nil {
			t.Fatal(err)
		}
	}
	for _, fn := range edit {
		fn(f)
	}
	path := filepath.Join(t.TempDir(), "fixture.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	eq, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := eq.Close(); err != nil {
			t.Error(err)
		}
	})
	return eq
}

func collect(t *testing.T, st *Stmt) []Row {
	t.Helper()
	seq, err := st.Query()
	if err != nil {
		t.Fatal(err)
	}
	return slices.Collect(seq)
}

func wantValues(t *testing.T, rows []Row, want [][]any) {
	t.Helper()
	got := make([][]any, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.Values)
	}
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("values\n got: %#v\nwant: %#v", got, want)
	}
}

func wantError(t *testing.T, st *Stmt, fragment string) {
	t.Helper()
	seq, err := st.Query()
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("want error containing %q, got %v", fragment, err)
	}
	if seq != nil {
		t.Fatal("failed query exposed partial results")
	}
}

func TestQueryProjectionConditionsAndLifetime(t *testing.T) {
	eq := workbook(t, map[string][][]any{"People": {
		{" id ", "team", "age", "note"},
		{"001", "a", "9", " keep "},
		{"002", "b", "20", nil},
		{},
		{"003", "a", nil, " "},
		{"004", "a", "20", nil},
	}})
	if !slices.Equal(eq.Sheets(), []string{"People"}) {
		t.Fatal(eq.Sheets())
	}
	st := eq.From("People").Select([]string{"id", "age"}).
		Where(Not(Or(And(Col("team").Eq("b"), Number("age").Gt(10)), Col("id").Eq("003")))).
		Where(Number("age").Gte(9)).OrderBy(NumericDesc("age")).Limit(9).Limit(2)
	seq, err := st.Query()
	if err != nil {
		t.Fatal(err)
	}
	if err := eq.Close(); err != nil {
		t.Fatal(err)
	}
	first := slices.Collect(seq)
	wantValues(t, first, [][]any{{"004", "20"}, {"001", "9"}})
	first[0].Columns[0], first[0].Values[0] = "changed", "changed"
	wantValues(t, slices.Collect(seq), [][]any{{"004", "20"}, {"001", "9"}})
	if slices.Collect(seq)[0].Columns[0] != "id" {
		t.Fatal("column mutation escaped iteration")
	}
	for range seq {
		break
	}
	if err := eq.Close(); err != nil {
		t.Fatal(err)
	}
	wantError(t, eq.From("People"), "closed")
}

func TestNullWhitespaceAndMerge(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {
		{"id", "value"}, {"01", "x"}, {"02", nil}, {}, {"03", " "}, {"04", ""},
	}}, func(f *excelize.File) {
		if err := f.MergeCell("Data", "B2", "B3"); err != nil {
			t.Fatal(err)
		}
	})
	wantValues(t, collect(t, eq.From("Data").Where(Col("value").IsNull())), [][]any{{"02", nil}, {"04", nil}})
	wantValues(t, collect(t, eq.From("Data").Where(Not(Col("value").Eq("x")))), [][]any{{"02", nil}, {"03", " "}, {"04", nil}})
	wantValues(t, collect(t, eq.From("Data").Where(Col("value").Ne("x"))), [][]any{{"03", " "}})
	wantValues(t, collect(t, eq.From("Data").OrderBy(Desc("value"))), [][]any{{"01", "x"}, {"03", " "}, {"02", nil}, {"04", nil}})
}

func TestDeferredValidationOnEmptyInput(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Empty": {{"id", "n"}}})
	cases := []struct {
		name    string
		build   func() *Stmt
		message string
	}{
		{"where", func() *Stmt { return eq.From("Empty").Where() }, "Where requires"},
		{"and", func() *Stmt { return eq.From("Empty").Where(And()) }, "requires at least"},
		{"or", func() *Stmt { return eq.From("Empty").Where(Or()) }, "requires at least"},
		{"not", func() *Stmt { return eq.From("Empty").Where(Not(Condition{})) }, "empty condition"},
		{"unknown nested", func() *Stmt { return eq.From("Empty").Where(Or(Col("id").Eq("x"), Col("missing").Eq("x"))) }, "missing"},
		{"select", func() *Stmt { return eq.From("Empty").Select(nil) }, "Select requires"},
		{"duplicate", func() *Stmt { return eq.From("Empty").Select([]string{"id", "id"}) }, "duplicate"},
		{"unknown select", func() *Stmt { return eq.From("Empty").Select([]string{"missing"}) }, "missing"},
		{"order", func() *Stmt { return eq.From("Empty").OrderBy(Asc("missing")) }, "missing"},
		{"negative limit", func() *Stmt { return eq.From("Empty").Limit(-1) }, "negative"},
		{"group", func() *Stmt { return eq.From("Empty").GroupBy("missing") }, "missing"},
		{"aggregate", func() *Stmt { return eq.From("Empty").Aggregate(Sum("missing")) }, "missing"},
		{"having", func() *Stmt { return eq.From("Empty").Having(Col("id").Eq("x")) }, "Having requires"},
		{"bad number", func() *Stmt { return eq.From("Empty").Where(Number("n").Eq(math.NaN())) }, "non-finite"},
		{"nil predicate", func() *Stmt { return eq.From("Empty").Where(Predicate(nil, nil)) }, "nil predicate"},
		{"nil map", func() *Stmt { return eq.From("Empty").Map(Map("x", nil, nil)) }, "nil mapping"},
		{"map dependency", func() *Stmt {
			return eq.From("Empty").Map(Map("x", []string{"missing"}, func(Row) (any, error) { return nil, nil }))
		}, "missing"},
		{"nil aggregate", func() *Stmt { return eq.From("Empty").Aggregate(CustomAggregate("x", nil, nil)) }, "nil function"},
		{"count args", func() *Stmt { return eq.From("Empty").Aggregate(Count("id", "n")) }, "at most one"},
		{"nil join", func() *Stmt { return eq.From("Empty").As("a").LeftJoin(nil, On("id", "id")) }, "join requires"},
		{"cycle", func() *Stmt { s := eq.From("Empty"); return s.Union(s) }, "cyclic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { wantError(t, tc.build(), tc.message) })
	}
	wantValues(t, collect(t, eq.From("Empty")), nil)
	wantValues(t, collect(t, eq.From("Empty").Limit(0)), nil)
}

func TestHeaderAndSheetValidation(t *testing.T) {
	cases := []struct {
		name    string
		rows    [][]any
		message string
	}{
		{"empty", nil, "empty worksheet"},
		{"duplicate", [][]any{{"id", " id "}}, "duplicate"},
		{"unnamed headers", [][]any{{nil, " \t"}, {"x", "extra"}}, "missing header"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eq := workbook(t, map[string][][]any{"Data": tc.rows})
			wantError(t, eq.From("Data"), tc.message)
		})
	}
	eq := workbook(t, map[string][][]any{"Data": {{"id"}, {"x"}}}, func(f *excelize.File) {
		style, err := f.NewStyle(&excelize.Style{NumFmt: 2})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStyle("Data", "Z100", "Z100", style); err != nil {
			t.Fatal(err)
		}
	})
	wantValues(t, collect(t, eq.From("Data")), [][]any{{"x"}})
	wantError(t, eq.From("data"), "does not exist")
}

func TestConditionsShortCircuitAndErrors(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {{"id", "n"}, {"x", "bad"}}})
	called := 0
	bad := Predicate([]string{"n"}, func(Row) (bool, error) { called++; return false, errors.New("callback failure") })
	wantValues(t, collect(t, eq.From("Data").Where(Or(Col("id").Eq("x"), bad))), [][]any{{"x", "bad"}})
	wantValues(t, collect(t, eq.From("Data").Where(And(Col("id").Eq("other"), bad))), nil)
	if called != 0 {
		t.Fatal("short-circuit evaluated a skipped condition")
	}
	wantError(t, eq.From("Data").Where(bad), "callback failure")
	wantError(t, eq.From("Data").Where(Number("n").Gt(0)), "row 2")
	wantError(t, eq.From("Data").OrderBy(NumericAsc("n")), "column \"n\"")
	wantError(t, eq.From("Data").Where(Or(Col("id").Eq("x"), Col("missing").Eq("x"))), "missing")
}

func TestAggregateGroupingHavingAndEmpty(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Sales": {
		{"team", "n"}, {"b", "10"}, {"a", "2"}, {"b", "20"}, {"a", nil}, {nil, "4"},
	}})
	aggs := []Aggregation{Count().As("rows"), Count("n").As("cells"), Sum("n").As("sum"), Avg("n").As("avg"), Min("n").As("min"), Max("n").As("max")}
	wantValues(t, collect(t, eq.From("Sales").GroupBy("team").Aggregate(aggs...)), [][]any{
		{"b", int64(2), int64(2), float64(30), float64(15), float64(10), float64(20)},
		{"a", int64(2), int64(1), float64(2), float64(2), float64(2), float64(2)},
		{nil, int64(1), int64(1), float64(4), float64(4), float64(4), float64(4)},
	})
	wantValues(t, collect(t, eq.From("Sales").GroupBy("team").Aggregate(Sum("n").As("total")).
		Having(Number("total").Gt(3)).Select([]string{"total", "team"}).OrderBy(NumericDesc("total"))),
		[][]any{{float64(30), "b"}, {float64(4), nil}})
	wantValues(t, collect(t, eq.From("Sales").Where(Col("team").Eq("none")).Aggregate(aggs...)),
		[][]any{{int64(0), int64(0), nil, nil, nil, nil}})
	wantValues(t, collect(t, eq.From("Sales").Where(Col("team").Eq("none")).GroupBy("team").Aggregate(Count())), nil)
	wantValues(t, collect(t, eq.From("Sales").GroupBy("team")), [][]any{{"b"}, {"a"}, {nil}})
	wantError(t, eq.From("Sales").GroupBy("team").Aggregate(Sum("n")).Select([]string{"n"}), "unknown column")
	wantError(t, eq.From("Sales").GroupBy("team").Aggregate(Count()).Having(Col("n").Eq("1")), "unknown column")
}

func TestStableOrderingMappingAndNumericFailures(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {
		{"id", "n"}, {"a", "2"}, {"b", "10"}, {"c", "2"}, {"d", nil},
	}})
	wantValues(t, collect(t, eq.From("Data").OrderBy(NumericAsc("n"))), [][]any{{"a", "2"}, {"c", "2"}, {"b", "10"}, {"d", nil}})
	wantValues(t, collect(t, eq.From("Data").OrderBy(NumericAsc("n"), Desc("id"))), [][]any{{"c", "2"}, {"a", "2"}, {"b", "10"}, {"d", nil}})
	mapN := Map("twice", []string{"n"}, func(r Row) (any, error) {
		v, err := r.Get("n")
		if err != nil || v == nil {
			return nil, err
		}
		n, err := number(v)
		return n * 2, err
	})
	wantValues(t, collect(t, eq.From("Data").Select([]string{"id"}).Map(mapN).OrderBy(NumericDesc("twice")).Limit(2)), [][]any{{"b", float64(20)}, {"a", float64(4)}})
	wantError(t, eq.From("Data").Map(Map("failure", nil, func(Row) (any, error) { return nil, errors.New("mapping failed") })), "mapping failed")
	wantError(t, eq.From("Data").OrderBy(CustomOrder("id", false, func(any, any) (int, error) { return 0, errors.New("sort failed") })), "sort failed")
	for _, n := range []string{"invalid", "NaN", "+Inf", "1e999"} {
		t.Run(n, func(t *testing.T) {
			bad := workbook(t, map[string][][]any{"Data": {{"n"}, {n}}})
			wantError(t, bad.From("Data").Aggregate(Sum("n")), "column \"n\"")
			wantError(t, bad.From("Data").OrderBy(NumericAsc("n")), "column \"n\"")
		})
	}
}

func TestJoinsMultiplicityNullsAndOrder(t *testing.T) {
	eq := workbook(t, map[string][][]any{
		"L": {{"id", "name"}, {"1", "a"}, {"1", "b"}, {"2", "c"}, {nil, "null-left"}},
		"R": {{"id", "value"}, {"1", "x"}, {"1", "y"}, {"1", "z"}, {"3", "w"}, {nil, "null-right"}},
	})
	left := func() *Stmt { return eq.From("L").As("l") }
	right := func() *Stmt { return eq.From("R").As("r") }
	wantValues(t, collect(t, left().LeftJoin(right(), On("id", "id")).Select([]string{"l.name", "r.value"})), [][]any{
		{"a", "x"}, {"a", "y"}, {"a", "z"}, {"b", "x"}, {"b", "y"}, {"b", "z"}, {"c", nil}, {"null-left", nil},
	})
	wantValues(t, collect(t, left().RightJoin(right(), On("id", "id")).Select([]string{"l.name", "r.value"})), [][]any{
		{"a", "x"}, {"b", "x"}, {"a", "y"}, {"b", "y"}, {"a", "z"}, {"b", "z"}, {nil, "w"}, {nil, "null-right"},
	})
	wantValues(t, collect(t, left().LeftJoin(right(), On("id", "id"), On("name", "value")).Select([]string{"r.value"})), [][]any{{nil}, {nil}, {nil}, {nil}})
	wantValues(t, collect(t, left().LeftJoin(right().Where(Col("r.value").Eq("x")), On("id", "id")).Where(Col("r.value").IsNotNull()).Select([]string{"l.name"})), [][]any{{"a"}, {"b"}})
	wantError(t, eq.From("L").LeftJoin(right(), On("id", "id")), "aliases")
	wantError(t, left().LeftJoin(eq.From("R").As("l"), On("id", "id")), "duplicate source alias")
	wantError(t, left().LeftJoin(right(), On("missing", "id")), "missing")
}

func TestSetOperationsAndTupleBoundaries(t *testing.T) {
	eq := workbook(t, map[string][][]any{
		"L": {{"a", "b"}, {"a|b", "c"}, {"a", "b|c"}, {"a|b", "c"}, {"null", nil}},
		"R": {{"a", "b"}, {"a", "b|c"}, {"new", "value"}, {"null", nil}},
	})
	wantValues(t, collect(t, eq.From("L").Union(eq.From("R"))), [][]any{{"a|b", "c"}, {"a", "b|c"}, {"null", nil}, {"new", "value"}})
	wantValues(t, collect(t, eq.From("L").Intersect(eq.From("R"))), [][]any{{"a", "b|c"}, {"null", nil}})
	wantValues(t, collect(t, eq.From("L").Except(eq.From("R"))), [][]any{{"a|b", "c"}})
	wantError(t, eq.From("L").Union(eq.From("R").Select([]string{"b", "a"})), "column names and order")
	// Local limits run before collection operators.
	wantValues(t, collect(t, eq.From("L").Limit(1).Union(eq.From("R").Limit(1))), [][]any{{"a|b", "c"}, {"a", "b|c"}})
}

func TestCustomFunctionsAndTypedEquality(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {{"v"}, {"A"}, {"a"}, {"B"}}})
	equalFold := func(a, b Row) (bool, error) {
		return strings.EqualFold(a.Values[0].(string), b.Values[0].(string)), nil
	}
	wantValues(t, collect(t, eq.From("Data").Union(eq.From("Data"), equalFold)), [][]any{{"A"}, {"B"}})
	wantValues(t, collect(t, eq.From("Data").Combine(eq.From("Data"), func(left, right []Row) ([]Row, error) { return append(left[:1], right[2:]...), nil })), [][]any{{"A"}, {"B"}})
	wantValues(t, collect(t, eq.From("Data").Aggregate(CustomAggregate("joined", []string{"v"}, func(rows []Row) (any, error) {
		var values []string
		for _, row := range rows {
			values = append(values, row.Values[0].(string))
		}
		return strings.Join(values, ","), nil
	}))), [][]any{{"A,a,B"}})
	boolMap := Map("flag", nil, func(Row) (any, error) { return true, nil })
	wantError(t, eq.From("Data").Map(boolMap).Union(eq.From("Data").Map(boolMap)), "custom equality")
	wantValues(t, collect(t, eq.From("Data").Map(boolMap).Union(eq.From("Data").Map(boolMap), func(a, b Row) (bool, error) {
		return reflect.DeepEqual(a.Values, b.Values), nil
	})), [][]any{{"A", true}, {"a", true}, {"B", true}})
	wantError(t, eq.From("Data").Combine(eq.From("Data"), func([]Row, []Row) ([]Row, error) { return []Row{{Columns: []string{"bad"}, Values: []any{"x"}}}, nil }), "schema")
	wantError(t, eq.From("Data").Union(eq.From("Data"), func(Row, Row) (bool, error) { return false, errors.New("equal failed") }), "equal failed")
}

// Rewrite a worksheet part to distinguish absent formula caches from empty ones.
func rewritePart(t *testing.T, eq *ExcelQuery, change func(string) string) *ExcelQuery {
	t.Helper()
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	for _, part := range eq.archive.File {
		r, err := part.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if part.Name == "xl/worksheets/sheet2.xml" {
			data = []byte(change(string(data)))
		}
		out, err := w.Create(part.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rewritten.xlsx")
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { result.Close() })
	return result
}

func TestRawValuesAndFormulaCaches(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {{"n", "text", "formula"}, {12, "001", nil}}}, func(f *excelize.File) {
		style, err := f.NewStyle(&excelize.Style{NumFmt: 2})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStyle("Data", "A2", "A2", style); err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellFormula("Data", "C2", "1+1"); err != nil {
			t.Fatal(err)
		}
	})
	wantError(t, eq.From("Data"), "formula has no cached result")
	cached := rewritePart(t, eq, func(xml string) string { return strings.Replace(xml, "<f>1+1</f>", "<f>1+1</f><v>2</v>", 1) })
	wantValues(t, collect(t, cached.From("Data")), [][]any{{"12", "001", "2"}})
	empty := rewritePart(t, eq, func(xml string) string { return strings.Replace(xml, "<f>1+1</f>", "<f>1+1</f><v></v>", 1) })
	wantValues(t, collect(t, empty.From("Data")), [][]any{{"12", "001", nil}})
}
