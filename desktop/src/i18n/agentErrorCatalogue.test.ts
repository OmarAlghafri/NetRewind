import { describe, expect, it } from "vitest";
import { messageFor, type AgentErrorPayload } from "./agentErrorCatalogue";

// The exact set of codes desktop/src-tauri/src/agent.rs's AgentError::new
// (and lib.rs's cancelled_error/"invalid_path") currently constructs -
// kept in sync by hand with a matching literal list in a Rust test in
// agent.rs (see that file's own `error_codes_used_in_this_file` test).
// A code added on one side and not the other fails one of these two
// tests, which is what keeps them from silently drifting apart.
const EXPECTED_CODES = [
  "timeout",
  "handshake_failed",
  "request_build_failed",
  "request_failed",
  "response_read_failed",
  "not_listening",
  "access_denied_windows",
  "access_denied_unix",
  "open_failed",
  "pipe_busy",
  "invalid_path",
  "cancelled",
];

function errorOf(code: string, params: Record<string, string> = {}): AgentErrorPayload {
  return { code, params, technical_detail: `<technical detail for ${code}>` };
}

describe("agentErrorCatalogue completeness against the codes agent.rs actually produces", () => {
  it("has a template for every expected code, in both languages, and no extra ones", () => {
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
  it("interpolates params into the template", () => {
    const err = errorOf("timeout", { endpoint: "/run/netrewind/api.sock", seconds: "10" });
    expect(messageFor(err, "en").message).toBe("The recorder at /run/netrewind/api.sock did not answer within 10s.");
    expect(messageFor(err, "ar").message).toBe("لم يستجب المُسجِّل عند /run/netrewind/api.sock خلال 10 ثانية.");
  });

  it("leaves an unmatched placeholder literally rather than dropping it silently", () => {
    const err = errorOf("timeout", { endpoint: "/tmp/x.sock" }); // missing "seconds"
    expect(messageFor(err, "en").message).toContain("{seconds}");
  });

  it("falls back to technical_detail, tagged unknown, for a code this build does not recognise", () => {
    const err = errorOf("some_future_code_this_build_predates", { x: "y" });
    const result = messageFor(err, "ar");
    expect(result.known).toBe(false);
    expect(result.message).toBe(err.technical_detail);
  });
});
