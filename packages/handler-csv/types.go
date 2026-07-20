package main

// Wire types — match @fhr/types and the StructuredDiff JSON schema.
// No dependency on forge or ForgeHub; this package is a standalone binary.

// Blob is raw file content.
type Blob = []byte

// ChangeKind describes the nature of a semantic change.
type ChangeKind string

const (
	Added    ChangeKind = "added"
	Removed  ChangeKind = "removed"
	Modified ChangeKind = "modified"
)

// DiffChange is one semantic unit of change within a StructuredDiff.
type DiffChange struct {
	Path     string       `json:"path"`
	Kind     ChangeKind   `json:"kind"`
	Label    string       `json:"label,omitempty"`
	Before   any          `json:"before,omitempty"`
	After    any          `json:"after,omitempty"`
	Children []DiffChange `json:"children,omitempty"`
}

// StructuredDiff is the wire format returned by Diff.
type StructuredDiff struct {
	Version string       `json:"version"`
	Format  string       `json:"format"`
	Changes []DiffChange `json:"changes"`
}

// SemanticConflict is one unresolvable conflict at a semantic path.
type SemanticConflict struct {
	Path   string `json:"path"`
	Ours   any    `json:"ours"`
	Theirs any    `json:"theirs"`
}

// ConflictInfo collects all conflicts from a 3-way merge.
type ConflictInfo struct {
	Conflicts []SemanticConflict `json:"conflicts"`
}
