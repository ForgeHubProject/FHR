package main

import (
	"runtime/debug"
	"math/rand"
	"testing"
)

// Malformed and hostile input must come back as errors, never panics: this
// handler runs in a server-side wasm worker, where a panic takes the call down.
func TestHostileInputNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, good := range hostileSeeds(t) {
		try := func(b []byte) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic %v\n%s", r, debug.Stack())
				}
			}()
			_, _ = decodeAny(b)
		}
		for n := 0; n <= len(good); n += max(1, len(good)/300) { // truncations
			try(good[:n])
		}
		for i := 0; i < 600; i++ { // corruptions
			b := append([]byte(nil), good...)
			if len(b) == 0 {
				break
			}
			for k := 0; k < 1+rng.Intn(6); k++ {
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			}
			try(b)
		}
	}
}

func hostileSeeds(t *testing.T) [][]byte {
	glb, _ := (&Handler{}).Import([]byte(bracketOBJ))
	return [][]byte{[]byte(bracketOBJ), glb}
}

// decodeAny feeds a blob to both directions: as OBJ text, and as a GLB.
func decodeAny(b []byte) (any, error) {
	h := &Handler{}
	if _, err := h.Import(b); err != nil {
		_, err2 := h.Export(b, ".obj")
		return nil, err2
	}
	return nil, nil
}
