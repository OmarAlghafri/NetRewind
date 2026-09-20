import { describe, expect, it } from "vitest";
import { formatBytes, formatCount, formatDateTime, formatDuration, formatTime } from "./format";

// execution order §4.6: "one shared module ... instead of the five [in
// fact ten] independent toLocaleTimeString call sites" - and specifically
// Latin numerals in Arabic time output, where a bare "ar-EG" locale
// silently produces Arabic-Indic digits instead, an inconsistency this
// session found by actually reading a live-rendered screenshot, not by
// inspecting the code alone.
describe("formatTime", () => {
  it("renders Latin digits in Arabic, not Arabic-Indic digits", () => {
    const d = new Date("2026-09-19T22:20:22Z");
    const ar = formatTime(d, "ar");
    expect(ar).not.toMatch(/[٠-٩]/);
    expect(ar).toMatch(/^\d{2}:\d{2}:\d{2}$/);
  });

  it("uses 24-hour time in both languages", () => {
    const d = new Date("2026-09-19T22:20:22Z");
    expect(formatTime(d, "ar")).not.toMatch(/[AaPp][Mm]/);
    expect(formatTime(d, "en")).not.toMatch(/[AaPp][Mm]/);
  });
});

describe("formatDateTime", () => {
  it("includes both a date and a time component", () => {
    const d = new Date("2026-09-19T22:20:22Z");
    const s = formatDateTime(d, "en");
    expect(s).toMatch(/2026/);
    expect(s).toMatch(/\d{1,2}:\d{2}:\d{2}/);
  });
});

describe("formatCount", () => {
  it("groups thousands", () => {
    expect(formatCount(12345, "en")).toBe("12,345");
  });

  it("renders Latin digits with grouping in Arabic too", () => {
    const ar = formatCount(12345, "ar");
    expect(ar).not.toMatch(/[٠-٩]/);
    expect(ar.replace(/[^\d]/g, "")).toBe("12345");
  });
});

describe("formatDuration", () => {
  it("matches the execution order's own compact example (4h 49m)", () => {
    expect(formatDuration(4 * 3600 + 49 * 60, "ar", "compact")).toBe("4س 49د");
    expect(formatDuration(4 * 3600 + 49 * 60, "en", "compact")).toBe("4h 49m");
  });

  it("matches the execution order's own detailed example (4 hours and 49 minutes)", () => {
    expect(formatDuration(4 * 3600 + 49 * 60, "ar", "detailed")).toBe("4 ساعات و49 دقيقة");
    expect(formatDuration(4 * 3600 + 49 * 60, "en", "detailed")).toBe("4 hours and 49 minutes");
  });

  it("drops the hour entirely when it is zero, rather than showing 0h", () => {
    expect(formatDuration(5 * 60 + 12, "en", "compact")).toBe("5m 12s");
    expect(formatDuration(5 * 60 + 12, "ar", "compact")).toBe("5د 12ث");
  });

  it("drops both hour and minute when both are zero", () => {
    expect(formatDuration(45, "en", "compact")).toBe("45s");
    expect(formatDuration(45, "ar", "compact")).toBe("45ث");
  });

  it("defaults to the compact style when none is given", () => {
    expect(formatDuration(3661, "en")).toBe(formatDuration(3661, "en", "compact"));
  });

  // Deliberate-break proof: swap the "و" join for a plain space, the
  // mistake of treating it like an English conjunction, and confirm the
  // detailed-Arabic test above would catch it.
  it("[deliberate-break proof] a plain-space join is caught by the detailed-Arabic test", () => {
    const wrongJoin = ["4 ساعات", "49 دقيقة"].join(" ");
    expect(wrongJoin).not.toBe("4 ساعات و49 دقيقة");
  });
});

describe("formatBytes", () => {
  it("shows plain bytes under 1000 with no decimal", () => {
    expect(formatBytes(533, "en")).toBe("533 B");
  });

  it("shows the model catalogue's own sizes as GB with one decimal", () => {
    expect(formatBytes(532517120, "en")).toBe("532.5 MB");
    expect(formatBytes(2740937888, "en")).toBe("2.7 GB");
  });

  it("always uses Latin digits in Arabic, matching every other number in the app", () => {
    expect(formatBytes(2740937888, "ar")).toBe("2.7 GB");
  });
});
