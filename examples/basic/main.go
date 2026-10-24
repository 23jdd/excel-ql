// Read a workbook with: go run ./examples/basic -file sales.xlsx -sheet Sales
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	ql "github.com/23jdd/excel-ql"
)

func main() {
	file := flag.String("file", "", "Excel workbook path (required)")
	sheet := flag.String("sheet", "", "worksheet name (defaults to first worksheet)")
	columns := flag.String("columns", "", "comma-separated output columns (defaults to all)")
	offset := flag.Int("offset", 0, "rows to skip before applying the limit")
	limit := flag.Int("limit", 10, "maximum output rows")
	flag.Parse()
	if *file == "" {
		log.Fatal("provide -file with an Excel workbook path")
	}
	if err := run(*file, *sheet, *columns, *offset, *limit); err != nil {
		log.Fatal(err)
	}
}

func run(file, sheet, columns string, offset, limit int) (err error) {
	db, err := ql.Open(file)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); err == nil {
			err = closeErr
		}
	}()
	if sheet == "" {
		sheets := db.Sheets()
		if len(sheets) == 0 {
			return fmt.Errorf("workbook has no worksheets")
		}
		sheet = sheets[0]
	}
	query := db.From(sheet).Offset(offset).Limit(limit)
	if columns != "" {
		names := strings.Split(columns, ",")
		for i := range names {
			names[i] = strings.TrimSpace(names[i])
		}
		query.Select(names)
	}
	rows, err := query.Query()
	if err != nil {
		return err
	}
	count := 0
	for row := range rows {
		if count == 0 {
			fmt.Println(row.Columns)
		}
		fmt.Println(row.Values)
		count++
	}
	fmt.Printf("%d rows\n", count)
	return nil
}
