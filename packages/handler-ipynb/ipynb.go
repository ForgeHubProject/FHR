// Package main is a format-aware handler for Jupyter notebooks (.ipynb). A
// notebook is JSON, so a line diff of the file drowns the real change in JSON
// structure, base64 images and shifting execution counts. This handler diffs
// it as what it is: an ordered list of cells, each a small text file (its
// source) with its outputs.
//
// Cells are paired by id (nbformat >= 4.5), then by content, so inserting a
// cell does not turn every cell after it into an edit. Each changed cell is
// reported with its whole source and outputs on both sides; drawing the line
// diff inside a cell is the renderer's job. Execution counts and metadata are
// run-to-run noise and are ignored.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Handler is the ipynb format handler.
type Handler struct{}

// Match returns true for .ipynb files.
func (h *Handler) Match(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".ipynb")
}

type rawNotebook struct {
	Cells    []rawCell       `json:"cells"`
	Metadata json.RawMessage `json:"metadata"`
}

type rawCell struct {
	CellType string            `json:"cell_type"`
	ID       string            `json:"id,omitempty"`
	Source   json.RawMessage   `json:"source"`
	Outputs  []json.RawMessage `json:"outputs,omitempty"`
}

// nbCell is one cell as the diff sees it.
type nbCell struct {
	index   int // 0-based position in its notebook
	kind    string
	id      string
	source  string
	outputs string
}

type notebook struct {
	cells  []nbCell
	kernel string
}

// text joins nbformat's "multiline string": a string or an array of lines.
func text(raw json.RawMessage) string {
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

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// outputText renders one cell's outputs as reviewable text: streams and
// text/plain results as they are, errors as name, value and traceback, and
// anything a reader cannot diff as text (images, HTML) as a one-line
// placeholder with its size, so a changed plot still shows as a change.
func outputText(outputs []json.RawMessage) string {
	var b strings.Builder
	for _, raw := range outputs {
		var o struct {
			OutputType string                     `json:"output_type"`
			Name       string                     `json:"name"`
			Text       json.RawMessage            `json:"text"`
			Data       map[string]json.RawMessage `json:"data"`
			EName      string                     `json:"ename"`
			EValue     string                     `json:"evalue"`
			Traceback  []string                   `json:"traceback"`
		}
		if json.Unmarshal(raw, &o) != nil {
			continue
		}
		var part string
		switch o.OutputType {
		case "stream":
			part = text(o.Text)
		case "error":
			// Jupyter's traceback already ends with "name: value"; it is only
			// spelled out when there is no traceback.
			part = o.EName + ": " + o.EValue
			if len(o.Traceback) > 0 {
				part = ansi.ReplaceAllString(strings.Join(o.Traceback, "\n"), "")
			}
		default: // execute_result, display_data
			mimes := make([]string, 0, len(o.Data))
			for m := range o.Data {
				mimes = append(mimes, m)
			}
			sort.Strings(mimes)
			if t, ok := o.Data["text/plain"]; ok {
				part = text(t)
			}
			for _, m := range mimes {
				if m == "text/plain" {
					continue
				}
				part = strings.TrimSuffix(part, "\n")
				if part != "" {
					part += "\n"
				}
				part += placeholder(m, o.Data[m])
			}
		}
		if part != "" && !strings.HasSuffix(part, "\n") {
			part += "\n"
		}
		b.WriteString(part)
	}
	return b.String()
}

// placeholder names a non-text output by type and size, e.g. "[image/png, 12.3 KB]".
func placeholder(mime string, raw json.RawMessage) string {
	body := text(raw)
	n := len(body)
	if strings.HasPrefix(mime, "image/") && mime != "image/svg+xml" {
		if dec, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(body), "")); err == nil {
			n = len(dec)
		}
	}
	return fmt.Sprintf("[%s, %s]", mime, humanSize(n))
}

func humanSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func parseNotebook(blob Blob) (*notebook, error) {
	if len(blob) == 0 {
		return &notebook{}, nil
	}
	var raw rawNotebook
	if err := json.Unmarshal(blob, &raw); err != nil {
		return nil, fmt.Errorf("parsing notebook JSON: %w", err)
	}
	nb := &notebook{kernel: kernelName(raw.Metadata)}
	for i, c := range raw.Cells {
		nb.cells = append(nb.cells, nbCell{index: i, kind: c.CellType, id: c.ID, source: text(c.Source), outputs: outputText(c.Outputs)})
	}
	return nb, nil
}

