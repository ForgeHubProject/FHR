package fhr

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// stub is a Handler whose answers are fixed, so the tests exercise only the
// protocol plumbing.
type stub struct {
	mergeErr  error
	conflicts *ConflictInfo
	gotBase   []byte
	gotHead   []byte
}

func (s *stub) Match(p string) bool { return strings.HasSuffix(p, ".stub") }

func (s *stub) Diff(base, head Blob) (StructuredDiff, error) {
	s.gotBase, s.gotHead = base, head
	if string(head) == "boom" {
		return StructuredDiff{}, errors.New("parsing head: boom")
	}
	return StructuredDiff{Version: "1.0", Format: "stub", Changes: []DiffChange{
		{Path: "a/b", Kind: Renamed, Label: "b", Before: "x", After: "b"},
	}}, nil
}

func (s *stub) Merge(base, ours, theirs Blob) (Blob, *ConflictInfo, error) {
	if s.mergeErr != nil {
		return nil, nil, s.mergeErr
	}
	return append(append(append(Blob{}, base...), ours...), theirs...), s.conflicts, nil
}

var stubInfo = Info{ID: "stub", Formats: []string{".stub"}}

func run(t *testing.T, h Handler, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	code = RunCLI(h, stubInfo, args, strings.NewReader(stdin), &o, &e)
	return code, o.String(), e.String()
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestUsageAndUnknownSubcommand(t *testing.T) {
	code, out, errOut := run(t, &stub{}, "")
	if code != 1 || out != "" || errOut != "usage: forge-handler-stub <match|diff|merge|info> [filepath]\n" {
		t.Fatalf("no args: code=%d out=%q err=%q", code, out, errOut)
	}
	code, _, errOut = run(t, &stub{}, "", "bogus")
	if code != 1 || errOut != "unknown subcommand: bogus\n" {
		t.Fatalf("bogus: code=%d err=%q", code, errOut)
	}
}

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"match", "x.stub"}, "true\n"},
		{[]string{"match", "x.csv"}, "false\n"},
		{[]string{"match"}, "false\n"}, // a missing path matches nothing
	} {
		code, out, _ := run(t, &stub{}, "", tc.args...)
		if code != 0 || out != tc.want {
			t.Errorf("%v: code=%d out=%q, want %q", tc.args, code, out, tc.want)
		}
	}
}

func TestDiffDecodesBlobsAndEncodesTheDiff(t *testing.T) {
	s := &stub{}
	code, out, _ := run(t, s, `{"base":"`+b64("old")+`","head":"`+b64("new")+`"}`, "diff")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if string(s.gotBase) != "old" || string(s.gotHead) != "new" {
		t.Fatalf("handler got base=%q head=%q", s.gotBase, s.gotHead)
	}
	want := `{"version":"1.0","format":"stub","changes":[{"path":"a/b","kind":"renamed","label":"b","before":"x","after":"b"}]}` + "\n"
	if out != want {
		t.Fatalf("out=%q\nwant %q", out, want)
	}
}

func TestErrorsAreJSONOnStderr(t *testing.T) {
	for _, tc := range []struct {
		name, stdin, sub, want string
	}{
		{"bad base64", `{"base":"!!","head":""}`, "diff", "decoding base blob: illegal base64 data at input byte 0"},
		{"bad head base64", `{"base":"","head":"!!"}`, "diff", "decoding head blob: illegal base64 data at input byte 0"},
		{"handler error", `{"base":"","head":"` + b64("boom") + `"}`, "diff", "parsing head: boom"},
		{"bad theirs", `{"base":"","ours":"","theirs":"!"}`, "merge", "decoding theirs blob: illegal base64 data at input byte 0"},
	} {
		code, out, errOut := run(t, &stub{}, tc.stdin, tc.sub)
		var got map[string]string
		if err := json.Unmarshal([]byte(errOut), &got); err != nil {
			t.Fatalf("%s: stderr is not JSON: %q", tc.name, errOut)
		}
		if code != 1 || out != "" || got["error"] != tc.want {
			t.Errorf("%s: code=%d out=%q error=%q, want %q", tc.name, code, out, got["error"], tc.want)
		}
	}

	code, _, errOut := run(t, &stub{}, "not json", "diff")
	if code != 1 || !strings.Contains(errOut, `"error":"invalid character`) {
		t.Errorf("malformed stdin: code=%d err=%q", code, errOut)
	}
}

