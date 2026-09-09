//go:build js && wasm

// WebAssembly entry point for the ipynb handler. Built with `GOOS=js
// GOARCH=wasm`, it exposes the same diff logic as the native subprocess binary
// so a diff can be computed client-side (SPEC-RENDERING.md §4, Tier B) with no
// producer/consumer skew.
package main

import (
	"encoding/base64"
	"encoding/json"
	"syscall/js"
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("diff", js.FuncOf(wasmDiff))
	api.Set("merge", js.FuncOf(wasmMerge))
	api.Set("info", js.FuncOf(wasmInfo))
	js.Global().Set("__forgeHandlerIpynb", api)
	select {}
}

func bytesFromArg(v js.Value) []byte {
	n := v.Get("length").Int()
	b := make([]byte, n)
	js.CopyBytesToGo(b, v)
	return b
}

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

func wasmDiff(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return `{"error":"diff(base, head) requires two Uint8Array arguments"}`
	}
	h := &Handler{}
	d, err := h.Diff(bytesFromArg(args[0]), bytesFromArg(args[1]))
	if err != nil {
		return jsError(err)
	}
	return jsResult(d)
}

func wasmMerge(_ js.Value, args []js.Value) any {
	if len(args) < 3 {
		return `{"error":"merge(base, ours, theirs) requires three Uint8Array arguments"}`
	}
	h := &Handler{}
	merged, ci, err := h.Merge(bytesFromArg(args[0]), bytesFromArg(args[1]), bytesFromArg(args[2]))
	if err != nil {
		return jsError(err)
	}
	out := map[string]any{"blob": base64.StdEncoding.EncodeToString(merged)}
	if ci != nil {
		out["conflicts"] = ci.Conflicts
	}
	return jsResult(out)
}

func wasmInfo(_ js.Value, _ []js.Value) any {
	return jsResult(map[string]any{
		"id":       handlerID,
		"formats":  []string{".ipynb"},
		"protocol": protocolVer,
	})
}
