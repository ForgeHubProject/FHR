package main

import (
	"github.com/forgehubproject/fhr/packages/go/fhr"
	"github.com/forgehubproject/fhr/packages/go/scene"
)

// The glTF/GLB handler is the 3D family engine itself: packages/go/scene holds
// the diff, merge and identity logic, shared with every format that converts to
// glTF. The subprocess protocol (native builds) and the wasm global (GOOS=js
// builds) both come from fhr.Run, so they run this exact Handler.
func main() {
	fhr.Run(&scene.Handler{}, fhr.Info{
		ID:           "gltf-scene",
		Formats:      []string{".gltf", ".glb"},
		Capabilities: &fhr.Capabilities{SemanticCompare: true, SemanticMerge: true},
	})
}
