# fhr — the FHR handler SDK for Rust

Write a format handler in Rust and ship it the way every FHR handler ships. You
implement one `Handler`, and the SDK gives you both entry points (SPEC.md §7):

- **the subprocess protocol** forge runs on people's machines: `match`, `diff`,
  `merge`, `preview`, `apply-choices`, `import`, `export` and `info`, as JSON
  over stdin/stdout. Call `fhr::run` from `main`.
- **the `fhr-wasm-1` module** ForgeHub runs on its server and in browsers. Add
  `fhr::export_wasm!` to your library.

Both answer exactly as the Go SDK (`packages/go/fhr`) does, with the same JSON,
exit codes and error texts. `tests/protocol.rs` holds them to the Go tests'
expectations.

## A handler

`packages/example-handler-rust` is a complete one. In short:

```rust
// src/lib.rs
use fhr::{ChangeKind, DiffChange, Handler, Info, StructuredDiff};

pub struct Kv;

impl Handler for Kv {
    fn info(&self) -> Info {
        Info::new("kv", [".kv"])
    }

    fn diff(&self, base: &[u8], head: &[u8]) -> Result<StructuredDiff, String> {
        // … compare, and describe what changed:
        Ok(StructuredDiff::new("kv", vec![
            DiffChange::new("keys/port", ChangeKind::Modified, "port").before("80").after("8080"),
        ]))
    }
}

fhr::export_wasm!(Kv, Kv);
```

```rust
// src/main.rs
fn main() {
    fhr::run(&my_handler::Kv)
}
```

```toml
# Cargo.toml: the library is the wasm module, the binary is forge's
[lib]
crate-type = ["cdylib", "rlib"]

[[bin]]
name = "forge-handler-kv"
path = "src/main.rs"
```

Only `info` and `diff` are required. `merge`, `preview`, `apply_choices`,
`import` and `export` have defaults that answer "not supported". Go can ask a
value which interfaces it implements; Rust can't. So a Rust handler **declares**
its optional calls in `Info`, and the SDK answers only what is declared:

| implement | and declare in `Info` |
|---|---|
| `merge` | `capabilities.semantic_merge` |
| `preview` | `.preview("image/png")` (its media type) |
| `apply_choices` | `capabilities.apply_choices` |
| `import` / `export` | `capabilities.import` / `capabilities.export` |

Errors are plain messages (`Err(String)`), reported as `{"error": "…"}`. A
panic is caught and reported the same way natively. In a wasm module it traps
instead, and the host starts a fresh instance for the next call.

## Building

```bash
cargo build --release -p my-handler                                         # forge-handler-<id>
cargo build --release -p my-handler --lib --target wasm32-unknown-unknown   # the fhr-wasm-1 module
```

The wasm build needs `rustup target add wasm32-unknown-unknown`. The module
imports nothing, and `@fhr/wasm-host` (packages/wasm-host) loads it.