// kernelName is the notebook's kernel as a reader knows it ("Python 3").
func kernelName(meta json.RawMessage) string {
	var m struct {
		Kernelspec struct {
			DisplayName string `json:"display_name"`
			Name        string `json:"name"`
		} `json:"kernelspec"`
	}
	if json.Unmarshal(meta, &m) != nil {
		return ""
	}
	if m.Kernelspec.DisplayName != "" {
		return m.Kernelspec.DisplayName
	}
	return m.Kernelspec.Name
}

// ── pairing ──────────────────────────────────────────────────────────────────

// pair is one cell on either side; -1 marks a side the cell is missing from.
type pair struct{ a, b int }

// lineSimilarity is the Dice coefficient of two texts' line multisets: 1 for
// the same lines, 0 for none in common.
func lineSimilarity(x, y string) float64 {
	if x == y {
		return 1
	}
	xl, yl := strings.Split(x, "\n"), strings.Split(y, "\n")
	count := map[string]int{}
	for _, l := range xl {
		count[l]++
	}
	common := 0
	for _, l := range yl {
		if count[l] > 0 {
			count[l]--
			common++
		}
	}
	return 2 * float64(common) / float64(len(xl)+len(yl))
}

// minSimilarity is how much two cells of the same type must share to count as
// one cell edited rather than one removed and another added.
const minSimilarity = 0.5

// maxAlign bounds the content-alignment table; past it, leftovers are reported
// as removed and added rather than paired, which is honest and never slow.
const maxAlign = 4_000_000

// pairCells matches base cells to head cells: by id where an id is unique on
// both sides, then by content in order (an alignment that maximises total
// similarity, where only identical cells or same-type cells sharing at least
// minSimilarity of their lines may pair).
func pairCells(a, b []nbCell) []pair {
	matchA := make([]int, len(a))
	matchB := make([]int, len(b))
	for i := range matchA {
		matchA[i] = -1
	}
	for j := range matchB {
		matchB[j] = -1
	}

	ids := func(cells []nbCell) map[string]int {
		seen := map[string]int{}
		for i, c := range cells {
			if c.id == "" {
				continue
			}
			if _, dup := seen[c.id]; dup {
				seen[c.id] = -1
			} else {
				seen[c.id] = i
			}
		}
		return seen
	}
	ia, ib := ids(a), ids(b)
	for id, i := range ia {
		if j, ok := ib[id]; ok && i >= 0 && j >= 0 {
			matchA[i], matchB[j] = j, i
		}
	}

	// Content alignment over the cells ids did not pair, in order.
	var ra, rb []int
	for i := range a {
		if matchA[i] < 0 {
			ra = append(ra, i)
		}
	}
	for j := range b {
		if matchB[j] < 0 {
			rb = append(rb, j)
		}
	}
	if len(ra) > 0 && len(rb) > 0 && len(ra)*len(rb) <= maxAlign {
		score := func(i, j int) float64 {
			x, y := a[ra[i]], b[rb[j]]
			if x.kind == y.kind && x.source == y.source {
				return 2 // identical cells always pair, ahead of any edit
			}
			if x.kind != y.kind {
				return 0
			}
			if s := lineSimilarity(x.source, y.source); s >= minSimilarity {
				return s
			}
			return 0
		}
		n, m := len(ra), len(rb)
		best := make([][]float64, n+1)
		for i := range best {
			best[i] = make([]float64, m+1)
		}
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				v := max(best[i+1][j], best[i][j+1])
				if s := score(i, j); s > 0 && best[i+1][j+1]+s > v {
					v = best[i+1][j+1] + s
				}
				best[i][j] = v
			}
		}
		for i, j := 0, 0; i < n && j < m; {
			if s := score(i, j); s > 0 && best[i][j] == best[i+1][j+1]+s {
				matchA[ra[i]], matchB[rb[j]] = rb[j], ra[i]
				i, j = i+1, j+1
			} else if best[i+1][j] >= best[i][j+1] {
				i++
			} else {
				j++
			}
		}
	}

	pairMoved(a, b, matchA, matchB)

	// Notebook order: head cells in order, each removed cell just after the
	// head position of the last base cell before it that survived.
	type keyed struct {
		p   pair
		key float64
	}
	var rows []keyed
	for j := range b {
		rows = append(rows, keyed{pair{matchB[j], j}, float64(j)})
	}
	anchor := -1.0 // removals before any surviving cell sort ahead of head cell 0
	for i := range a {
		if matchA[i] >= 0 {
			anchor = float64(matchA[i])
			continue
		}
		anchor += 0.001 // keep consecutive removals in base order
		rows = append(rows, keyed{pair{i, -1}, anchor + 0.5})
	}
	sort.SliceStable(rows, func(x, y int) bool { return rows[x].key < rows[y].key })
	out := make([]pair, len(rows))
	for k, r := range rows {
		out[k] = r.p
	}
	return out
}

