package fhr

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fhr-wasm-1 through Call: the subprocess protocol's calls and answers, with
// blobs as raw bytes. The stubs are cli_test.go's, so both transports are held
// to the same handler behaviour.

func call(t *testing.T, h Handler, info Info, op string, params map[string]any, blobs ...Blob) (json.RawMessage, []Blob, error) {
	t.Helper()
	req, err := EncodeRequest(op, params, blobs...)
	if err != nil {
		t.Fatal(err)
	}
	return DecodeResponse(Call(h, info, req))
}

func TestCallDiffAnswersAsTheSubprocessDoes(t *testing.T) {
	s := &stub{}
	result, blobs, err := call(t, s, stubInfo, "diff", nil, Blob("old"), Blob("new"))
	if err != nil || len(blobs) != 0 {
		t.Fatalf("diff: err=%v blobs=%d", err, len(blobs))
	}
	if string(s.gotBase) != "old" || string(s.gotHead) != "new" {
		t.Fatalf("the handler got %q, %q", s.gotBase, s.gotHead)
	}
	_, out, _ := run(t, &stub{}, `{"base":"`+b64("old")+`","head":"`+b64("new")+`"}`, "diff")
	if string(result)+"\n" != out {
		t.Fatalf("diff differs from the subprocess answer:\n wasm: %s\n  cli: %s", result, out)
	}
	_, _, err = call(t, s, stubInfo, "diff", nil, Blob("old"), Blob("boom"))
	if err == nil || err.Error() != "parsing head: boom" {
		t.Fatalf("a handler error comes back as the error: %v", err)
	}
}

