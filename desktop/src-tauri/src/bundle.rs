//! Reading evidence bundles.
//!
//! A bundle is what `netrewind bundle export` (or the recorder's
//! `/v1/bundle`) writes: a tar.gz holding `manifest.json`, `events.json`,
//! `incidents.json`, a `SHA256SUMS` file over the three, and optionally
//! `SHA256SUMS.sig`, an ed25519 signature over the checksum file. Opening
//! one here mirrors `internal/bundle.Inspect` exactly: every member is
//! checked against the checksum file before anything is parsed, a
//! configured public key makes the signature mandatory, and the contents are
//! only ever shown - never merged into any store.

use base64::Engine;
use ed25519_dalek::{Signature, Verifier, VerifyingKey};
use flate2::read::GzDecoder;
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::io::Read;

const MANIFEST: &str = "manifest.json";
const EVENTS: &str = "events.json";
const INCIDENTS: &str = "incidents.json";
/// Optional (ADR 0008): the sender's own operator notes for the incidents
/// in this bundle's window, present only when the exporting recorder had
/// notes enabled. Absent from the required-member checks below by design -
/// an older bundle, or one exported with notes disabled, simply lacks it.
const NOTES: &str = "notes.json";
const CHECKSUMS: &str = "SHA256SUMS";
const SIGNATURE: &str = "SHA256SUMS.sig";

/// The largest member accepted, matching the Go side's `maxBundleMember`.
const MAX_MEMBER: u64 = 256 << 20;

/// The highest bundle format and event schema this viewer understands.
const FORMAT_VERSION: u64 = 1;
const SCHEMA_VERSION: u64 = 1;

#[derive(Serialize, Debug)]
pub struct Contents {
    pub manifest: serde_json::Value,
    pub events: serde_json::Value,
    pub incidents: serde_json::Value,
    /// `None` when the archive carries no notes.json at all (an older
    /// bundle, or one exported with notes disabled) - distinct from
    /// `Some(Value::Array(vec![]))`, which means the sender's own
    /// notes.Store was queried and genuinely had none for this window.
    /// Shown read-only, labelled as coming from the bundle - ADR 0008:
    /// never merged into the viewer's own record or notes store.
    pub notes: Option<serde_json::Value>,
    /// True when a public key was given and the signature verified.
    pub signed: bool,
    /// True when the archive carried a signature file at all.
    pub has_signature: bool,
}

pub fn open(path: &str, public_key: Option<&str>) -> Result<Contents, String> {
    let file = std::fs::File::open(path).map_err(|e| format!("cannot open {path}: {e}"))?;
    let members = read_tar_gz(file)?;

    for required in [MANIFEST, EVENTS, INCIDENTS, CHECKSUMS] {
        if !members.contains_key(required) {
            return Err(format!("not an evidence bundle: {required} is missing"));
        }
    }
    verify_checksums(&members)?;

    let has_signature = members.contains_key(SIGNATURE);
    let signed = match public_key.map(str::trim).filter(|k| !k.is_empty()) {
        None => false,
        Some(key) => {
            let sig = members.get(SIGNATURE).ok_or_else(|| {
                "a public key is configured, so an unsigned bundle is refused (no SHA256SUMS.sig)".to_string()
            })?;
            verify_signature(key, &members[CHECKSUMS], sig)?;
            true
        }
    };

    let manifest: serde_json::Value = serde_json::from_slice(&members[MANIFEST])
        .map_err(|e| format!("manifest.json does not parse: {e}"))?;
    let format = manifest
        .get("format_version")
        .and_then(|v| v.as_u64())
        .unwrap_or(0);
    if format > FORMAT_VERSION {
        return Err(format!(
            "bundle format v{format}; this build understands up to v{FORMAT_VERSION} - open it with a newer NetRewind"
        ));
    }
    let schema = manifest
        .get("schema_version")
        .and_then(|v| v.as_u64())
        .unwrap_or(0);
    if schema > SCHEMA_VERSION {
        return Err(format!(
            "event schema v{schema}; this build understands up to v{SCHEMA_VERSION} - open it with a newer NetRewind"
        ));
    }
    let mut events: serde_json::Value = serde_json::from_slice(&members[EVENTS])
        .map_err(|e| format!("events.json does not parse: {e}"))?;
    let mut incidents: serde_json::Value = serde_json::from_slice(&members[INCIDENTS])
        .map_err(|e| format!("incidents.json does not parse: {e}"))?;
    // Bundles written before 1.0.0 encode an empty list as null.
    for v in [&mut events, &mut incidents] {
        if v.is_null() {
            *v = serde_json::Value::Array(Vec::new());
        }
    }
    if !events.is_array() || !incidents.is_array() {
        return Err("events.json and incidents.json must each be a JSON array".to_string());
    }

    let notes = match members.get(NOTES) {
        None => None,
        Some(raw) => {
            let mut v: serde_json::Value = serde_json::from_slice(raw)
                .map_err(|e| format!("notes.json does not parse: {e}"))?;
            if v.is_null() {
                v = serde_json::Value::Array(Vec::new());
            }
            if !v.is_array() {
                return Err("notes.json must be a JSON array".to_string());
            }
            Some(v)
        }
    };

    Ok(Contents {
        manifest,
        events,
        incidents,
        notes,
        signed,
        has_signature,
    })
}