// pairMoved pairs the cells the in-order alignment left over, regardless of
// order, so a cell that moved past others is one moved cell rather than one
// removed and one added: first identical cells unique among the leftovers on
// both sides, then the most similar same-type cells (at least minSimilarity),
// best pairs first.
func pairMoved(a, b []nbCell, matchA, matchB []int) {
	key := func(c nbCell) string { return c.kind + "\x00" + c.source }
	free := func(match []int) []int {
		var out []int
		for i, m := range match {
			if m < 0 {
				out = append(out, i)
			}
		}
		return out
	}
	fa, fb := free(matchA), free(matchB)
	if len(fa) == 0 || len(fb) == 0 || len(fa)*len(fb) > maxAlign {
		return
	}
	countA, countB, atB := map[string]int{}, map[string]int{}, map[string]int{}
	for _, i := range fa {
		countA[key(a[i])]++
	}
	for _, j := range fb {
		countB[key(b[j])]++
		atB[key(b[j])] = j
	}
	for _, i := range fa {
		k := key(a[i])
		if countA[k] == 1 && countB[k] == 1 {
			j := atB[k]
			matchA[i], matchB[j] = j, i
		}
	}

	type cand struct {
		i, j int
		sim  float64
	}
	var cands []cand
	for _, i := range free(matchA) {
		for _, j := range free(matchB) {
			if a[i].kind != b[j].kind {
				continue
			}
			if s := lineSimilarity(a[i].source, b[j].source); s >= minSimilarity {
				cands = append(cands, cand{i, j, s})
			}
		}
	}
	sort.SliceStable(cands, func(x, y int) bool { return cands[x].sim > cands[y].sim })
	for _, c := range cands {
		if matchA[c.i] < 0 && matchB[c.j] < 0 {
			matchA[c.i], matchB[c.j] = c.j, c.i
		}
	}
}

// moved marks the paired cells whose relative order changed: the pairs left
// out of the longest run that kept its order.
func moved(pairs []pair) map[int]bool {
	var seq []pair // matched pairs in head order
	for _, p := range pairs {
		if p.a >= 0 && p.b >= 0 {
			seq = append(seq, p)
		}
	}
	// Longest increasing subsequence of base indices (patience sorting).
	tails, prev := []int{}, make([]int, len(seq))
	for k, p := range seq {
		lo := sort.Search(len(tails), func(t int) bool { return seq[tails[t]].a >= p.a })
		if lo > 0 {
			prev[k] = tails[lo-1]
		} else {
			prev[k] = -1
		}
		if lo == len(tails) {
			tails = append(tails, k)
		} else {
			tails[lo] = k
		}
	}
	keep := map[int]bool{}
	if len(tails) > 0 {
		for k := tails[len(tails)-1]; k >= 0; k = prev[k] {
			keep[seq[k].b] = true
		}
	}
	out := map[int]bool{}
	for _, p := range seq {
		if !keep[p.b] {
			out[p.b] = true
		}
	}
	return out
}

// ── diff ─────────────────────────────────────────────────────────────────────

