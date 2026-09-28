package fhr

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// ── stdin/stdout message shapes (SPEC.md, subprocess protocol) ────────────────

type diffInput struct {
	Base string `json:"base"` // base64-encoded blob
	Head string `json:"head"` // base64-encoded blob
}

type mergeInput struct {
	Base   string `json:"base"`
	Ours   string `json:"ours"`
	Theirs string `json:"theirs"`
}

type mergeOutput struct {
	Blob      string             `json:"blob"`                // base64-encoded merged blob
	Conflicts []SemanticConflict `json:"conflicts,omitempty"` // omitted on clean merge
}

// cliError is a failure the protocol reports on stderr as {"error": "..."}.
type cliError struct{ err error }

// RunCLI is the subprocess protocol: one call per process, the subcommand in
// args[0], the payload on stdin, the answer on stdout. It returns the process
// exit code. Run calls it with the real process streams on native builds; it is
// exported so the protocol can be exercised in-process (and under GOOS=js,
// where the wasm tests run it too).
func RunCLI(h Handler, info Info, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	info = info.withDefaults()
	if len(args) < 1 {
		fmt.Fprintf(stderr, "usage: %s <match|diff|merge|info> [filepath]\n", info.binaryName())
		return 1
	}

	var out any
	var fail *cliError
	switch args[0] {
	case "match":
		// A missing path matches nothing — answered, not an error.
		fmt.Fprintln(stdout, len(args) >= 2 && h.Match(args[1]))
		return 0

	case "diff":
		out, fail = cliDiff(h, stdin)

	case "merge":
		out, fail = cliMerge(h, stdin)

	case "info":
		out = info

	default:
		fmt.Fprintf(stderr, "unknown subcommand: %s\n", args[0])
		return 1
	}

	if fail != nil {
		_ = json.NewEncoder(stderr).Encode(map[string]string{"error": fail.err.Error()})
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		_ = json.NewEncoder(stderr).Encode(map[string]string{"error": err.Error()})
		return 1
	}
	return 0
}

func cliDiff(h Handler, stdin io.Reader) (any, *cliError) {
	var inp diffInput
	if err := json.NewDecoder(stdin).Decode(&inp); err != nil {
		return nil, &cliError{err}
	}
	base, err := decodeBlob("base", inp.Base)
	if err != nil {
		return nil, &cliError{err}
	}
	head, err := decodeBlob("head", inp.Head)
	if err != nil {
		return nil, &cliError{err}
	}
	d, err := h.Diff(base, head)
	if err != nil {
		return nil, &cliError{err}
	}
	return d, nil
}

func cliMerge(h Handler, stdin io.Reader) (any, *cliError) {
	var inp mergeInput
	if err := json.NewDecoder(stdin).Decode(&inp); err != nil {
		return nil, &cliError{err}
	}
	base, err := decodeBlob("base", inp.Base)
	if err != nil {
		return nil, &cliError{err}
	}
	ours, err := decodeBlob("ours", inp.Ours)
	if err != nil {
		return nil, &cliError{err}
	}
	theirs, err := decodeBlob("theirs", inp.Theirs)
	if err != nil {
		return nil, &cliError{err}
	}
	merged, ci, err := h.Merge(base, ours, theirs)
	if err != nil {
		return nil, &cliError{err}
	}
	out := mergeOutput{Blob: base64.StdEncoding.EncodeToString(merged)}
	if ci != nil {
		out.Conflicts = ci.Conflicts
	}
	return out, nil
}

func decodeBlob(side, s string) (Blob, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decoding %s blob: %w", side, err)
	}
	return b, nil
}
