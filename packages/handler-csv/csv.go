// Package main is a format-aware handler for .csv files. It produces a
// semantic diff at column / row / cell granularity rather than a line patch,
// so a moved column or a single changed cell reads clearly.
//
// v0 aligns rows positionally (row N vs row N) with header awareness. Key-based
// row alignment (align by a primary-key column so inserts/deletes don't cascade)
// is the planned follow-up — see issue #17.
package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"path/filepath"
	"strings"
)

// Handler is the CSV format handler.
type Handler struct{}

// Match returns true for .csv files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".csv")
}

// parseCSV reads a blob into records, tolerating ragged rows (variable field
// counts). An empty blob yields no records (not an error) — the added/deleted
// file case.
func parseCSV(blob Blob) ([][]string, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	r := csv.NewReader(bytes.NewReader(blob))
	r.FieldsPerRecord = -1 // allow ragged rows; we diff cells individually
	r.LazyQuotes = true
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing CSV: %w", err)
	}
	return recs, nil
}

func header(recs [][]string) []string {
	if len(recs) == 0 {
		return nil
	}
	return recs[0]
}

func dataRows(recs [][]string) [][]string {
	if len(recs) <= 1 {
		return nil
	}
	return recs[1:]
}

func at(s []string, i int) (string, bool) {
	if i < 0 || i >= len(s) {
		return "", false
	}
	return s[i], true
}

// colName returns the header label for column i, or a positional fallback.
func colName(hdr []string, i int) string {
	if v, ok := at(hdr, i); ok && v != "" {
		return v
	}
	return fmt.Sprintf("col %d", i+1)
}

// Diff produces a column/row/cell semantic diff of two CSV blobs. An empty blob
// on either side is the added/deleted-file case (all rows added / removed).
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	baseRecs, err := parseCSV(base)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	headRecs, err := parseCSV(head)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("head: %w", err)
	}

	// Non-nil so an empty diff marshals as [] (never null).
	changes := []DiffChange{}
	changes = append(changes, diffHeader(header(baseRecs), header(headRecs))...)
	changes = append(changes, diffRows(dataRows(baseRecs), dataRows(headRecs), header(headRecs))...)

	return StructuredDiff{Version: "1.0", Format: "csv", Changes: changes}, nil
}

// diffHeader compares column headers positionally: added, removed, or renamed.
func diffHeader(a, b []string) []DiffChange {
	var ch []DiffChange
	n := max(len(a), len(b))
	for i := 0; i < n; i++ {
		av, aok := at(a, i)
		bv, bok := at(b, i)
		switch {
		case !aok && bok:
			ch = append(ch, DiffChange{Path: "columns." + bv, Kind: Added, Label: bv, After: bv})
		case aok && !bok:
			ch = append(ch, DiffChange{Path: "columns." + av, Kind: Removed, Label: av, Before: av})
		case av != bv:
			ch = append(ch, DiffChange{Path: "columns." + av, Kind: Modified, Label: av, Before: av, After: bv})
		}
	}
	return ch
}

// diffRows compares data rows positionally; a modified row carries per-cell
// children keyed by column name. Row labels use 1-based line numbers (the
// header is line 1), so they line up with what an editor shows.
func diffRows(baseData, headData [][]string, hdr []string) []DiffChange {
	var ch []DiffChange
	n := max(len(baseData), len(headData))
	for i := 0; i < n; i++ {
		br, bok := row(baseData, i)
		hr, hok := row(headData, i)
		line := i + 2 // +1 for header, +1 for 1-based
		path := fmt.Sprintf("rows.%d", line)
		label := fmt.Sprintf("row %d", line)
		switch {
		case !bok && hok:
			ch = append(ch, DiffChange{Path: path, Kind: Added, Label: label, After: strings.Join(hr, ", ")})
		case bok && !hok:
			ch = append(ch, DiffChange{Path: path, Kind: Removed, Label: label, Before: strings.Join(br, ", ")})
		default:
			if cells := diffCells(br, hr, hdr); len(cells) > 0 {
				ch = append(ch, DiffChange{Path: path, Kind: Modified, Label: label, Children: cells})
			}
		}
	}
	return ch
}

func row(data [][]string, i int) ([]string, bool) {
	if i < 0 || i >= len(data) {
		return nil, false
	}
	return data[i], true
}

// diffCells compares two rows cell by cell, labelling each change by its column.
func diffCells(a, b []string, hdr []string) []DiffChange {
	var ch []DiffChange
	n := max(len(a), len(b))
	for i := 0; i < n; i++ {
		av, aok := at(a, i)
		bv, bok := at(b, i)
		if aok && bok && av == bv {
			continue
		}
		if !aok && !bok {
			continue
		}
		col := colName(hdr, i)
		c := DiffChange{Path: col, Kind: Modified, Label: col}
		switch {
		case !aok:
			c.Kind = Added
			c.After = bv
		case !bok:
			c.Kind = Removed
			c.Before = av
		default:
			c.Before = av
			c.After = bv
		}
		ch = append(ch, c)
	}
	return ch
}

// Merge is not yet supported for CSV (v0 is diff-only). A cell-level 3-way
// merge is a planned follow-up (issue #17).
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for csv")
}
