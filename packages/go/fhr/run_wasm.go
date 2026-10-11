//go:build js && wasm

package fhr

import (
	"encoding/base64"
	"encoding/json"
	"syscall/js"
)

// Run registers the handler's api as a JS global (GlobalName) and keeps the Go
// runtime alive so the exported callbacks remain invokable. diff, merge and
// info take Uint8Arrays and answer a JSON string — {"error": "..."} on failure —
// which is the contract ForgeHub's wasm-worker.cjs and browserWasm.ts parse.
// preview, registered only for a Previewer, answers an object instead (see
// wasmPreview), so a large preview never round-trips through base64 and JSON.
func Run(h Handler, info Info) {
	info = info.withDefaults(h)
	api := js.Global().Get("Object").New()
	api.Set("diff", js.FuncOf(func(_ js.Value, args []js.Value) any { return wasmDiff(h, args) }))
	api.Set("merge", js.FuncOf(func(_ js.Value, args []js.Value) any { return wasmMerge(h, args) }))
	api.Set("info", js.FuncOf(func(_ js.Value, _ []js.Value) any { return jsResult(info) }))
	if p, ok := h.(Previewer); ok {
		api.Set("preview", js.FuncOf(func(_ js.Value, args []js.Value) any { return wasmPreview(p, args) }))
	}
	if i, ok := h.(Importer); ok {
		api.Set("import", js.FuncOf(func(_ js.Value, args []js.Value) any { return wasmImport(i, args) }))
	}
	if e, ok := h.(Exporter); ok {
		api.Set("export", js.FuncOf(func(_ js.Value, args []js.Value) any { return wasmExport(info, e, args) }))
	}
	if a, ok := h.(ChoiceApplier); ok {
		api.Set("applyChoices", js.FuncOf(func(_ js.Value, args []js.Value) any { return wasmApplyChoices(a, args) }))
	}
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

// preview(blob): one Uint8Array → {mediaType, blob: Uint8Array} on success, or
// {error} — an object either way, so callers test `.error` rather than parse.
func wasmPreview(p Previewer, args []js.Value) any {
	result := js.Global().Get("Object").New()
	if len(args) < 1 {
		result.Set("error", "preview(blob) requires one Uint8Array argument")
		return result
	}
	out, err := p.Preview(bytesFromArg(args[0]))
	if err != nil {
		result.Set("error", err.Error())
		return result
	}
	arr := js.Global().Get("Uint8Array").New(len(out))
	js.CopyBytesToJS(arr, out)
	result.Set("mediaType", p.PreviewMediaType())
	result.Set("blob", arr)
	return result
}

// applyChoices(merged, theirs, takeJSON): two Uint8Arrays and a JSON array of
// conflict paths → {blob: base64} JSON string, or {error}.
func wasmApplyChoices(a ChoiceApplier, args []js.Value) any {
	if len(args) < 3 {
		return `{"error":"applyChoices(merged, theirs, take) requires two Uint8Arrays and a JSON array of paths"}`
	}
	var take []string
	if err := json.Unmarshal([]byte(args[2].String()), &take); err != nil {
		return jsError(err)
	}
	out, err := a.ApplyChoices(bytesFromArg(args[0]), bytesFromArg(args[1]), take)
	if err != nil {
		return jsError(err)
	}
	return jsResult(struct {
		Blob string `json:"blob"`
	}{base64.StdEncoding.EncodeToString(out)})
}

// import(blob): one Uint8Array → {mediaType, blob: Uint8Array} (a GLB), or
// {error} — shaped like preview, for the same reason.
func wasmImport(i Importer, args []js.Value) any {
	result := js.Global().Get("Object").New()
	if len(args) < 1 {
		result.Set("error", "import(blob) requires one Uint8Array argument")
		return result
	}
	out, err := i.Import(bytesFromArg(args[0]))
	if err != nil {
		result.Set("error", err.Error())
		return result
	}
	arr := js.Global().Get("Uint8Array").New(len(out))
	js.CopyBytesToJS(arr, out)
	result.Set("mediaType", MediaTypeGLB)
	result.Set("blob", arr)
	return result
}

// export(glb, format): a GLB Uint8Array and the target extension → {blob:
// Uint8Array}, or {error}. An omitted format follows the CLI's rule.
func wasmExport(info Info, e Exporter, args []js.Value) any {
	result := js.Global().Get("Object").New()
	if len(args) < 1 {
		result.Set("error", "export(glb, format) requires a Uint8Array argument")
		return result
	}
	want := ""
	if len(args) > 1 && args[1].Type() == js.TypeString {
		want = args[1].String()
	}
	format, err := exportFormat(info, want)
	if err != nil {
		result.Set("error", err.Error())
		return result
	}
	out, err := e.Export(bytesFromArg(args[0]), format)
	if err != nil {
		result.Set("error", err.Error())
		return result
	}
	arr := js.Global().Get("Uint8Array").New(len(out))
	js.CopyBytesToJS(arr, out)
	result.Set("blob", arr)
	return result
}
