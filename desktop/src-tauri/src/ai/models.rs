//! Model storage layout and preflight checks (Part C of the 1.2.0 plan):
//! `app_local_data_dir()/ai/{models/,quarantine/,logs/}`, and whether this
//! machine has enough free RAM and disk before a download or a launch is
//! even offered.
//!
//! `#![allow(dead_code)]`: wired into `lib.rs`'s Tauri commands in the
//! next step of this same phase - see runtime.rs's identical note.
#![allow(dead_code)]

use std::path::{Path, PathBuf};

/// The AI subsystem's own subtree under the app's local data directory -
/// never the recorder's (usually service-owned) data directory, matching
/// `internal/aimodel`'s own choice on the CLI side (a model download must
/// work for an ordinary user with no recorder installed at all).
pub fn ai_dir(app_local_data_dir: &Path) -> PathBuf {
    app_local_data_dir.join("ai")
}

pub fn models_dir(app_local_data_dir: &Path) -> PathBuf {
    ai_dir(app_local_data_dir).join("models")
}

pub fn quarantine_dir(app_local_data_dir: &Path) -> PathBuf {
    ai_dir(app_local_data_dir).join("quarantine")
}

pub fn logs_dir(app_local_data_dir: &Path) -> PathBuf {
    ai_dir(app_local_data_dir).join("logs")
}

/// The result of checking a machine against one model's own stated
/// requirements, before a download (disk) or a launch (RAM) is offered.
#[derive(Debug, Clone, PartialEq)]
pub struct Preflight {
    pub ram_ok: bool,
    pub available_ram_bytes: u64,
    pub disk_ok: bool,
    pub available_disk_bytes: u64,
}

/// The pure comparison at preflight's core, separated from actually
/// querying the OS (`live_preflight`) so it can be tested with known
/// numbers instead of whatever RAM/disk a test happens to run with.
pub fn evaluate_preflight(
    available_ram_bytes: u64,
    min_ram_available_bytes: u64,
    available_disk_bytes: u64,
    size_bytes: u64,
) -> Preflight {
    Preflight {
        ram_ok: available_ram_bytes >= min_ram_available_bytes,
        available_ram_bytes,
        disk_ok: available_disk_bytes >= size_bytes,
        available_disk_bytes,
    }
}

/// Picks the free space of whichever mounted disk actually contains
/// `path` - the mount point with the longest matching prefix, since a
/// nested mount (e.g. a separate `/home` mount under `/`) must win over
/// its parent. `0` (never enough) if nothing matches, which should not
/// happen on a real machine but must never be mistaken for "unlimited".
fn free_space_for_path(mounts: &[(PathBuf, u64)], path: &Path) -> u64 {
    mounts
        .iter()
        .filter(|(mount, _)| path.starts_with(mount))
        .max_by_key(|(mount, _)| mount.as_os_str().len())
        .map(|(_, free)| *free)
        .unwrap_or(0)
}

/// Queries this machine's real available RAM and the free space of the
/// disk `models_dir` lives on, then applies `evaluate_preflight`.
pub fn live_preflight(
    models_dir: &Path,
    min_ram_available_bytes: u64,
    size_bytes: u64,
) -> Preflight {
    let mut sys = sysinfo::System::new();
    sys.refresh_memory();
    let available_ram = sys.available_memory();

    let disks = sysinfo::Disks::new_with_refreshed_list();
    let mounts: Vec<(PathBuf, u64)> = disks
        .list()
        .iter()
        .map(|d| (d.mount_point().to_path_buf(), d.available_space()))
        .collect();
    let available_disk = free_space_for_path(&mounts, models_dir);

    evaluate_preflight(
        available_ram,
        min_ram_available_bytes,
        available_disk,
        size_bytes,
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn storage_paths_are_all_under_the_ai_subtree() {
        let base = Path::new("/data");
        assert_eq!(models_dir(base), Path::new("/data/ai/models"));
        assert_eq!(quarantine_dir(base), Path::new("/data/ai/quarantine"));
        assert_eq!(logs_dir(base), Path::new("/data/ai/logs"));
    }

    #[test]
    fn evaluate_preflight_passes_when_both_resources_are_sufficient() {
        let p = evaluate_preflight(8_000_000_000, 4_000_000_000, 10_000_000_000, 2_500_000_000);
        assert!(p.ram_ok);
        assert!(p.disk_ok);
    }

    #[test]
    fn evaluate_preflight_fails_ram_but_not_disk() {
        let p = evaluate_preflight(1_000_000_000, 4_000_000_000, 10_000_000_000, 2_500_000_000);
        assert!(!p.ram_ok);
        assert!(p.disk_ok);
    }

    #[test]
    fn evaluate_preflight_fails_disk_but_not_ram() {
        let p = evaluate_preflight(8_000_000_000, 4_000_000_000, 1_000_000_000, 2_500_000_000);
        assert!(p.ram_ok);
        assert!(!p.disk_ok);
    }

    #[test]
    fn evaluate_preflight_boundary_is_inclusive() {
        // Exactly enough must pass - a model whose own stated minimum is
        // the machine's exact available amount should not be refused.
        let p = evaluate_preflight(4_000_000_000, 4_000_000_000, 2_500_000_000, 2_500_000_000);
        assert!(p.ram_ok);
        assert!(p.disk_ok);
    }

    #[test]
    fn free_space_for_path_prefers_the_longest_matching_mount() {
        let mounts = vec![
            (PathBuf::from("/"), 1_000),
            (PathBuf::from("/home"), 50_000),
        ];
        assert_eq!(
            free_space_for_path(&mounts, Path::new("/home/user/.cache/netrewind")),
            50_000
        );
        assert_eq!(
            free_space_for_path(&mounts, Path::new("/var/lib/other")),
            1_000
        );
    }

    #[test]
    fn free_space_for_path_is_zero_when_nothing_matches() {
        let mounts = vec![(PathBuf::from("/mnt/data"), 99_999)];
        assert_eq!(free_space_for_path(&mounts, Path::new("/home/user")), 0);
    }

    #[test]
    fn live_preflight_returns_plausible_nonzero_numbers_on_this_real_machine() {
        // Not a mock: this actually queries the real machine running the
        // test. Only asserts a sanity bound, since the real numbers vary
        // by machine - the point is that querying sysinfo/Disks and
        // wiring them into evaluate_preflight does not silently produce 0
        // or panic.
        let p = live_preflight(&std::env::temp_dir(), 1, 1);
        assert!(
            p.available_ram_bytes > 0,
            "no RAM reported at all - the sysinfo wiring is broken"
        );
    }
}
