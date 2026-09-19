import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { buildSupportSummary, Diagnostics } from "./Diagnostics";
import { DEFAULT_SETTINGS } from "../data/source";
import type { Record } from "../data/useRecord";

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(<LanguageProvider>{ui}</LanguageProvider>);
}

function record(over: Partial<Record> = {}): Record {
  return {
    status: "ready",
    error: "",
    events: [],
    incidents: [],
    health: null,
    capabilities: [],
    rules: [],
    manifest: null,
    bundleSigned: false,
    bundleHasSignature: false,
    refreshedAt: null,
    stale: false,
    refresh: () => {},
    ...over,
  };
}

function baseParams(over: Partial<Parameters<typeof buildSupportSummary>[0]> = {}) {
  return {
    lang: "en" as const,
    appVersion: "1.1.0",
    sourceKind: "live",
    lastRefresh: "22:21:25",
    eventsTotal: 44,
    incidentsTotal: 16,
    byFamily: [["l2", 15], ["flow", 8]] as [string, number][],
    bySeverity: [["warn", 11], ["error", 6]] as [string, number][],
    ...over,
  };
}

// docs/desktop.md: "what to copy into a support request" - secret-free.
describe("buildSupportSummary", () => {
  it("includes version, source, refresh time, and the by-family/by-severity breakdowns", () => {
    const text = buildSupportSummary(baseParams());
    expect(text).toContain("1.1.0");
    expect(text).toContain("live");
    expect(text).toContain("22:21:25");
    expect(text).toContain("l2: 15");
    expect(text).toContain("flow: 8");
    expect(text).toContain("warn: 11");
    expect(text).toContain("error: 6");
  });

  it("includes the live endpoint as-is - a local pipe/socket name, not a secret", () => {
    const text = buildSupportSummary(baseParams({ endpoint: "\\\\.\\pipe\\netrewind-api" }));
    expect(text).toContain("\\\\.\\pipe\\netrewind-api");
  });

  it("reduces a bundle path to its file name only, never the full path", () => {
    const text = buildSupportSummary(
      baseParams({ sourceKind: "bundle", bundlePath: "C:\\Users\\jsmith\\Desktop\\incident-42.tar.gz" }),
    );
    expect(text).toContain("incident-42.tar.gz");
    expect(text).not.toContain("jsmith");
    expect(text).not.toContain("Desktop");
  });

  it("reduces a POSIX bundle path to its file name only", () => {
    const text = buildSupportSummary(baseParams({ sourceKind: "bundle", bundlePath: "/home/jsmith/incident-42.tar.gz" }));
    expect(text).toContain("incident-42.tar.gz");
    expect(text).not.toContain("jsmith");
  });

  it("omits the endpoint line entirely for demo/bundle mode, not an empty value", () => {
    const text = buildSupportSummary(baseParams({ sourceKind: "demo" }));
    expect(text).not.toMatch(/Endpoint/i);
  });

  it("reads in Arabic when asked, with the same values", () => {
    const text = buildSupportSummary(baseParams({ lang: "ar" }));
    expect(text).toContain("إصدار التطبيق");
    expect(text).toContain("1.1.0");
  });
});

describe("Diagnostics copy button", () => {
  it("shows a live preview of the summary and copies it on click", async () => {
    const writeText = vi.fn(async (_text: string) => {});
    Object.assign(navigator, { clipboard: { writeText } });

    english(
      <Diagnostics
        events={[]}
        incidents={[]}
        settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
        record={record()}
      />,
    );

    const preview = screen.getByLabelText("Support summary") as HTMLTextAreaElement;
    expect(preview).toHaveAttribute("readonly");
    expect(preview.value).toContain("live");

    fireEvent.click(screen.getByText("Copy summary"));
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
    expect(writeText.mock.calls[0][0]).toBe(preview.value);
    expect(await screen.findByText("Copied")).toBeInTheDocument();
  });

  it("shows a failure message rather than crashing when the clipboard API rejects", async () => {
    Object.assign(navigator, { clipboard: { writeText: vi.fn(async () => Promise.reject(new Error("denied"))) } });

    english(
      <Diagnostics events={[]} incidents={[]} settings={{ ...DEFAULT_SETTINGS, kind: "live" }} record={record()} />,
    );

    fireEvent.click(screen.getByText("Copy summary"));
    expect(await screen.findByText("Could not copy")).toBeInTheDocument();
  });
});
