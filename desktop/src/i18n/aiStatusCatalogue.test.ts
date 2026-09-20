import { describe, expect, it } from "vitest";
import { messageFor, type AiErrorPayload } from "./aiStatusCatalogue";

// The exact set of codes desktop/src-tauri/src/ai/codes.rs's ALL currently
// lists - kept in sync by hand with that file's own count-pinned test
// (all_codes_have_no_duplicates_and_match_the_expected_count, asserting 26).
// A code added on one side and not the other fails one of these two tests.
const EXPECTED_CODES = [
  "runtime_missing",
  "runtime_hash_mismatch",
  "runtime_not_executable",
  "runtime_incompatible_glibc",
  "runtime_spawn_failed",
  "runtime_port_unavailable",
  "runtime_not_ready",
  "runtime_exited",
  "model_missing",
  "model_hash_mismatch",
  "model_quarantined",
  "manifest_fetch_failed",
  "manifest_signature_invalid",
  "manifest_model_not_found",
  "download_failed",
  "download_cancelled",
  "preflight_ram",
  "preflight_disk",
  "cli_missing",
  "cli_version_mismatch",
  "cli_failed",
  "analysis_timeout",
  "analysis_cancelled",
  "analysis_invalid",
  "notes_need_recorder",
  "notes_write_failed",
];

function errorOf(code: string, message = `<message for ${code}>`): AiErrorPayload {
  return { code, message };
}

describe("aiStatusCatalogue completeness against the codes codes.rs actually produces", () => {
  it("has exactly 26 expected codes (matching codes.rs's own pinned count)", () => {
    expect(EXPECTED_CODES.length).toBe(26);
  });

  it("has no duplicate expected codes", () => {
    expect(new Set(EXPECTED_CODES).size).toBe(EXPECTED_CODES.length);
  });

  it("has a template for every expected code, in both languages", () => {
    for (const code of EXPECTED_CODES) {
      const en = messageFor(errorOf(code), "en");
      const ar = messageFor(errorOf(code), "ar");
      expect(en.known, `${code} has no English template`).toBe(true);
      expect(ar.known, `${code} has no Arabic template`).toBe(true);
      expect(en.message.trim(), `${code}'s English message is empty`).not.toBe("");
      expect(ar.message.trim(), `${code}'s Arabic message is empty`).not.toBe("");
    }
  });
});

describe("messageFor", () => {
  it("falls back to the payload's own message, tagged unknown, for a code this build does not recognise", () => {
    const err = errorOf("some_future_code_this_build_predates", "raw message from a newer build");
    const result = messageFor(err, "ar");
    expect(result.known).toBe(false);
    expect(result.message).toBe(err.message);
  });

  it("returns the Arabic and English templates as distinct text for the same code", () => {
    const err = errorOf("runtime_not_ready");
    const en = messageFor(err, "en");
    const ar = messageFor(err, "ar");
    expect(en.message).not.toBe(ar.message);
  });
});
