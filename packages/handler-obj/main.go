package main

import "github.com/forgehubproject/fhr/packages/go/fhr"

// The subprocess protocol (native builds) and the wasm global (GOOS=js builds)
// both come from fhr.Run, so they run this exact Handler — preview included.
func main() {
	fhr.Run(&Handler{}, fhr.Info{
		ID:           "obj",
		Formats:      []string{".obj"},
		Capabilities: &fhr.Capabilities{SemanticCompare: true, SemanticMerge: true},
	})
}
