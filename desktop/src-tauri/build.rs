fn main() {
    // Exposes the compile target triple to src/ai/runtime.rs as
    // env!("TARGET") - runtime.lock.json is keyed by exactly this string
    // (e.g. "x86_64-pc-windows-msvc"), the same triple Cargo already
    // named when it built this binary, so there is no separate detection
    // to keep in sync with it.
    println!(
        "cargo:rustc-env=TARGET={}",
        std::env::var("TARGET").expect("Cargo always sets TARGET for a build script")
    );
    tauri_build::build()
}
