// Package fhr is the Go side of the FHR handler contract: the StructuredDiff
// wire types, and the two entry points every handler binary exposes — the
// subprocess protocol forge speaks (SPEC.md) and the wasm global ForgeHub and
// forge's --web shell call (SPEC-RENDERING.md §4, Tier B).
//
// A handler implements Handler and hands it to Run from its main package:
//
//	func main() {
//		fhr.Run(&Handler{}, fhr.Info{ID: "csv", Formats: []string{".csv"}})
//	}
//
// The build target picks the entry point — a native build speaks the
// subprocess protocol, a GOOS=js build registers the wasm global — so both
// run the exact same Handler and cannot drift from each other.
package fhr

import (
	"strings"
	"unicode"
)

// ProtocolVersion is the subprocess/wasm protocol revision these entry points
// speak. Info.Protocol defaults to it.
const ProtocolVersion = "1.0"

// Handler is what a format implements. Merge may return an error for formats
// that have no semantic merge yet; say so in Info.Capabilities as well.
type Handler interface {
	Match(path string) bool
	Diff(base, head Blob) (StructuredDiff, error)
	Merge(base, ours, theirs Blob) (Blob, *ConflictInfo, error)
}

// Info is the handler's answer to the protocol's "info" call. It is the same
// shape forge reads (internal/fhr.Info); fields a handler leaves unset are
// omitted rather than guessed.
type Info struct {
	ID           string        `json:"id"`
	Formats      []string      `json:"formats"`
	Protocol     string        `json:"protocol"`
	Capabilities *Capabilities `json:"capabilities,omitempty"`
}

// Capabilities is the handler's own declaration of what it supports — the
// HandlerCapabilities object of @fhr/types, which hosts pass to renderers so a
// presentation can be chosen honestly.
type Capabilities struct {
	SemanticCompare bool `json:"semanticCompare"`
	SemanticMerge   bool `json:"semanticMerge"`
}

func (i Info) withDefaults() Info {
	if i.Protocol == "" {
		i.Protocol = ProtocolVersion
	}
	return i
}

// binaryName is the released binary's name, used in the usage line.
func (i Info) binaryName() string { return "forge-handler-" + i.ID }

// GlobalName is the JS global a wasm build registers its api under:
// "__forgeHandler" plus the id in PascalCase ("gltf-scene" → GltfScene). Hosts
// discover it by the "__forgeHandler" prefix, so only the prefix is contract;
// the suffix keeps two handlers loaded on one page from colliding.
func GlobalName(id string) string {
	var b strings.Builder
	b.WriteString("__forgeHandler")
	upper := true
	for _, r := range id {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			upper = true
			continue
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		b.WriteRune(r)
	}
	return b.String()
}
