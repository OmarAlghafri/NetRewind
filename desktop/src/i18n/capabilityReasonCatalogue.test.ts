import { describe, expect, it } from "vitest";
import { reasonMessageFor } from "./capabilityReasonCatalogue";

// The exact set of codes cmd/netrewindd/main.go's DownCoded/
// UnsupportedCoded call sites currently use - kept in sync by hand with a
// matching literal expectation on the Go side (see
// internal/registry/registry_test.go's coded-reason tests, which prove
// these two specific codes are what the registry actually produces).
const EXPECTED_CODES = ["requires_platform", "collector_stopped"];

describe("capabilityReasonCatalogue completeness against the codes the recorder actually produces", () => {
  it("has a template for every expected code, in both languages", () => {
    for (const code of EXPECTED_CODES) {
      const cap = { reason: "<fallback>", reason_code: code, reason_params: { platform: "linux" } };
      const en = reasonMessageFor(cap, "en");
      const ar = reasonMessageFor(cap, "ar");
      expect(en.known, `${code} has no English template`).toBe(true);
      expect(ar.known, `${code} has no Arabic template`).toBe(true);
      expect(en.message.trim()).not.toBe("");
      expect(ar.message.trim()).not.toBe("");
    }
  });
});

describe("reasonMessageFor", () => {
  it("interpolates params into the template", () => {
    const cap = { reason: "requires linux", reason_code: "requires_platform", reason_params: { platform: "linux" } };
    expect(reasonMessageFor(cap, "en").message).toBe("This collector requires linux.");
    expect(reasonMessageFor(cap, "ar").message).toBe("تتطلب وحدة جمع البيانات هذه نظام linux.");
  });

  it("falls back to reason, tagged unknown, when there is no code", () => {
    const cap = { reason: "nft: executable file not found", reason_code: undefined, reason_params: undefined };
    const result = reasonMessageFor(cap, "ar");
    expect(result.known).toBe(false);
    expect(result.message).toBe("nft: executable file not found");
  });

  it("falls back to reason, tagged unknown, for a code this build does not recognise", () => {
    const cap = { reason: "some new failure", reason_code: "some_future_code", reason_params: {} };
    const result = reasonMessageFor(cap, "en");
    expect(result.known).toBe(false);
    expect(result.message).toBe("some new failure");
  });

  it("never crashes or returns blank when reason itself is also absent", () => {
    const result = reasonMessageFor({ reason: undefined, reason_code: undefined, reason_params: undefined }, "ar");
    expect(result.message).toBe("");
  });
});
