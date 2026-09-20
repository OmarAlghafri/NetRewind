//! The local AI assistant's desktop-shell half: sidecar runtime lifecycle,
//! model storage, and the staged CLI (Part C of the 1.2.0 plan). Off by
//! default (`AI_FEATURE_ENABLED`) until a model profile passes its
//! evaluation gate - see docs/product/adr/0006.

pub mod cli;
pub mod codes;
pub mod models;
pub mod runtime;

/// The compile-time gate ADR 0006 describes: `false` until a model profile
/// in the signed manifest actually passes its pre-registered evaluation
/// gate (execution order §4.10 - thresholds fixed before the run, never
/// lowered after seeing a result). This is deliberately not a runtime
/// setting: `aiSettings.enabled` (the desktop's own localStorage setting,
/// also off by default) only controls whether an operator who could
/// already reach this feature has turned it on for themselves - it is not
/// itself the safety boundary. Every one of this module's Tauri commands
/// checks this constant independently of whatever the frontend already
/// decided to render, so a UI bug that fails to hide the panel cannot by
/// itself let a build ship the feature ungated.
pub const AI_FEATURE_ENABLED: bool = false;

/// Called first by every Tauri command in this module that would spawn a
/// process or otherwise do real work - a shared choke point rather than
/// three copies of the same `if !AI_FEATURE_ENABLED { ... }` at each call
/// site, so there is exactly one place to get this check right (and one
/// place a test can exercise it against).
pub fn require_enabled() -> Result<(), String> {
    if AI_FEATURE_ENABLED {
        Ok(())
    } else {
        Err(codes::FEATURE_DISABLED.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn require_enabled_refuses_while_the_feature_flag_is_off() {
        assert!(!AI_FEATURE_ENABLED, "flip this test once the flag flips");
        assert_eq!(require_enabled(), Err(codes::FEATURE_DISABLED.to_string()));
    }
}
