package fhr

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// fhr-wasm-1 (SPEC §7): the language-neutral wasm contract. A host writes one
// request into the module's memory and gets one response back, both shaped
//
//	[u32 header length, little-endian][JSON header][blobs, back to back]
//
// The request header names the call and the length of each blob that follows,
// plus the call's own parameters; the response header holds {"result": …} or
// {"error": "…"}, and "blobs" when the result carries files. The calls and
// their results are the subprocess protocol's, except that blobs travel as raw
// bytes — never base64 — and so never leave the result JSON.
//
// Call is the whole contract on the Go side. It has no build tags, so it is
// tested natively; run_wasip1.go only moves bytes in and out of memory.

// WasmABI names this contract, as the manifest and the spec do.
const WasmABI = "fhr-wasm-1"

type callRequest struct {
	Op    string `json:"op"`
	Blobs []int  `json:"blobs"`
	// Path is match's file path.
	Path string `json:"path,omitempty"`
	// Format is export's target extension; empty follows the subprocess rule.
	Format string `json:"format,omitempty"`
	// Take is applyChoices' conflict paths to take from theirs.
	Take []string `json:"take,omitempty"`
}

type callResponse struct {
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	Blobs  []int  `json:"blobs,omitempty"`
}

// Call answers one fhr-wasm-1 request. It never panics: a malformed request,
// a call the handler does not implement and a panic inside the handler all
// come back as {"error": …}, so a host's module stays usable.
func Call(h Handler, info Info, req []byte) (resp []byte) {
	info = info.withDefaults(h)
	defer func() {
		if r := recover(); r != nil {
			resp = encodeResponse(callResponse{Error: fmt.Sprintf("handler panicked: %v", r)}, nil)
		}
	}()
	r, blobs, err := decodeRequest(req)
	if err != nil {
		return encodeResponse(callResponse{Error: err.Error()}, nil)
	}
	result, out, err := dispatch(h, info, r, blobs)
	if err != nil {
		return encodeResponse(callResponse{Error: err.Error()}, nil)
	}
	return encodeResponse(callResponse{Result: result}, out)
}

// blobsFor checks the request carries exactly the blobs a call takes.
func blobsFor(op string, blobs []Blob, names ...string) error {
	if len(blobs) != len(names) {
		return fmt.Errorf("%s takes %d blob(s) (%v), got %d", op, len(names), names, len(blobs))
	}
	return nil
}

func dispatch(h Handler, info Info, r callRequest, blobs []Blob) (any, []Blob, error) {
	switch r.Op {
	case "info":
		return info, nil, nil
	case "match":
		return h.Match(r.Path), nil, nil
	case "diff":
		if err := blobsFor(r.Op, blobs, "base", "head"); err != nil {
			return nil, nil, err
		}
		d, err := h.Diff(blobs[0], blobs[1])
		return d, nil, err
	case "merge":
		if err := blobsFor(r.Op, blobs, "base", "ours", "theirs"); err != nil {
			return nil, nil, err
		}
		merged, ci, err := h.Merge(blobs[0], blobs[1], blobs[2])
		if err != nil {
			return nil, nil, err
		}
		out := struct {
			Conflicts []SemanticConflict `json:"conflicts,omitempty"`
		}{}
		if ci != nil {
			out.Conflicts = ci.Conflicts
		}
		return out, []Blob{merged}, nil
	case "preview":
		p, ok := h.(Previewer)
		if !ok {
			return nil, nil, errNoPreview(info)
		}
		if err := blobsFor(r.Op, blobs, "blob"); err != nil {
			return nil, nil, err
		}
		out, err := p.Preview(blobs[0])
		if err != nil {
			return nil, nil, err
		}
		return mediaResult{MediaType: p.PreviewMediaType()}, []Blob{out}, nil
	case "applyChoices":
		a, ok := h.(ChoiceApplier)
		if !ok {
			return nil, nil, fmt.Errorf("%s cannot apply conflict choices", info.ID)
		}
		if err := blobsFor(r.Op, blobs, "merged", "theirs"); err != nil {
			return nil, nil, err
		}
		out, err := a.ApplyChoices(blobs[0], blobs[1], r.Take)
		if err != nil {
			return nil, nil, err
		}
		return struct{}{}, []Blob{out}, nil
	case "import":
		i, ok := h.(Importer)
		if !ok {
			return nil, nil, fmt.Errorf("%s cannot import to glTF", info.ID)
		}
		if err := blobsFor(r.Op, blobs, "blob"); err != nil {
			return nil, nil, err
		}
		out, err := i.Import(blobs[0])
		if err != nil {
			return nil, nil, err
		}
		return mediaResult{MediaType: MediaTypeGLB}, []Blob{out}, nil
	case "export":
		e, ok := h.(Exporter)
		if !ok {
			return nil, nil, fmt.Errorf("%s cannot export from glTF", info.ID)
		}
		if err := blobsFor(r.Op, blobs, "glb"); err != nil {
			return nil, nil, err
		}
		format, err := exportFormat(info, r.Format)
		if err != nil {
			return nil, nil, err
		}
		out, err := e.Export(blobs[0], format)
		if err != nil {
			return nil, nil, err
		}
		return struct{}{}, []Blob{out}, nil
	}
	return nil, nil, fmt.Errorf("unknown call %q", r.Op)
}

