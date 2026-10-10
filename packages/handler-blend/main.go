package main

import "github.com/forgehubproject/fhr/packages/go/fhr"

// A .blend is read by a scene.Codec — a parser, no writer — and ReadOnly turns
// it into a handler that diffs, imports and previews but declares no export.
// The subprocess protocol and the wasm global both come from fhr.Run.
func main() {
	fhr.Run(Codec.ReadOnly(), Codec.Info())
}
