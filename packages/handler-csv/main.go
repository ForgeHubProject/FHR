//go:build !js

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

// ── stdin/stdout message shapes ────────────────────────────────────────────

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

type infoOutput struct {
	ID       string   `json:"id"`
	Formats  []string `json:"formats"`
	Protocol string   `json:"protocol"`
}

// ── main: subprocess protocol dispatch ─────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: forge-handler-csv <match|diff|merge|info> [filepath]")
		os.Exit(1)
	}

	h := &Handler{}

	switch os.Args[1] {
	case "match":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stdout, "false")
			return
		}
		if h.Match(os.Args[2]) {
			fmt.Fprintln(os.Stdout, "true")
		} else {
			fmt.Fprintln(os.Stdout, "false")
		}

	case "diff":
		var inp diffInput
		if err := json.NewDecoder(os.Stdin).Decode(&inp); err != nil {
			fatalErr(err)
		}
		baseBlob, err := base64.StdEncoding.DecodeString(inp.Base)
		if err != nil {
			fatalErr(fmt.Errorf("decoding base blob: %w", err))
		}
		headBlob, err := base64.StdEncoding.DecodeString(inp.Head)
		if err != nil {
			fatalErr(fmt.Errorf("decoding head blob: %w", err))
		}
		diff, err := h.Diff(baseBlob, headBlob)
		if err != nil {
			fatalErr(err)
		}
		mustEncode(diff)

	case "merge":
		var inp mergeInput
		if err := json.NewDecoder(os.Stdin).Decode(&inp); err != nil {
			fatalErr(err)
		}
		baseBlob, err := base64.StdEncoding.DecodeString(inp.Base)
		if err != nil {
			fatalErr(fmt.Errorf("decoding base blob: %w", err))
		}
		oursBlob, err := base64.StdEncoding.DecodeString(inp.Ours)
		if err != nil {
			fatalErr(fmt.Errorf("decoding ours blob: %w", err))
		}
		theirsBlob, err := base64.StdEncoding.DecodeString(inp.Theirs)
		if err != nil {
			fatalErr(fmt.Errorf("decoding theirs blob: %w", err))
		}
		merged, ci, err := h.Merge(baseBlob, oursBlob, theirsBlob)
		if err != nil {
			fatalErr(err)
		}
		out := mergeOutput{Blob: base64.StdEncoding.EncodeToString(merged)}
		if ci != nil {
			out.Conflicts = ci.Conflicts
		}
		mustEncode(out)

	case "info":
		mustEncode(infoOutput{
			ID:       handlerID,
			Formats:  []string{".csv"},
			Protocol: protocolVer,
		})

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func mustEncode(v any) {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		fatalErr(err)
	}
}

func fatalErr(err error) {
	_ = json.NewEncoder(os.Stderr).Encode(map[string]string{"error": err.Error()})
	os.Exit(1)
}