func TestMerge(t *testing.T) {
	in := `{"base":"` + b64("a") + `","ours":"` + b64("b") + `","theirs":"` + b64("c") + `"}`

	code, out, _ := run(t, &stub{}, in, "merge")
	if code != 0 || out != `{"blob":"`+b64("abc")+`"}`+"\n" {
		t.Fatalf("clean merge: code=%d out=%q", code, out)
	}

	s := &stub{conflicts: &ConflictInfo{Conflicts: []SemanticConflict{{Path: "p", Ours: 1, Theirs: 2}}}}
	code, out, _ = run(t, s, in, "merge")
	if code != 0 || out != `{"blob":"`+b64("abc")+`","conflicts":[{"path":"p","ours":1,"theirs":2}]}`+"\n" {
		t.Fatalf("conflicted merge: code=%d out=%q", code, out)
	}

	code, _, errOut := run(t, &stub{mergeErr: errors.New("semantic merge is not yet supported for stub")}, in, "merge")
	if code != 1 || errOut != `{"error":"semantic merge is not yet supported for stub"}`+"\n" {
		t.Fatalf("unsupported merge: code=%d err=%q", code, errOut)
	}
}

func TestInfo(t *testing.T) {
	code, out, _ := run(t, &stub{}, "", "info")
	if code != 0 || out != `{"id":"stub","formats":[".stub"],"protocol":"1.0"}`+"\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}

	var o bytes.Buffer
	withCaps := Info{ID: "stub", Formats: []string{".stub"}, Capabilities: &Capabilities{SemanticCompare: true}}
	RunCLI(&stub{}, withCaps, []string{"info"}, strings.NewReader(""), &o, &bytes.Buffer{})
	want := `{"id":"stub","formats":[".stub"],"protocol":"1.0","capabilities":{"semanticCompare":true,"semanticMerge":false}}` + "\n"
	if o.String() != want {
		t.Fatalf("out=%q\nwant %q", o.String(), want)
	}
}

func TestGlobalName(t *testing.T) {
	for id, want := range map[string]string{
		"csv":        "__forgeHandlerCsv",
		"gltf-scene": "__forgeHandlerGltfScene",
		"image-meta": "__forgeHandlerImageMeta",
		"ipynb":      "__forgeHandlerIpynb",
		"obj":        "__forgeHandlerObj",
	} {
		if got := GlobalName(id); got != want {
			t.Errorf("GlobalName(%q) = %q, want %q", id, got, want)
		}
	}
}

// previewStub is a stub that also converts: its preview is the input reversed.
type previewStub struct{ stub }

func (p *previewStub) PreviewMediaType() string { return MediaTypeGLB }

func (p *previewStub) Preview(blob Blob) (Blob, error) {
	if string(blob) == "boom" {
		return nil, errors.New("parsing: boom")
	}
	out := make(Blob, len(blob))
	for i, b := range blob {
		out[len(blob)-1-i] = b
	}
	return out, nil
}

func TestPreview(t *testing.T) {
	p := &previewStub{}

	code, out, _ := run(t, p, `{"blob":"`+b64("abc")+`"}`, "preview")
	if code != 0 || out != `{"mediaType":"model/gltf-binary","blob":"`+b64("cba")+`"}`+"\n" {
		t.Fatalf("preview: code=%d out=%q", code, out)
	}

	code, _, errOut := run(t, p, `{"blob":"`+b64("boom")+`"}`, "preview")
	if code != 1 || errOut != `{"error":"parsing: boom"}`+"\n" {
		t.Fatalf("preview error: code=%d err=%q", code, errOut)
	}

	// info and usage advertise preview only for a Previewer — declared from the
	// implementation, so a stale Info cannot claim one the handler lacks.
	code, out, _ = run(t, p, "", "info")
	if code != 0 || out != `{"id":"stub","formats":[".stub"],"protocol":"1.0","preview":"model/gltf-binary"}`+"\n" {
		t.Fatalf("info: code=%d out=%q", code, out)
	}
	_, _, errOut = run(t, p, "")
	if errOut != "usage: forge-handler-stub <match|diff|merge|preview|info> [filepath]\n" {
		t.Fatalf("usage: %q", errOut)
	}
	var o bytes.Buffer
	lying := Info{ID: "stub", Formats: []string{".stub"}, Preview: MediaTypeGLB}
	RunCLI(&stub{}, lying, []string{"info"}, strings.NewReader(""), &o, &bytes.Buffer{})
	if strings.Contains(o.String(), "preview") {
		t.Fatalf("a handler without Preview must not declare one: %q", o.String())
	}

	code, _, errOut = run(t, &stub{}, `{"blob":""}`, "preview")
	if code != 1 || errOut != `{"error":"stub has no preview: the format renders from its own bytes"}`+"\n" {
		t.Fatalf("no preview: code=%d err=%q", code, errOut)
	}
}
