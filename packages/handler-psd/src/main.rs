//! forge-handler-psd: the subprocess protocol forge runs (SPEC.md §7).
fn main() {
    fhr::run(&handler_psd::Psd)
}
