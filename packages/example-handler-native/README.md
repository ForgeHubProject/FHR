# example-handler-native

Skeleton for a **language-agnostic** FHR backend handler.

The backend can be written in **any language** — Go, Rust, Python, C, anything.
Forge communicates with it over the **subprocess JSON protocol** (stdin/stdout).
ForgeHub can run it as a **WASM module** for sandboxed server-side execution.

The frontend renderer is still TypeScript + React (see `renderer.tsx`).

## Protocol

Your binary must respond to these subcommands:

```
forge-handler-myformat match <filepath>
  stdout: "true" or "false"
  exit:   0 always

forge-handler-myformat diff
  stdin:  { "base": "<base64>", "head": "<base64>" }
  stdout: StructuredDiff JSON (see @fhr/types)
  exit:   0 on success, 1 on error

forge-handler-myformat merge
  stdin:  { "base": "<base64>", "ours": "<base64>", "theirs": "<base64>" }
  stdout: { "blob": "<base64>", "conflicts": [ ...SemanticConflict ] }
  exit:   0 on success, 1 on error (or "not supported")

forge-handler-myformat info
  stdout: { "id": "myformat", "version": "1.0.0", "formats": [".myext"], "protocol": "1.0" }
  exit:   0 always
```

Blobs are base64-encoded so the transport stays pure JSON.

## Files in this skeleton

```
example-handler-native/
├── handler.go          # Go reference implementation of the protocol
├── renderer.tsx        # React renderer (always TypeScript)
└── README.md
```

## Building

```bash
# Native binaries for each platform
GOOS=linux   GOARCH=amd64 go build -o forge-handler-example_linux-amd64   .
GOOS=darwin  GOARCH=arm64 go build -o forge-handler-example_darwin-arm64  .
GOOS=windows GOARCH=amd64 go build -o forge-handler-example_windows-amd64.exe .

# WASM (for ForgeHub API sandboxed execution)
GOOS=wasip1 GOARCH=wasm  go build -o forge-handler-example.wasm .
```