// cellPath is the change path for a cell: its id when it has one, else its
// 1-based position, escaped as SPEC.md requires for a path segment. Without
// an id, a removed cell's position is in the base notebook and everything
// else's in the head, so removed cells get their own "base-" positions.
func cellPath(c nbCell, removed bool) string {
	seg := c.id
	if seg == "" {
		seg = fmt.Sprint(c.index + 1)
		if removed {
			seg = "base-" + seg
		}
	}
	seg = strings.NewReplacer("%", "%25", "/", "%2F").Replace(seg)
	return "cells/" + seg
}

// cellLabel names a cell by its position in the notebook it is in: the head,
// or for a removed cell the base ("code cell (was 6)").
func cellLabel(c nbCell, removed bool) string {
	if removed {
		return fmt.Sprintf("%s cell (was %d)", c.kind, c.index+1)
	}
	return fmt.Sprintf("%s cell %d", c.kind, c.index+1)
}

// Diff produces a cell-level semantic diff of two notebooks. An empty blob on
// either side is the added/deleted-file case (every cell added / removed).
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
	if a.kernel != b.kernel && len(base) > 0 && len(head) > 0 {
		changes = append(changes, DiffChange{Path: "metadata/kernel", Kind: Modified, Label: "kernel", Before: a.kernel, After: b.kernel})
	}

	pairs := pairCells(a.cells, b.cells)
	movedCells := moved(pairs)
	for _, p := range pairs {
		switch {
		case p.a < 0:
			c := b.cells[p.b]
			changes = append(changes, DiffChange{Path: cellPath(c, false), Kind: Added, Label: cellLabel(c, false), Children: sideRows(c, Added)})
		case p.b < 0:
			c := a.cells[p.a]
			changes = append(changes, DiffChange{Path: cellPath(c, true), Kind: Removed, Label: cellLabel(c, true), Children: sideRows(c, Removed)})
		default:
			x, y := a.cells[p.a], b.cells[p.b]
			var kids []DiffChange
			if movedCells[p.b] {
				kids = append(kids, DiffChange{Path: "position", Kind: Modified, Label: "position", Before: x.index + 1, After: y.index + 1})
			}
			if x.kind != y.kind {
				kids = append(kids, DiffChange{Path: "cell_type", Kind: Modified, Label: "type", Before: x.kind, After: y.kind})
			}
			if x.source != y.source {
				kids = append(kids, DiffChange{Path: "source", Kind: Modified, Label: "source", Before: x.source, After: y.source})
			}
			if x.outputs != y.outputs {
				kids = append(kids, DiffChange{Path: "outputs", Kind: Modified, Label: "outputs", Before: x.outputs, After: y.outputs})
			}
			if len(kids) > 0 {
				changes = append(changes, DiffChange{Path: cellPath(y, false), Kind: Modified, Label: cellLabel(y, false), Children: kids})
			}
		}
	}
	// Paths are selection keys and must be unique; only duplicate ids that
	// pairing could not use can collide, so number the repeats.
	seen := map[string]int{}
	for i := range changes {
		p := changes[i].Path
		if seen[p]++; seen[p] > 1 {
			changes[i].Path = fmt.Sprintf("%s~%d", p, seen[p])
		}
	}
	return StructuredDiff{Version: "1.0", Format: "ipynb", Changes: changes}, nil
}

// sideRows lists an added or removed cell's source and outputs, so a renderer
// can draw the whole cell as added or removed lines.
func sideRows(c nbCell, kind ChangeKind) []DiffChange {
	rows := []DiffChange{{Path: "source", Kind: kind, Label: "source"}}
	if c.outputs != "" {
		rows = append(rows, DiffChange{Path: "outputs", Kind: kind, Label: "outputs"})
	}
	for i := range rows {
		v := c.source
		if rows[i].Path == "outputs" {
			v = c.outputs
		}
		if kind == Added {
			rows[i].After = v
		} else {
			rows[i].Before = v
		}
	}
	return rows
}

// Merge is not yet supported for notebooks (diff-only): forge falls back to
// picking a side. Cell-level 3-way merge is a planned follow-up (issue #18).
func (h *Handler) Merge(_, _, _ Blob) (Blob, *ConflictInfo, error) {
	return nil, nil, fmt.Errorf("semantic merge is not yet supported for ipynb")
}
