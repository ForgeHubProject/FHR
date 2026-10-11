// Package main is a format-aware handler for Jupyter notebooks (.ipynb). A
// notebook is JSON, so a text diff drowns the real change in output churn and
// shifting execution_counts. This handler diffs by cell and treats outputs and
// execution_count as volatile — ignored by default — so the diff reflects
// actual source/structure changes.
//
// v0 aligns cells positionally (cell N vs cell N). Aligning by cell id
// (nbformat >= 4.5) or source similarity is the planned follow-up — issue #18.
package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Handler is the ipynb format handler.
type Handler struct{}

// Match returns true for .ipynb files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".ipynb")
}

type notebook struct {
	Cells []cell `json:"cells"`
}

// cell captures only the fields we diff. outputs and execution_count are
// deliberately omitted — they're volatile and ignored by default.
type cell struct {
	CellType string          `json:"cell_type"`
	Source   json.RawMessage `json:"source"`
	ID       string          `json:"id,omitempty"`
}

// sourceText normalizes a cell's source, which nbformat allows to be either a
// single string or an array of line strings.
func sourceText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err == nil {
		return strings.Join(lines, "")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func parseNotebook(blob Blob) (*notebook, error) {
	if len(blob) == 0 {
		return &notebook{}, nil
	}
	var nb notebook
	if err := json.Unmarshal(blob, &nb); err != nil {
		return nil, fmt.Errorf("parsing notebook JSON: %w", err)
	}
	return &nb, nil
}

func cellAt(cells []cell, i int) (cell, bool) {
	if i < 0 || i >= len(cells) {
		return cell{}, false
	}
	return cells[i], true
}

// Diff produces a cell-level semantic diff of two notebooks, ignoring outputs
// and execution_count. An empty blob on either side is the added/deleted-file
// case (all cells added / removed).
func (h *Handler) Diff(base, head Blob) (StructuredDiff, error) {
	a, err := parseNotebook(base)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("base: %w", err)
	}
	b, err := parseNotebook(head)
	if err != nil {
		return StructuredDiff{}, fmt.Errorf("head: %w", err)
	}

	changes := []DiffChange{} // non-nil so an empty diff marshals as [] not null
	n := max(len(a.Cells), len(b.Cells))
	for i := 0; i < n; i++ {
		ac, aok := cellAt(a.Cells, i)
		bc, bok := cellAt(b.Cells, i)
		path := fmt.Sprintf("cells.%d", i+1)
		label := fmt.Sprintf("cell %d", i+1)
		switch {
		case !aok && bok:
			changes = append(changes, DiffChange{Path: path, Kind: Added, Label: label + " (" + bc.CellType + ")", After: sourceText(bc.Source)})
		case aok && !bok:
			changes = append(changes, DiffChange{Path: path, Kind: Removed, Label: label + " (" + ac.CellType + ")", Before: sourceText(ac.Source)})
		default:
			var kids []DiffChange
			if ac.CellType != bc.CellType {
				kids = append(kids, DiffChange{Path: "cell_type", Kind: Modified, Label: "type", Before: ac.CellType, After: bc.CellType})
			}
			as, bs := sourceText(ac.Source), sourceText(bc.Source)
			if as != bs {
				kids = append(kids, DiffChange{Path: "source", Kind: Modified, Label: "source", Before: as, After: bs})
			}
			if len(kids) > 0 {
				changes = append(changes, DiffChange{Path: path, Kind: Modified, Label: label, Children: kids})
			}
		}
	}

	return StructuredDiff{Version: "1.0", Format: "ipynb", Changes: changes}, nil
}

// Merge is not yet supported for notebooks (v0 is diff-only). Cell-level 3-way
// merge is a planned follow-up (issue #18).
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for ipynb")
}