func TestCallInfoDeclaresFromTheImplementation(t *testing.T) {
	for _, h := range []Handler{&stub{}, &previewStub{}} {
		result, _, err := call(t, h, stubInfo, "info", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, out, _ := run(t, h, "", "info")
		if string(result)+"\n" != out {
			t.Fatalf("info differs from the subprocess answer:\n wasm: %s\n  cli: %s", result, out)
		}
	}
}

func TestCallMatch(t *testing.T) {
	for path, want := range map[string]string{"a/b.stub": "true", "a/b.txt": "false"} {
		result, _, err := call(t, &stub{}, stubInfo, "match", map[string]any{"path": path})
		if err != nil || string(result) != want {
			t.Fatalf("match %s: %s err=%v", path, result, err)
		}
	}
}

func TestCallMergeReturnsTheBlobRaw(t *testing.T) {
	result, blobs, err := call(t, &stub{}, stubInfo, "merge", nil, Blob("b"), Blob("o"), Blob("t"))
	if err != nil || string(result) != `{}` || len(blobs) != 1 || string(blobs[0]) != "bot" {
		t.Fatalf("clean merge: %s %q err=%v", result, blobs, err)
	}
	s := &stub{conflicts: &ConflictInfo{Conflicts: []SemanticConflict{{Path: "nodes/A", Ours: "left", Theirs: "right"}}}}
	result, _, err = call(t, s, stubInfo, "merge", nil, Blob("b"), Blob("o"), Blob("t"))
	if err != nil || !strings.Contains(string(result), `"path":"nodes/A"`) {
		t.Fatalf("conflicting merge: %s err=%v", result, err)
	}
}

func TestCallPreview(t *testing.T) {
	result, blobs, err := call(t, &previewStub{}, stubInfo, "preview", nil, Blob("abc"))
	if err != nil || string(result) != `{"mediaType":"model/gltf-binary"}` || len(blobs) != 1 || string(blobs[0]) != "cba" {
		t.Fatalf("preview: %s %q err=%v", result, blobs, err)
	}
	_, _, err = call(t, &stub{}, stubInfo, "preview", nil, Blob("abc"))
	if err == nil || err.Error() != "stub has no preview: the format renders from its own bytes" {
		t.Fatalf("no preview: %v", err)
	}
}

func TestCallApplyChoices(t *testing.T) {
	_, blobs, err := call(t, &applierStub{}, stubInfo, "applyChoices", map[string]any{"take": []string{"nodes/A"}}, Blob("m"), Blob("t"))
	if err != nil || len(blobs) != 1 || string(blobs[0]) != "m+t" {
		t.Fatalf("applyChoices: %q err=%v", blobs, err)
	}
	_, _, err = call(t, &applierStub{}, stubInfo, "applyChoices", map[string]any{"take": []string{"nodes/B"}}, Blob("m"), Blob("t"))
	if err == nil || !strings.Contains(err.Error(), "unknown conflict path nodes/B") {
		t.Fatalf("unknown path: %v", err)
	}
	_, _, err = call(t, &stub{}, stubInfo, "applyChoices", nil, Blob("m"), Blob("t"))
	if err == nil || err.Error() != "stub cannot apply conflict choices" {
		t.Fatalf("not an applier: %v", err)
	}
}

func TestCallImportExport(t *testing.T) {
	result, blobs, err := call(t, &xcode{}, stubInfo, "import", nil, Blob("abc"))
	if err != nil || string(result) != `{"mediaType":"model/gltf-binary"}` || string(blobs[0]) != "ABC" {
		t.Fatalf("import: %s %q err=%v", result, blobs, err)
	}
	_, blobs, err = call(t, &xcode{}, stubInfo, "export", map[string]any{"format": ".stub"}, Blob("ABC"))
	if err != nil || string(blobs[0]) != "abc.stub" {
		t.Fatalf("export: %q err=%v", blobs, err)
	}
	// No format: the handler's only one, as the subprocess protocol resolves it.
	_, blobs, err = call(t, &xcode{}, stubInfo, "export", nil, Blob("ABC"))
	if err != nil || string(blobs[0]) != "abc.stub" {
		t.Fatalf("export without a format: %q err=%v", blobs, err)
	}
	_, _, err = call(t, &stub{}, stubInfo, "import", nil, Blob("abc"))
	if err == nil || err.Error() != "stub cannot import to glTF" {
		t.Fatalf("not an importer: %v", err)
	}
}

func TestCallBlobsTravelRawAndLarge(t *testing.T) {
	// 9 MiB holding every byte value: past the old 8 MiB wasm cap, and nothing
	// on the way is text (no base64, no JSON).
	in := make(Blob, 9<<20)
	for i := range in {
		in[i] = byte(i)
	}
	_, blobs, err := call(t, &previewStub{}, stubInfo, "preview", nil, in)
	if err != nil || len(blobs[0]) != len(in) {
		t.Fatalf("large preview: err=%v len=%d", err, len(blobs))
	}
	for i := range in {
		if blobs[0][i] != in[len(in)-1-i] {
			t.Fatalf("byte %d came back wrong", i)
		}
	}
}

func TestCallRefusesMalformedRequests(t *testing.T) {
	header := func(h string) []byte {
		return append(binary.LittleEndian.AppendUint32(nil, uint32(len(h))), h...)
	}
	cases := map[string]struct {
		req  []byte
		want string
	}{
		"too short":             {[]byte{1, 0}, "request too short"},
		"header overruns":       {append(binary.LittleEndian.AppendUint32(nil, 99), '{', '}'), "overruns the request"},
		"header is not json":    {header("nope"), "request header"},
		"blob overruns":         {header(`{"op":"diff","blobs":[3,10]}`), "blob 0 (3 bytes) overruns"},
		"bytes after the blobs": {append(header(`{"op":"info","blobs":[]}`), 'x'), "1 bytes after the last blob"},
		"negative length":       {header(`{"op":"diff","blobs":[-1]}`), "blob 0 (-1 bytes) overruns"},
		"wrong blob count":      {append(header(`{"op":"diff","blobs":[1]}`), 'a'), "diff takes 2 blob(s)"},
		"unknown call":          {header(`{"op":"frobnicate","blobs":[]}`), `unknown call "frobnicate"`},
	}
	for name, c := range cases {
		_, _, err := DecodeResponse(Call(&stub{}, stubInfo, c.req))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", name, err, c.want)
		}
	}
}

// panicker's Diff panics, as a handler bug might.
type panicker struct{ stub }

func (panicker) Diff(_, _ Blob) (StructuredDiff, error) { panic(errors.New("index out of range")) }

func TestCallTurnsAHandlerPanicIntoAnError(t *testing.T) {
	_, _, err := call(t, &panicker{}, stubInfo, "diff", nil, Blob("a"), Blob("b"))
	if err == nil || err.Error() != "handler panicked: index out of range" {
		t.Fatalf("panic: %v", err)
	}
	// The module stays usable after it.
	if _, _, err := call(t, &panicker{}, stubInfo, "info", nil); err != nil {
		t.Fatalf("after a panic: %v", err)
	}
}