fn read_tar_gz<R: Read>(r: R) -> Result<HashMap<String, Vec<u8>>, String> {
    let mut archive = tar::Archive::new(GzDecoder::new(r));
    let mut members = HashMap::new();
    let entries = archive
        .entries()
        .map_err(|e| format!("corrupt archive: {e}"))?;
    for entry in entries {
        let mut entry = entry.map_err(|e| format!("corrupt archive: {e}"))?;
        let name = entry
            .path()
            .map_err(|e| format!("corrupt archive: {e}"))?
            .to_string_lossy()
            .trim_start_matches("./")
            .to_string();
        // Only the flat, known member names are read; anything else (a path
        // with a directory, an unknown file) is ignored rather than trusted.
        if !matches!(
            name.as_str(),
            MANIFEST | EVENTS | INCIDENTS | NOTES | CHECKSUMS | SIGNATURE
        ) {
            continue;
        }
        if entry.size() > MAX_MEMBER {
            return Err(format!(
                "{name} is larger than {} MiB; refusing",
                MAX_MEMBER >> 20
            ));
        }
        let mut data = Vec::with_capacity(entry.size() as usize);
        entry
            .read_to_end(&mut data)
            .map_err(|e| format!("corrupt archive: {e}"))?;
        members.insert(name, data);
    }
    Ok(members)
}

fn verify_checksums(members: &HashMap<String, Vec<u8>>) -> Result<(), String> {
    let sums = parse_checksums(&members[CHECKSUMS])?;
    // NOTES is required-if-present: a bundle without it is fine (older, or
    // notes disabled at export time), but one that DOES carry notes.json
    // must have it listed and matching, the same as every other member -
    // an unverified-but-parsed notes.json would be exactly the tampering
    // gap this whole check exists to close.
    let mut required = vec![MANIFEST, EVENTS, INCIDENTS];
    if members.contains_key(NOTES) {
        required.push(NOTES);
    }
    for name in required {
        let want = sums
            .get(name)
            .ok_or_else(|| format!("{name} is not listed in SHA256SUMS"))?;
        let got = Sha256::digest(&members[name]);
        if got.as_slice() != want.as_slice() {
            return Err(format!(
                "{name} does not match SHA256SUMS - the bundle is corrupt or was tampered with"
            ));
        }
    }
    Ok(())
}

fn parse_checksums(data: &[u8]) -> Result<HashMap<String, Vec<u8>>, String> {
    let text = std::str::from_utf8(data).map_err(|_| "SHA256SUMS is not text".to_string())?;
    let mut out = HashMap::new();
    for line in text.lines() {
        let line = line.trim();
        if line.is_empty() {
            continue;
        }
        let mut fields = line.split_whitespace();
        let (Some(hexsum), Some(name), None) = (fields.next(), fields.next(), fields.next()) else {
            return Err(format!(
                "SHA256SUMS line {line:?} is not '<sha256>  <name>'"
            ));
        };
        let sum = hex::decode(hexsum).map_err(|_| format!("SHA256SUMS: {hexsum:?} is not hex"))?;
        if sum.len() != 32 {
            return Err(format!("SHA256SUMS: {hexsum:?} is not a SHA-256"));
        }
        out.insert(name.trim_start_matches('*').to_string(), sum);
    }
    if out.is_empty() {
        return Err("SHA256SUMS is empty".to_string());
    }
    Ok(out)
}

fn verify_signature(public_key: &str, checksums: &[u8], signature: &[u8]) -> Result<(), String> {
    let engine = base64::engine::general_purpose::STANDARD;
    let raw = engine
        .decode(public_key.trim())
        .map_err(|_| "the configured public key is not base64".to_string())?;
    let key: [u8; 32] = raw
        .try_into()
        .map_err(|_| "the configured public key is not a 32-byte ed25519 key".to_string())?;
    let key = VerifyingKey::from_bytes(&key)
        .map_err(|e| format!("the configured public key is invalid: {e}"))?;
    // The signature travels raw or base64-armoured, same as internal/update accepts.
    let sig_bytes: Vec<u8> = match engine.decode(String::from_utf8_lossy(signature).trim()) {
        Ok(decoded) if decoded.len() == 64 => decoded,
        _ => signature.to_vec(),
    };
    let sig_bytes: [u8; 64] = sig_bytes
        .try_into()
        .map_err(|_| "SHA256SUMS.sig is not a 64-byte ed25519 signature".to_string())?;
    key.verify(checksums, &Signature::from_bytes(&sig_bytes))
        .map_err(|_| {
            "signature does not verify: the checksum file is not signed by the configured key"
                .to_string()
        })
}

