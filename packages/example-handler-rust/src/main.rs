//! forge-handler-kv: the subprocess protocol forge runs (SPEC.md §7).
fn main() {
    fhr::run(&example_handler_rust::Kv)
}
