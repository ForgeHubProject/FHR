package main

import (
	"fmt"
	"strings"

	"github.com/forgehubproject/fhr/packages/go/fhr"
)

// ApplyChoices applies a resolver's decisions to a merge that left conflicts:
// for each conflict path in takePaths — the paths Merge reported, `nodes/<key>`
// or `mtllib` — theirs replaces what the merge kept (ours). A node theirs
// removed is removed; a group whose object the merge dropped brings that
// object back from theirs. Everything else stays as merged, and the file is
// written fresh, so every index is right however the choices mix the sides.
//
// This is what `forge mergetool` calls once the reviewer has picked, per
// conflict, "keep current" or "take incoming". A path it does not know is an
// error: a choice silently ignored would read as resolved.
func (h *Handler) ApplyChoices(merged, theirs fhr.Blob, takePaths []string) (fhr.Blob, error) {
	if len(takePaths) == 0 {
		return merged, nil
	}
	mf, err := parseOBJ(merged)
	if err != nil {
		return nil, fmt.Errorf("merged: %w", err)
	}
	tf, err := parseOBJ(theirs)
	if err != nil {
		return nil, fmt.Errorf("theirs: %w", err)
	}
	m, t := indexSides(mf), indexSides(tf)

	libs := mf.mtlLibs
	take := map[string]bool{}
	for _, p := range takePaths {
		switch {
		case p == "mtllib":
			libs = tf.mtlLibs
		case strings.HasPrefix(p, "nodes/"):
			key := unescapeSegment(strings.TrimPrefix(p, "nodes/"))
			id, ok := idByKey(m, key)
			if !ok {
				id, ok = idByKey(t, key)
			}
			if !ok {
				return nil, fmt.Errorf("conflict path %q names no object or group in either file", p)
			}
			take[id] = true
		default:
			return nil, fmt.Errorf("unknown conflict path %q", p)
		}
	}

	ids := mergeOrder(m.order, t.order)
	decision := make(map[string]pick, len(ids))
	for _, id := range ids {
		_, inM := m.nodes[id]
		_, inT := t.nodes[id]
		switch {
		case take[id] && inT:
			decision[id] = fromTheirs
		case take[id]:
			decision[id] = drop // theirs removed it
		case inM:
			decision[id] = fromOurs
		}
	}
	// A group taken from theirs whose object is not in the merged file (it was
	// removed with its object) needs that object: bring it from theirs too.
	for _, id := range ids {
		if decision[id] == drop {
			continue
		}
		if p := parentOf(id, m, t); p != "" && decision[p] == drop {
			if _, inT := t.nodes[p]; inT {
				decision[p] = fromTheirs
			} else {
				decision[id] = drop
			}
		}
	}
	return writeOBJ(mf.header, libs, buildTree(ids, decision, m, t, nil)), nil
}

func idByKey(s *side, key string) (string, bool) {
	for id, k := range s.key {
		if k == key {
			return id, true
		}
	}
	return "", false
}