#[cfg(test)]
mod tests {
    use super::*;
    use flate2::write::GzEncoder;
    use flate2::Compression;
    use std::io::Write;

    fn tarball(members: &[(&str, &[u8])]) -> Vec<u8> {
        let mut buf = Vec::new();
        {
            let enc = GzEncoder::new(&mut buf, Compression::default());
            let mut tar = tar::Builder::new(enc);
            for (name, data) in members {
                let mut header = tar::Header::new_gnu();
                header.set_size(data.len() as u64);
                header.set_mode(0o644);
                header.set_cksum();
                tar.append_data(&mut header, name, *data).unwrap();
            }
            tar.into_inner().unwrap().finish().unwrap();
        }
        buf
    }

    fn checksums(members: &[(&str, &[u8])]) -> Vec<u8> {
        let mut lines: Vec<String> = members
            .iter()
            .map(|(name, data)| format!("{}  {}\n", hex::encode(Sha256::digest(data)), name))
            .collect();
        lines.sort();
        lines.concat().into_bytes()
    }

    fn write_temp(name: &str, data: &[u8]) -> String {
        let path = std::env::temp_dir().join(format!(
            "netrewind-desktop-test-{}-{name}",
            std::process::id()
        ));
        std::fs::File::create(&path)
            .unwrap()
            .write_all(data)
            .unwrap();
        path.to_string_lossy().to_string()
    }

    fn good_members() -> Vec<(&'static str, Vec<u8>)> {
        let manifest = br#"{"format_version":1,"schema_version":1,"app_version":"t","observer_id":"o","event_count":1,"incident_count":0}"#.to_vec();
        let events = br#"[{"event_id":"01TEST","kind":"link.down"}]"#.to_vec();
        let incidents = b"[]".to_vec();
        let sums = checksums(&[
            (MANIFEST, &manifest),
            (EVENTS, &events),
            (INCIDENTS, &incidents),
        ]);
        vec![
            (MANIFEST, manifest),
            (EVENTS, events),
            (INCIDENTS, incidents),
            (CHECKSUMS, sums),
        ]
    }

