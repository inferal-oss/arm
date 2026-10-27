//! The host functions ABI.md allows: `env.now` and `env.random`.

#[link(wasm_import_module = "env")]
unsafe extern "C" {
    #[link_name = "now"]
    fn host_now() -> f64;
    #[link_name = "random"]
    fn host_random() -> f64;
}

/// Milliseconds since the Unix epoch.
pub fn now() -> f64 {
    unsafe { host_now() }
}

/// Uniform in [0, 1).
pub fn random() -> f64 {
    unsafe { host_random() }
}
