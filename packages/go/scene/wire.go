package scene

import "github.com/forgehubproject/fhr/packages/go/fhr"

// The wire types live in the fhr package; these aliases keep the engine's code
// unqualified. They are the same types, so scene.DiffChange and fhr.DiffChange
// are interchangeable for callers.
type (
	Blob             = fhr.Blob
	ChangeKind       = fhr.ChangeKind
	DiffChange       = fhr.DiffChange
	StructuredDiff   = fhr.StructuredDiff
	SemanticConflict = fhr.SemanticConflict
	ConflictInfo     = fhr.ConflictInfo
)

const (
	Added      = fhr.Added
	Removed    = fhr.Removed
	Modified   = fhr.Modified
	Renamed    = fhr.Renamed
	Reparented = fhr.Reparented
)
