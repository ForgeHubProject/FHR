package main

import "github.com/forgehubproject/fhr/packages/go/fhr"

// The wire types live in the shared fhr package; these aliases only keep the
// handler's own code unqualified. They are the same types, so they cannot
// drift from @fhr/types the way per-handler copies did.
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
