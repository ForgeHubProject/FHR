package main

import "github.com/forgehubproject/fhr/packages/go/fhr"

// handlerID names this handler in its info and in the diffs it writes.
const handlerID = "jpeg"

// exts are the extensions this handler claims, matched case-insensitively.
var exts = []string{".jpg", ".jpeg"}

// The subprocess protocol (native builds) and the wasm global (GOOS=js builds)
// both come from the shared entry points, so they run this exact Handler.
func main() {
	fhr.Run(&Handler{}, fhr.Info{
		ID:           handlerID,
		Formats:      exts,
		Capabilities: &fhr.Capabilities{SemanticCompare: true, SemanticMerge: false},
	})
}
