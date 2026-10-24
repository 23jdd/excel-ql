package ql

import (
	"slices"
	"testing"
)

func TestOffsetPagination(t *testing.T) {
	eq := workbook(t, map[string][][]any{
		"Data":  {{"id", "n"}, {"a", "30"}, {"b", "10"}, {"c", "40"}, {"d", "20"}},
		"Empty": {{"id", "n"}},
	})
	cases := []struct {
		name  string
		build func() *Stmt
		want  [][]any
	}{
		{"zero", func() *Stmt { return eq.From("Data").Offset(0) }, [][]any{{"a", "30"}, {"b", "10"}, {"c", "40"}, {"d", "20"}}},
		{"without limit", func() *Stmt { return eq.From("Data").Offset(2) }, [][]any{{"c", "40"}, {"d", "20"}}},
		{"last call wins", func() *Stmt { return eq.From("Data").Offset(3).Offset(1).Limit(1) }, [][]any{{"b", "10"}}},
		{"reset", func() *Stmt { return eq.From("Data").Offset(3).Offset(0).Limit(1) }, [][]any{{"a", "30"}}},
		{"after sorting", func() *Stmt { return eq.From("Data").OrderBy(NumericAsc("n")).Offset(1).Limit(2) }, [][]any{{"d", "20"}, {"a", "30"}}},
		{"call order", func() *Stmt { return eq.From("Data").Limit(2).Offset(1).OrderBy(NumericAsc("n")) }, [][]any{{"d", "20"}, {"a", "30"}}},
		{"after filtering", func() *Stmt { return eq.From("Data").Offset(1).Where(Number("n").Gt(10)).Limit(1) }, [][]any{{"c", "40"}}},
		{"at end", func() *Stmt { return eq.From("Data").Offset(4) }, nil},
		{"past end", func() *Stmt { return eq.From("Data").Offset(5) }, nil},
		{"maximum int", func() *Stmt { return eq.From("Data").Offset(int(^uint(0) >> 1)).Limit(10) }, nil},
		{"zero limit", func() *Stmt { return eq.From("Data").Offset(1).Limit(0) }, nil},
		{"empty input", func() *Stmt { return eq.From("Empty").Offset(1) }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { wantValues(t, collect(t, tc.build()), tc.want) })
	}
	wantError(t, eq.From("Data").Offset(-1), "Offset must not be negative")
	wantError(t, eq.From("Empty").Offset(-1), "Offset must not be negative")
	wantError(t, eq.From("Data").Offset(-1).Offset(0), "Offset must not be negative")
	// Skipping all rows must not bypass structural or computation errors.
	wantError(t, eq.From("Data").Offset(100).Select([]string{"missing"}), "missing")
}

func TestOffsetAfterAggregationAndBeforeSets(t *testing.T) {
	eq := workbook(t, map[string][][]any{
		"Data": {{"team", "n"}, {"a", "20"}, {"b", "10"}, {"a", "10"}, {"c", "40"}},
	})
	wantValues(t, collect(t, eq.From("Data").GroupBy("team").Aggregate(Sum("n").As("total")).
		Having(Number("total").Gt(15)).OrderBy(NumericDesc("total")).Offset(1).Limit(1)), [][]any{{"a", float64(30)}})
	wantValues(t, collect(t, eq.From("Data").Select([]string{"team"}).Offset(1).Limit(1).
		Union(eq.From("Data").Select([]string{"team"}).Offset(3).Limit(1))), [][]any{{"b"}, {"c"}})
	st := eq.From("Data").Offset(1).Limit(1)
	seq, err := st.Query()
	if err != nil {
		t.Fatal(err)
	}
	st.Offset(2)
	wantValues(t, slices.Collect(seq), [][]any{{"b", "10"}})
	wantValues(t, collect(t, st), [][]any{{"a", "10"}})
}
