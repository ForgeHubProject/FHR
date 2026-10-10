package main

import "github.com/forgehubproject/fhr/packages/go/fhr"

// Collada is a scene.Codec — a parser and a writer — and the codec derives the
// whole handler (diff, import, export, preview). The subprocess protocol and
// the wasm global both come from fhr.Run.
func main() {
	fhr.Run(Codec.Handler(), Codec.Info())
}