    fn as_refs<'a>(m: &'a [(&'static str, Vec<u8>)]) -> Vec<(&'static str, &'a [u8])> {
        m.iter().map(|(n, d)| (*n, d.as_slice())).collect()
    }

    #[test]
    fn opens_a_well_formed_bundle() {
        let members = good_members();
        let path = write_temp("good.tar.gz", &tarball(&as_refs(&members)));
        let c = open(&path, None).unwrap();
        assert_eq!(c.events.as_array().unwrap().len(), 1);
        assert_eq!(c.manifest["observer_id"], "o");
        assert!(!c.signed && !c.has_signature);
        assert!(
            c.notes.is_none(),
            "no notes.json member was given - c.notes must be None, not an empty array"
        );
    }

    #[test]
    fn opens_a_bundle_with_notes_and_returns_them() {
        let mut members = good_members();
        let notes =
            br#"[{"incident_id":"i1","rule_id":"gateway-hijack","outcome":"confirmed"}]"#.to_vec();
        members.push((NOTES, notes));
        // Rebuild SHA256SUMS over every real member now that notes.json is
        // one of them - good_members()'s own sums do not know about it.
        let checksums_index = members.iter().position(|(n, _)| *n == CHECKSUMS).unwrap();
        let full = members.clone();
        members[checksums_index].1 = checksums(&as_refs(&full));

        let path = write_temp("with-notes.tar.gz", &tarball(&as_refs(&members)));
        let c = open(&path, None).unwrap();
        let notes_arr = c
            .notes
            .expect("notes.json was in the archive - c.notes must be Some");
        assert_eq!(notes_arr.as_array().unwrap().len(), 1);
        assert_eq!(notes_arr[0]["rule_id"], "gateway-hijack");
    }

    #[test]
    fn treats_null_notes_as_an_empty_array() {
        let mut members = good_members();
        let notes = b"null".to_vec();
        members.push((NOTES, notes.clone()));
        let checksums_index = members.iter().position(|(n, _)| *n == CHECKSUMS).unwrap();
        let full: Vec<(&str, Vec<u8>)> = members.clone();
        members[checksums_index].1 = checksums(&as_refs(&full));

        let path = write_temp("null-notes.tar.gz", &tarball(&as_refs(&members)));
        let c = open(&path, None).unwrap();
        assert_eq!(c.notes.unwrap().as_array().unwrap().len(), 0);
    }

    #[test]
    fn refuses_a_tampered_notes_member() {
        let mut members = good_members();
        let notes = br#"[{"incident_id":"i1"}]"#.to_vec();
        members.push((NOTES, notes));
        let checksums_index = members.iter().position(|(n, _)| *n == CHECKSUMS).unwrap();
        let full: Vec<(&str, Vec<u8>)> = members.clone();
        members[checksums_index].1 = checksums(&as_refs(&full));
        // Tamper with notes.json AFTER computing sums over the honest version.
        let notes_index = members.iter().position(|(n, _)| *n == NOTES).unwrap();
        members[notes_index].1 = br#"[{"incident_id":"tampered"}]"#.to_vec();

        let path = write_temp("tampered-notes.tar.gz", &tarball(&as_refs(&members)));
        let err = open(&path, None).unwrap_err();
        assert!(err.contains("does not match SHA256SUMS"), "{err}");
    }

    #[test]
    fn accepts_null_for_an_empty_list() {
        let manifest = br#"{"format_version":1,"schema_version":1}"#.to_vec();
        let events = b"null".to_vec();
        let incidents = b"null".to_vec();
        let sums = checksums(&[
            (MANIFEST, &manifest),
            (EVENTS, &events),
            (INCIDENTS, &incidents),
        ]);
        let members = vec![
            (MANIFEST, manifest),
            (EVENTS, events),
            (INCIDENTS, incidents),
            (CHECKSUMS, sums),
        ];
        let path = write_temp("nulls.tar.gz", &tarball(&as_refs(&members)));
        let c = open(&path, None).unwrap();
        assert_eq!(c.events.as_array().unwrap().len(), 0);
        assert_eq!(c.incidents.as_array().unwrap().len(), 0);
    }

    #[test]
    fn refuses_a_tampered_member() {
        let mut members = good_members();
        members[1].1 = br#"[{"event_id":"01TEST","kind":"link.up"}]"#.to_vec(); // events changed, sums not
        let path = write_temp("tampered.tar.gz", &tarball(&as_refs(&members)));
        let err = open(&path, None).unwrap_err();
        assert!(err.contains("does not match SHA256SUMS"), "{err}");
    }

    #[test]
    fn refuses_a_missing_member_and_garbage() {
        let members = good_members();
        let path = write_temp("missing.tar.gz", &tarball(&as_refs(&members[..3])));
        assert!(open(&path, None)
            .unwrap_err()
            .contains("SHA256SUMS is missing"));
        let path = write_temp("garbage.tar.gz", b"this is not a tarball");
        assert!(open(&path, None).is_err());
    }

    #[test]
    fn refuses_a_newer_format() {
        let mut members = good_members();
        members[0].1 = br#"{"format_version":99,"schema_version":1}"#.to_vec();
        let m = members[0].1.clone();
        members[3].1 = checksums(&[
            (MANIFEST, &m),
            (EVENTS, &members[1].1),
            (INCIDENTS, &members[2].1),
        ]);
        let path = write_temp("newer.tar.gz", &tarball(&as_refs(&members)));
        assert!(open(&path, None).unwrap_err().contains("newer NetRewind"));
    }

    #[test]
    fn signature_is_required_once_a_key_is_configured_and_must_verify() {
        use ed25519_dalek::{Signer, SigningKey};
        let members = good_members();
        let key = SigningKey::from_bytes(&[7u8; 32]);
        let pub_b64 =
            base64::engine::general_purpose::STANDARD.encode(key.verifying_key().to_bytes());
        let path = write_temp("unsigned.tar.gz", &tarball(&as_refs(&members)));
        assert!(open(&path, Some(&pub_b64))
            .unwrap_err()
            .contains("unsigned bundle is refused"));

        let sig = key.sign(&members[3].1).to_bytes().to_vec();
        let mut signed = members.clone();
        signed.push((SIGNATURE, sig));
        let path = write_temp("signed.tar.gz", &tarball(&as_refs(&signed)));
        let c = open(&path, Some(&pub_b64)).unwrap();
        assert!(c.signed && c.has_signature);

        let other = SigningKey::from_bytes(&[9u8; 32]);
        let other_b64 =
            base64::engine::general_purpose::STANDARD.encode(other.verifying_key().to_bytes());
        assert!(open(&path, Some(&other_b64))
            .unwrap_err()
            .contains("not signed by the configured key"));
    }
}
