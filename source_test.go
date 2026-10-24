package ql

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestUnnamedColumnsAreIgnored(t *testing.T) {
	cases := []struct {
		name    string
		rows    [][]any
		columns []string
		values  [][]any
	}{
		{"trailing", [][]any{{"id"}, {"a", "ignored"}}, []string{"id"}, [][]any{{"a"}}},
		{"internal", [][]any{{"id", nil, "score"}, {"a", "ignored", "10"}, {"b", "ignored", nil}}, []string{"id", "score"}, [][]any{{"a", "10"}, {"b", nil}}},
		{"leading", [][]any{{nil, "name", nil, "id"}, {"ignored", "Alice", "ignored", "001"}}, []string{"name", "id"}, [][]any{{"Alice", "001"}}},
		{"whitespace header", [][]any{{" id ", " \t", " score "}, {"a", "ignored", "10"}}, []string{"id", "score"}, [][]any{{"a", "10"}}},
		{"unnamed values only", [][]any{{"id", nil}, {nil, "ignored"}, {"a", nil}, {"", "ignored"}}, []string{"id"}, [][]any{{"a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eq := workbook(t, map[string][][]any{"Data": tc.rows})
			rows := collect(t, eq.From("Data"))
			wantValues(t, rows, tc.values)
			if !reflect.DeepEqual(rows[0].Columns, tc.columns) {
				t.Fatalf("columns: got %v, want %v", rows[0].Columns, tc.columns)
			}
		})
	}
}

func TestUnnamedColumnsSkipFormulaAndCellErrors(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {
		{"id", nil, " \t", nil, nil, "score"},
		{"001", nil, nil, "placeholder", "placeholder", "10"},
		{nil, "ignored row"},
	}}, func(f *excelize.File) {
		for _, cell := range []string{"B2", "C2", "I187", "J187"} {
			if err := f.SetCellFormula("Data", cell, "1+1"); err != nil {
				t.Fatal(err)
			}
		}
	})
	eq = rewritePart(t, eq, func(part string) string {
		for cell, replacement := range map[string]string{
			"D2": `<c r="D2" t="e"><v>#DIV/0!</v></c>`,
			"E2": `<c r="E2" t="s"><v>99999999</v></c>`,
		} {
			pattern := regexp.MustCompile(`<c\b[^>]*\br="` + cell + `"[^>]*>.*?</c>`)
			if !pattern.MatchString(part) {
				t.Fatalf("missing fixture cell %s", cell)
			}
			part = pattern.ReplaceAllString(part, replacement)
		}
		return part
	})
	wantValues(t, collect(t, eq.From("Data").Where(Number("score").Gt(5))), [][]any{{"001", "10"}})
	wantValues(t, collect(t, eq.From("Data").As("d").Select([]string{"d.score", "d.id"})), [][]any{{"10", "001"}})
	wantError(t, eq.From("Data").Select([]string{"missing"}), "unknown column")
}

func TestUnnamedColumnsIgnoreDistantDataWithoutRange(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {
		{"组号", "学号", "姓名", "登记ID", "提交ID", "次数", "分工", "地址"},
		{"1", "001", "测试甲", "user-a", "a", "2", "开发", "repo-a"},
		{"2", "002", "测试乙", "user-b", "b", "3", "测试", "repo-b"},
	}}, func(f *excelize.File) {
		for _, cell := range []string{"I187", "J187"} {
			if err := f.SetCellStr("Data", cell, "outside named columns"); err != nil {
				t.Fatal(err)
			}
		}
	})
	wantValues(t, collect(t, eq.From("Data").Select([]string{"姓名", "登记ID"})), [][]any{{"测试甲", "user-a"}, {"测试乙", "user-b"}})
}

func TestDuplicateNamedHeadersStillFail(t *testing.T) {
	eq := workbook(t, map[string][][]any{"Data": {{"id", nil, " id "}, {"a", "ignored", "b"}}})
	wantError(t, eq.From("Data"), "duplicate column")
}
