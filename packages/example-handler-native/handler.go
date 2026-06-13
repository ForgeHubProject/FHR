// Example FHR backend handler written in Go.
// Any language that can read stdin and write stdout works — this is just a reference.
//
// Build:
//   GOOS=linux GOARCH=amd64 go build -o forge-handler-example_linux-amd64 .
//   GOOS=wasip1 GOARCH=wasm go build -o forge-handler-example.wasm .

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

// ─── Wire types (must match @fhr/types) ──────────────────────────────────────

type StructuredDiff struct {
	Version string        `json:"version"`
	Format  string        `json:"format"`
	Changes []DiffChange  `json:"changes"`
}

type DiffChange struct {
	Path     string        `json:"path"`
	Kind     string        `json:"kind"` // "added" | "removed" | "modified"
	Label    string        `json:"label,omitempty"`
	Before   interface{}   `json:"before,omitempty"`
	After    interface{}   `json:"after,omitempty"`
	Children []DiffChange  `json:"children,omitempty"`
}

type SemanticConflict struct {
	Path   string      `json:"path"`
	Ours   interface{} `json:"ours"`
	Theirs interface{} `json:"theirs"`
}

type DiffInput struct {
	Base string `json:"base"` // base64-encoded blob
	Head string `json:"head"` // base64-encoded blob
}

type MergeInput struct {
	Base   string `json:"base"`
	Ours   string `json:"ours"`
	Theirs string `json:"theirs"`
}

type MergeOutput struct {
	Blob      string             `json:"blob"`      // base64-encoded result
	Conflicts []SemanticConflict `json:"conflicts,omitempty"`
}

type HandlerInfo struct {
	ID       string   `json:"id"`
	Version  string   `json:"version"`
	Formats  []string `json:"formats"`
	Protocol string   `json:"protocol"`
}

// ─── Main dispatch ────────────────────────────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: forge-handler-example <match|diff|merge|info> [filepath]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "match":
		cmdMatch()
	case "diff":
		cmdDiff()
	case "merge":
		cmdMerge()
	case "info":
		cmdInfo()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(1)
	}
}

// ─── Subcommands ─────────────────────────────────────────────────────────────

func cmdMatch() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stdout, "false")
		return
	}
	path := os.Args[2]
	// TODO: replace with your extension check
	if len(path) > 8 && path[len(path)-8:] == ".example" {
		fmt.Fprintln(os.Stdout, "true")
	} else {
		fmt.Fprintln(os.Stdout, "false")
	}
}

func cmdDiff() {
	var input DiffInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fmt.Fprintf(os.Stderr, `{"error":%q}`, err.Error())
		os.Exit(1)
	}

	baseBlob, err := base64.StdEncoding.DecodeString(input.Base)
	if err != nil {
		fmt.Fprintf(os.Stderr, `{"error":"invalid base64 in base"}\n`)
		os.Exit(1)
	}
	headBlob, err := base64.StdEncoding.DecodeString(input.Head)
	if err != nil {
		fmt.Fprintf(os.Stderr, `{"error":"invalid base64 in head"}\n`)
		os.Exit(1)
	}

	// TODO: implement your semantic diff logic here
	diff := computeDiff(baseBlob, headBlob)

	if err := json.NewEncoder(os.Stdout).Encode(diff); err != nil {
		fmt.Fprintf(os.Stderr, `{"error":%q}`, err.Error())
		os.Exit(1)
	}
}

func cmdMerge() {
	var input MergeInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fmt.Fprintf(os.Stderr, `{"error":%q}`, err.Error())
		os.Exit(1)
	}

	// Decode blobs
	_, _ = base64.StdEncoding.DecodeString(input.Base)
	oursBlob, _ := base64.StdEncoding.DecodeString(input.Ours)
	_, _ = base64.StdEncoding.DecodeString(input.Theirs)

	// TODO: implement your 3-way merge logic here.
	// Return conflicts for any paths that couldn't be reconciled.
	result := MergeOutput{
		Blob:      base64.StdEncoding.EncodeToString(oursBlob), // placeholder: take ours
		Conflicts: []SemanticConflict{},
	}

	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, `{"error":%q}`, err.Error())
		os.Exit(1)
	}
}

func cmdInfo() {
	info := HandlerInfo{
		ID:       "example",
		Version:  "0.0.1",
		Formats:  []string{".example"},
		Protocol: "1.0",
	}
	_ = json.NewEncoder(os.Stdout).Encode(info)
}

// ─── Your logic goes here ─────────────────────────────────────────────────────

func computeDiff(base, head []byte) StructuredDiff {
	// Replace this with real semantic diffing for your format.
	_ = base
	_ = head
	return StructuredDiff{
		Version: "1.0",
		Format:  "example",
		Changes: []DiffChange{},
	}
}
