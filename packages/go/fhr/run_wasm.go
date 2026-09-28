//go:build js && wasm

package fhr

import (
	"encoding/base64"
	"encoding/json"
	"syscall/js"
)

// Run registers the handler's api as a JS global (GlobalName) and keeps the Go
// runtime alive so the exported callbacks remain invokable. Every call takes
// Uint8Arrays and answers a JSON string — {"error": "..."} on failure — which is
// the contract ForgeHub's wasm-worker.cjs and browserWasm.ts parse.
func Run(h Handler, info Info) {
	info = info.withDefaults()
	api := js.Global().Get("Object").New()
	api.Set("diff", js.FuncOf(func(_ js.Value, args []js.Value) any { return wasmDiff(h, args) }))
	api.Set("merge", js.FuncOf(func(_ js.Value, args []js.Value) any { return wasmMerge(h, args) }))
	api.Set("info", js.FuncOf(func(_ js.Value, _ []js.Value) any { return jsResult(info) }))
	js.Global().Set(GlobalName(info.ID), api)
	select {}
}

// bytesFromArg copies a JS Uint8Array argument into a Go byte slice.
func bytesFromArg(v js.Value) []byte {
	n := v.Get("length").Int()
	b := make([]byte, n)
	js.CopyBytesToGo(b, v)
	return b
}

// jsResult marshals v to a JSON string (JS side does JSON.parse); on failure
// it returns a JSON error object so callers always get parseable JSON.
func jsResult(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return jsError(err)
	}
	return string(data)
}

func jsError(err error) any {
	data, _ := json.Marshal(map[string]string{"error": err.Error()})
	return string(data)
}

// diff(base, head): two Uint8Arrays → StructuredDiff JSON string.
func wasmDiff(h Handler, args []js.Value) any {
	if len(args) < 2 {
		return `{"error":"diff(base, head) requires two Uint8Array arguments"}`
	}
	d, err := h.Diff(bytesFromArg(args[0]), bytesFromArg(args[1]))
	if err != nil {
		return jsError(err)
	}
	return jsResult(d)
}

// merge(base, ours, theirs): three Uint8Arrays → {blob: base64, conflicts?}.
func wasmMerge(h Handler, args []js.Value) any {
	if len(args) < 3 {
		return `{"error":"merge(base, ours, theirs) requires three Uint8Array arguments"}`
	}
	merged, ci, err := h.Merge(bytesFromArg(args[0]), bytesFromArg(args[1]), bytesFromArg(args[2]))
	if err != nil {
		return jsError(err)
	}
	out := mergeOutput{Blob: base64.StdEncoding.EncodeToString(merged)}
	if ci != nil {
		out.Conflicts = ci.Conflicts
	}
	return jsResult(out)
}
