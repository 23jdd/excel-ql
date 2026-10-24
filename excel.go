// Package ql provides read-only, composable queries over Excel worksheets.
package ql

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/xuri/excelize/v2"
)

// ExcelQuery owns an open workbook. Close it after generating query results.
type ExcelQuery struct {
	mu      sync.Mutex
	f       *excelize.File
	archive *zip.Reader
	closed  bool
}

func Open(path string) (*ExcelQuery, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &ExcelQuery{f: f, archive: archive}, nil
}

// Close is idempotent. Previously generated sequences remain usable.
func (eq *ExcelQuery) Close() error {
	if eq == nil {
		return nil
	}
	eq.mu.Lock()
	defer eq.mu.Unlock()
	if eq.closed || eq.f == nil {
		return nil
	}
	eq.closed = true
	err := eq.f.Close()
	eq.f, eq.archive = nil, nil
	return err
}

func (eq *ExcelQuery) Sheets() []string {
	if eq == nil {
		return nil
	}
	eq.mu.Lock()
	defer eq.mu.Unlock()
	if eq.closed || eq.f == nil {
		return nil
	}
	return append([]string(nil), eq.f.GetSheetList()...)
}

func (eq *ExcelQuery) From(sheet string) *Stmt { return &Stmt{eq: eq, sheet: sheet} }

func (eq *ExcelQuery) xmlFile(name string) ([]byte, error) {
	f, err := eq.archive.Open(name)
	if err != nil {
		return nil, fmt.Errorf("workbook part %q: %w", name, err)
	}
	defer f.Close()
	return io.ReadAll(f)
}
