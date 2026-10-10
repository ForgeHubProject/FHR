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

type exportInput struct {
	Blob   string `json:"blob"`   // base64-encoded GLB
	Format string `json:"format"` // target extension, e.g. ".obj"
}

type applyChoicesInput struct {
	Merged string   `json:"merged"` // base64: the merge's result, ours at every conflict
	Theirs string   `json:"theirs"` // base64
	Take   []string `json:"take"`   // conflict paths to take from theirs
}

type previewInput struct {
	Blob string `json:"blob"` // base64-encoded blob
}

type previewOutput struct {
	MediaType string `json:"mediaType"`
	Blob      string `json:"blob"` // base64-encoded preview
}

// cliError is a failure the protocol reports on stderr as {"error": "..."}.
type cliError struct{ err error }

// RunCLI is the subprocess protocol: one call per process, the subcommand in
// args[0], the payload on stdin, the answer on stdout. It returns the process
// exit code. Run calls it with the real process streams on native builds; it is
// exported so the protocol can be exercised in-process (and under GOOS=js,
// where the wasm tests run it too).
func RunCLI(h Handler, info Info, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	info = info.withDefaults(h)
	if len(args) < 1 {
		fmt.Fprintf(stderr, "usage: %s <match|diff|merge|%s%s%sinfo> [filepath]\n", info.binaryName(), applyUsage(h), previewUsage(info), transcodeUsage(h))
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

	case "preview":
		out, fail = cliPreview(h, info, stdin)

	case "apply-choices":
		out, fail = cliApplyChoices(h, info, stdin)

	case "import":
		out, fail = cliImport(h, info, stdin)

	case "export":
		out, fail = cliExport(h, info, stdin)

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

func cliPreview(h Handler, info Info, stdin io.Reader) (any, *cliError) {
	p, ok := h.(Previewer)
	if !ok {
		return nil, &cliError{errNoPreview(info)}
	}
	var inp previewInput
	if err := json.NewDecoder(stdin).Decode(&inp); err != nil {
		return nil, &cliError{err}
	}
	blob, err := decodeBlob("input", inp.Blob)
	if err != nil {
		return nil, &cliError{err}
	}
	out, err := p.Preview(blob)
	if err != nil {
		return nil, &cliError{err}
	}
	return previewOutput{MediaType: p.PreviewMediaType(), Blob: base64.StdEncoding.EncodeToString(out)}, nil
}

func cliApplyChoices(h Handler, info Info, stdin io.Reader) (any, *cliError) {
	a, ok := h.(ChoiceApplier)
	if !ok {
		return nil, &cliError{fmt.Errorf("%s cannot apply conflict choices", info.ID)}
	}
	var inp applyChoicesInput
	if err := json.NewDecoder(stdin).Decode(&inp); err != nil {
		return nil, &cliError{err}
	}
	merged, err := decodeBlob("merged", inp.Merged)
	if err != nil {
		return nil, &cliError{err}
	}
	theirs, err := decodeBlob("theirs", inp.Theirs)
	if err != nil {
		return nil, &cliError{err}
	}
	out, err := a.ApplyChoices(merged, theirs, inp.Take)
	if err != nil {
		return nil, &cliError{err}
	}
	return struct {
		Blob string `json:"blob"`
	}{base64.StdEncoding.EncodeToString(out)}, nil
}

func cliImport(h Handler, info Info, stdin io.Reader) (any, *cliError) {
	i, ok := h.(Importer)
	if !ok {
		return nil, &cliError{fmt.Errorf("%s cannot import to glTF", info.ID)}
	}
	var inp previewInput
	if err := json.NewDecoder(stdin).Decode(&inp); err != nil {
		return nil, &cliError{err}
	}
	blob, err := decodeBlob("input", inp.Blob)
	if err != nil {
		return nil, &cliError{err}
	}
	out, err := i.Import(blob)
	if err != nil {
		return nil, &cliError{err}
	}
	return previewOutput{MediaType: MediaTypeGLB, Blob: base64.StdEncoding.EncodeToString(out)}, nil
}

func cliExport(h Handler, info Info, stdin io.Reader) (any, *cliError) {
	e, ok := h.(Exporter)
	if !ok {
		return nil, &cliError{fmt.Errorf("%s cannot export from glTF", info.ID)}
	}
	var inp exportInput
	if err := json.NewDecoder(stdin).Decode(&inp); err != nil {
		return nil, &cliError{err}
	}
	glb, err := decodeBlob("input", inp.Blob)
	if err != nil {
		return nil, &cliError{err}
	}
	format, err := exportFormat(info, inp.Format)
	if err != nil {
		return nil, &cliError{err}
	}
	out, err := e.Export(glb, format)
	if err != nil {
		return nil, &cliError{err}
	}
	return struct {
		Blob string `json:"blob"`
	}{base64.StdEncoding.EncodeToString(out)}, nil
}

// exportFormat resolves the requested target against the handler's formats.
// An empty request is the handler's only format, or an error when it has
// several — never a silent pick.
func exportFormat(info Info, want string) (string, error) {
	if want == "" {
		if len(info.Formats) == 1 {
			return info.Formats[0], nil
		}
		return "", fmt.Errorf("%s exports several formats (%v): say which", info.ID, info.Formats)
	}
	for _, f := range info.Formats {
		if f == want {
			return f, nil
		}
	}
	return "", fmt.Errorf("%s does not export %q (formats: %v)", info.ID, want, info.Formats)
}

// transcodeUsage lists import/export only for handlers that have them.
func transcodeUsage(h Handler) string {
	var s string
	if _, ok := h.(Importer); ok {
		s += "import|"
	}
	if _, ok := h.(Exporter); ok {
		s += "export|"
	}
	return s
}

func errNoPreview(info Info) error {
	return fmt.Errorf("%s has no preview: the format renders from its own bytes", info.ID)
}

// applyUsage lists the apply-choices subcommand only for handlers that have it.
func applyUsage(h Handler) string {
	if _, ok := h.(ChoiceApplier); ok {
		return "apply-choices|"
	}
	return ""
}

// previewUsage lists the preview subcommand only for handlers that have one.
func previewUsage(info Info) string {
	if info.Preview == "" {
		return ""
	}
	return "preview|"
}

func decodeBlob(side, s string) (Blob, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decoding %s blob: %w", side, err)
	}
	return b, nil
}
