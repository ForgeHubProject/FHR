package main

import "github.com/forgehubproject/fhr/packages/go/fhr"

// The wire types live in the fhr package; these aliases keep the handler's code
// unqualified.
type (
	Blob           = fhr.Blob
	ChangeKind     = fhr.ChangeKind
	DiffChange     = fhr.DiffChange
	StructuredDiff = fhr.StructuredDiff
	ConflictInfo   = fhr.ConflictInfo
)

const (
	Added    = fhr.Added
	Removed  = fhr.Removed
	Modified = fhr.Modified
)
