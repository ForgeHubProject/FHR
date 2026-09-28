package main

import "github.com/forgehubproject/fhr/packages/go/fhr"

// The subprocess protocol (native builds) and the wasm global (GOOS=js builds)
// both come from the shared entry points, so they run this exact Handler.
func main() {
	fhr.Run(&Handler{}, fhr.Info{
		ID:           "ipynb",
		Formats:      []string{".ipynb"},
		Capabilities: &fhr.Capabilities{SemanticCompare: true, SemanticMerge: false},
	})
}
