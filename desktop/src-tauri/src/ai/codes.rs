//! Every error code the local-AI subsystem (`src/ai/*`) can produce, named
//! rather than typed as a string literal at each call site - the same
//! reasoning as `agent::codes` (a typo in a literal would silently produce
//! an untranslatable code no test could catch; a typo in a constant name is
//! a compile error instead).
//!
//! `desktop/src/i18n/aiStatusCatalogue.ts` must have a template for every
//! one of these, hand-kept in sync the same way `agentErrorCatalogue.ts`
//! is kept in sync with `agent::codes` - `all_codes_have_no_duplicates`
//! here proves this list is internally consistent; that file's own test
//! proves its catalogue matches.

#[allow(dead_code)]
pub const RUNTIME_MISSING: &str = "runtime_missing";
#[allow(dead_code)]
pub const RUNTIME_HASH_MISMATCH: &str = "runtime_hash_mismatch";
#[allow(dead_code)]
pub const RUNTIME_NOT_EXECUTABLE: &str = "runtime_not_executable";
#[allow(dead_code)]
pub const RUNTIME_INCOMPATIBLE_GLIBC: &str = "runtime_incompatible_glibc";
#[allow(dead_code)]
pub const RUNTIME_SPAWN_FAILED: &str = "runtime_spawn_failed";
#[allow(dead_code)]
pub const RUNTIME_PORT_UNAVAILABLE: &str = "runtime_port_unavailable";
#[allow(dead_code)]
pub const RUNTIME_NOT_READY: &str = "runtime_not_ready";
#[allow(dead_code)]
pub const RUNTIME_EXITED: &str = "runtime_exited";
#[allow(dead_code)]
pub const MODEL_MISSING: &str = "model_missing";
#[allow(dead_code)]
pub const MODEL_HASH_MISMATCH: &str = "model_hash_mismatch";
#[allow(dead_code)]
pub const MODEL_QUARANTINED: &str = "model_quarantined";
#[allow(dead_code)]
pub const MANIFEST_FETCH_FAILED: &str = "manifest_fetch_failed";
#[allow(dead_code)]
pub const MANIFEST_SIGNATURE_INVALID: &str = "manifest_signature_invalid";
#[allow(dead_code)]
pub const MANIFEST_MODEL_NOT_FOUND: &str = "manifest_model_not_found";
#[allow(dead_code)]
pub const DOWNLOAD_FAILED: &str = "download_failed";
#[allow(dead_code)]
pub const DOWNLOAD_CANCELLED: &str = "download_cancelled";
#[allow(dead_code)]
pub const PREFLIGHT_RAM: &str = "preflight_ram";
#[allow(dead_code)]
pub const PREFLIGHT_DISK: &str = "preflight_disk";
#[allow(dead_code)]
pub const CLI_MISSING: &str = "cli_missing";
#[allow(dead_code)]
pub const CLI_VERSION_MISMATCH: &str = "cli_version_mismatch";
#[allow(dead_code)]
pub const CLI_FAILED: &str = "cli_failed";
#[allow(dead_code)]
pub const ANALYSIS_TIMEOUT: &str = "analysis_timeout";
#[allow(dead_code)]
pub const ANALYSIS_CANCELLED: &str = "analysis_cancelled";
#[allow(dead_code)]
pub const ANALYSIS_INVALID: &str = "analysis_invalid";
#[allow(dead_code)]
pub const NOTES_NEED_RECORDER: &str = "notes_need_recorder";
#[allow(dead_code)]
pub const NOTES_WRITE_FAILED: &str = "notes_write_failed";
#[allow(dead_code)]
pub const FEATURE_DISABLED: &str = "feature_disabled";

#[allow(dead_code)] // read only by this module's own tests until lib.rs wires a use for it
pub const ALL: &[&str] = &[
    RUNTIME_MISSING,
    RUNTIME_HASH_MISMATCH,
    RUNTIME_NOT_EXECUTABLE,
    RUNTIME_INCOMPATIBLE_GLIBC,
    RUNTIME_SPAWN_FAILED,
    RUNTIME_PORT_UNAVAILABLE,
    RUNTIME_NOT_READY,
    RUNTIME_EXITED,
    MODEL_MISSING,
    MODEL_HASH_MISMATCH,
    MODEL_QUARANTINED,
    MANIFEST_FETCH_FAILED,
    MANIFEST_SIGNATURE_INVALID,
    MANIFEST_MODEL_NOT_FOUND,
    DOWNLOAD_FAILED,
    DOWNLOAD_CANCELLED,
    PREFLIGHT_RAM,
    PREFLIGHT_DISK,
    CLI_MISSING,
    CLI_VERSION_MISMATCH,
    CLI_FAILED,
    ANALYSIS_TIMEOUT,
    ANALYSIS_CANCELLED,
    ANALYSIS_INVALID,
    NOTES_NEED_RECORDER,
    NOTES_WRITE_FAILED,
    FEATURE_DISABLED,
];

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashSet;

    /// `desktop/src/i18n/aiStatusCatalogue.ts` hand-keeps a matching literal
    /// list and checks its catalogue against it - this is the other half:
    /// that `ALL` itself has no duplicate and no typo'd entry.
    #[test]
    fn all_codes_have_no_duplicates_and_match_the_expected_count() {
        let unique: HashSet<&str> = ALL.iter().copied().collect();
        assert_eq!(
            unique.len(),
            ALL.len(),
            "ALL has a duplicate entry: {ALL:?}"
        );
        // A change to this number is exactly the signal to go update
        // aiStatusCatalogue.test.ts's own EXPECTED_CODES list too.
        assert_eq!(
            ALL.len(),
            27,
            "a code was added or removed - update aiStatusCatalogue.test.ts too"
        );
    }

    #[test]
    fn every_code_is_a_non_empty_snake_case_identifier() {
        for code in ALL {
            assert!(!code.is_empty());
            assert!(
                code.chars().all(|c| c.is_ascii_lowercase() || c == '_'),
                "{code:?} is not snake_case - it would not match a hand-typed catalogue key on the TS side"
            );
        }
    }
}