type mediaResult struct {
	MediaType string `json:"mediaType"`
}

// decodeRequest splits a request into its header and blobs, checking every
// length against what is actually there.
func decodeRequest(req []byte) (callRequest, []Blob, error) {
	var r callRequest
	if len(req) < 4 {
		return r, nil, fmt.Errorf("request too short: %d bytes", len(req))
	}
	n := uint64(binary.LittleEndian.Uint32(req))
	if n > uint64(len(req)-4) {
		return r, nil, fmt.Errorf("request header of %d bytes overruns the request", n)
	}
	if err := json.Unmarshal(req[4:4+n], &r); err != nil {
		return r, nil, fmt.Errorf("request header: %w", err)
	}
	at := 4 + n
	blobs := make([]Blob, 0, len(r.Blobs))
	for i, l := range r.Blobs {
		if l < 0 || uint64(l) > uint64(len(req))-at {
			return r, nil, fmt.Errorf("blob %d (%d bytes) overruns the request", i, l)
		}
		blobs = append(blobs, req[at:at+uint64(l)])
		at += uint64(l)
	}
	if at != uint64(len(req)) {
		return r, nil, fmt.Errorf("%d bytes after the last blob", uint64(len(req))-at)
	}
	return r, blobs, nil
}

// encodeResponse writes a response: its header, then the blobs.
func encodeResponse(r callResponse, blobs []Blob) []byte {
	size := 0
	for _, b := range blobs {
		r.Blobs = append(r.Blobs, len(b))
		size += len(b)
	}
	header, err := json.Marshal(r)
	if err != nil {
		header, _ = json.Marshal(callResponse{Error: "encoding the response: " + err.Error()})
		blobs, size = nil, 0
	}
	out := make([]byte, 0, 4+len(header)+size)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(header)))
	out = append(out, header...)
	for _, b := range blobs {
		out = append(out, b...)
	}
	return out
}

// EncodeRequest builds an fhr-wasm-1 request: a header with the call and its
// parameters, then the blobs. Hosts written in Go (and the tests) use it.
func EncodeRequest(op string, params map[string]any, blobs ...Blob) ([]byte, error) {
	header := map[string]any{"op": op}
	for k, v := range params {
		header[k] = v
	}
	lens := make([]int, len(blobs))
	size := 0
	for i, b := range blobs {
		lens[i] = len(b)
		size += len(b)
	}
	header["blobs"] = lens
	h, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 4+len(h)+size)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(h)))
	out = append(out, h...)
	for _, b := range blobs {
		out = append(out, b...)
	}
	return out, nil
}

// DecodeResponse splits a response into its header (result JSON or error)
// and blobs. Hosts written in Go (and the tests) use it.
func DecodeResponse(resp []byte) (result json.RawMessage, blobs []Blob, err error) {
	if len(resp) < 4 {
		return nil, nil, fmt.Errorf("response too short: %d bytes", len(resp))
	}
	n := uint64(binary.LittleEndian.Uint32(resp))
	if n > uint64(len(resp)-4) {
		return nil, nil, fmt.Errorf("response header of %d bytes overruns the response", n)
	}
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
		Blobs  []int           `json:"blobs"`
	}
	if err := json.Unmarshal(resp[4:4+n], &r); err != nil {
		return nil, nil, fmt.Errorf("response header: %w", err)
	}
	if r.Error != "" {
		return nil, nil, fmt.Errorf("%s", r.Error)
	}
	at := 4 + n
	for i, l := range r.Blobs {
		if l < 0 || uint64(l) > uint64(len(resp))-at {
			return nil, nil, fmt.Errorf("response blob %d (%d bytes) overruns the response", i, l)
		}
		blobs = append(blobs, resp[at:at+uint64(l)])
		at += uint64(l)
	}
	return r.Result, blobs, nil
}
