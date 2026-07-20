package main

// Shared handler identity, used by both the native CLI entry point (main.go)
// and the WebAssembly entry point (wasm.go), which are mutually exclusive
// build targets.
const (
	handlerID   = "svg"
	protocolVer = "1.0"
)
