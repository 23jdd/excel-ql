package ql

import (
	"encoding/xml"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

type sourceCell struct {
	Ref     string    `xml:"r,attr"`
	Type    string    `xml:"t,attr"`
	Formula *struct{} `xml:"f"`
	Value   *string   `xml:"v"`
	Inline  *struct{} `xml:"is"`
}

func (eq *ExcelQuery) sheetPart(sheet string) (string, error) {
	var workbook struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	var rels struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	data, err := eq.xmlFile("xl/workbook.xml")
	if err != nil {
		return "", err
	}
	if err = xml.Unmarshal(data, &workbook); err != nil {
		return "", err
	}
	data, err = eq.xmlFile("xl/_rels/workbook.xml.rels")
	if err != nil {
		return "", err
	}
	if err = xml.Unmarshal(data, &rels); err != nil {
		return "", err
	}
	for _, s := range workbook.Sheets {
		if s.Name != sheet {
			continue
		}
		for _, rel := range rels.Items {
			if rel.ID != s.ID {
				continue
			}
			if rel.Mode == "External" {
				return "", fmt.Errorf("external worksheet is unsupported")
			}
			if strings.HasPrefix(rel.Target, "/") {
				return path.Clean(strings.TrimPrefix(rel.Target, "/")), nil
			}
			return path.Join("xl", rel.Target), nil
		}
		return "", fmt.Errorf("missing worksheet relationship")
	}
	return "", fmt.Errorf("worksheet does not exist")
}

func (eq *ExcelQuery) read(sheet, alias string) (table, error) {
	if eq == nil {
		return table{}, fmt.Errorf("nil workbook")
	}
	eq.mu.Lock()
	defer eq.mu.Unlock()
	if eq.closed || eq.f == nil || eq.archive == nil {
		return table{}, fmt.Errorf("workbook is closed")
	}
	part, err := eq.sheetPart(sheet)
	if err != nil {
		return table{}, err
	}
	data, err := eq.xmlFile(part)
	if err != nil {
		return table{}, err
	}
	var ws struct {
		Rows []struct {
			Cells []sourceCell `xml:"c"`
		} `xml:"sheetData>row"`
		Merges []struct {
			Ref string `xml:"ref,attr"`
		} `xml:"mergeCells>mergeCell"`
	}
	if err = xml.Unmarshal(data, &ws); err != nil {
		return table{}, err
	}
	type rect struct{ x1, y1, x2, y2 int }
	merges := make([]rect, 0, len(ws.Merges))
	for _, merge := range ws.Merges {
		ends := strings.Split(merge.Ref, ":")
		if len(ends) != 2 {
			return table{}, fmt.Errorf("invalid merged range %q", merge.Ref)
		}
		x1, y1, err := excelize.CellNameToCoordinates(ends[0])
		if err != nil {
			return table{}, err
		}
		x2, y2, err := excelize.CellNameToCoordinates(ends[1])
		if err != nil {
			return table{}, err
		}
		merges = append(merges, rect{x1, y1, x2, y2})
	}
	values := make(map[int]map[int]string)
	width := 0
	for _, row := range ws.Rows {
		for _, cell := range row.Cells {
			if cell.Value == nil && cell.Inline == nil && cell.Formula == nil {
				continue
			}
			x, y, err := excelize.CellNameToCoordinates(cell.Ref)
			if err != nil {
				return table{}, err
			}
			covered := false
			for _, m := range merges {
				if x >= m.x1 && x <= m.x2 && y >= m.y1 && y <= m.y2 && (x != m.x1 || y != m.y1) {
					covered = true
					break
				}
			}
			if covered {
				continue
			}
			if cell.Formula != nil && cell.Value == nil {
				return table{}, fmt.Errorf("row %d cell %s: formula has no cached result", y, cell.Ref)
			}
			if cell.Type == "e" {
				return table{}, fmt.Errorf("row %d cell %s: Excel error value", y, cell.Ref)
			}
			value, err := eq.f.GetCellValue(sheet, cell.Ref, excelize.Options{RawCellValue: true})
			if err != nil {
				return table{}, fmt.Errorf("row %d cell %s: %w", y, cell.Ref, err)
			}
			if value == "" {
				continue
			}
			if values[y] == nil {
				values[y] = make(map[int]string)
			}
			values[y][x] = value
			if x > width {
				width = x
			}
		}
	}
	if width == 0 {
		return table{}, fmt.Errorf("empty worksheet: missing header row")
	}
	t := table{columns: make([]string, width)}
	if alias != "" {
		t.aliases = []string{alias}
	}
	for i := range t.columns {
		name := strings.TrimSpace(values[1][i+1])
		if name == "" {
			return table{}, fmt.Errorf("row 1 column %d: empty header", i+1)
		}
		if alias != "" {
			name = alias + "." + name
		}
		t.columns[i] = name
	}
	if err := uniqueNames(t.columns); err != nil {
		return table{}, fmt.Errorf("row 1: %w", err)
	}
	indices := make([]int, 0, len(values))
	for y := range values {
		if y > 1 {
			indices = append(indices, y)
		}
	}
	sort.Ints(indices)
	for _, y := range indices {
		row := Row{Columns: t.columns, Values: make([]any, width)}
		for x, value := range values[y] {
			row.Values[x-1] = value
		}
		t.rows = append(t.rows, record{row: row, origin: fmt.Sprintf("sheet %q row %d", sheet, y)})
	}
	return t, nil
}
