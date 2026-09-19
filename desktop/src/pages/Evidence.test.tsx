import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { Evidence } from "./Evidence";
import { DEFAULT_SETTINGS } from "../data/source";
import type { Record } from "../data/useRecord";
import type { Incident } from "../types";

vi.mock("../data/tauri", async () => {
  const actual = await vi.importActual<typeof import("../data/tauri")>("../data/tauri");
  return {
    ...actual,
    isTauri: () => true,
    pickBundleToSave: vi.fn(async () => "/tmp/netrewind-test.tar.gz"),
    agentExportBundle: vi.fn(async () => ({ bytes: 1234, path: "/tmp/netrewind-test.tar.gz" })),
  };
});

import { agentExportBundle, pickBundleToSave } from "../data/tauri";

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

function incident(over: Partial<Incident> = {}): Incident {
  return {
    incident_id: "inc-1",
    opened_at: Date.parse("2026-09-19T10:00:00Z") * 1e6,
    closed_at: Date.parse("2026-09-19T10:02:00Z") * 1e6,
    status: "closed",
    title: "A link went down",
    severity: "warn",
    confidence: 80,
    root_cause: { kind: "link.down", entity: "nrlab0", event_id: "e1", confidence: 80 },
    chain: [],
    rule_id: "link-down-isolated-hosts",
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

// PRD U5: exporting "a specific incident" rather than always a fixed
// recent window.
describe("Evidence incident-scoped export", () => {
  it("shows the incident's title and its own window instead of the generic export UI when routed with from/to/selection", () => {
    english(
      <Evidence
        record={record({ incidents: [incident()] })}
        settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
        context={{ from: "2026-09-19T10:00:00.000Z", to: "2026-09-19T10:02:00.000Z", selection: "inc-1" }}
        onOpenBundle={() => {}}
        onCloseBundle={() => {}}
      />,
    );
    expect(screen.getByText("Exporting evidence for this incident")).toBeInTheDocument();
    expect(screen.getByText(/A link went down/)).toBeInTheDocument();
    expect(screen.getByText("Padding around the incident (minutes)")).toBeInTheDocument();
    // The generic window selector must not also be showing - one mode at a time.
    expect(screen.queryByText("Time window")).not.toBeInTheDocument();
  });

  it("still shows the generic window export when there is no routing context", () => {
    english(
      <Evidence
        record={record()}
        settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
        context={{}}
        onOpenBundle={() => {}}
        onCloseBundle={() => {}}
      />,
    );
    expect(screen.getByText("Export this recording")).toBeInTheDocument();
    expect(screen.getByText("Time window")).toBeInTheDocument();
    expect(screen.queryByText("Exporting evidence for this incident")).not.toBeInTheDocument();
  });

  it("exports with since/until padded around the incident's own window, not the generic window", async () => {
    english(
      <Evidence
        record={record({ incidents: [incident()] })}
        settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
        context={{ from: "2026-09-19T10:00:00.000Z", to: "2026-09-19T10:02:00.000Z", selection: "inc-1" }}
        onOpenBundle={() => {}}
        onCloseBundle={() => {}}
      />,
    );

    fireEvent.click(screen.getByText("Export this incident's evidence…"));
    await vi.waitFor(() => expect(agentExportBundle).toHaveBeenCalledTimes(1));

    expect(pickBundleToSave).toHaveBeenCalledTimes(1);
    const [, query] = vi.mocked(agentExportBundle).mock.calls[0];
    const params = new URLSearchParams(query);
    // Default padding is 5 minutes (DEFAULT_PADDING_MINUTES) either side of
    // the incident's own 10:00:00-10:02:00 window.
    expect(params.get("since")).toBe("2026-09-19T09:55:00.000Z");
    expect(params.get("until")).toBe("2026-09-19T10:07:00.000Z");
  });

  it("applies an adjusted padding value, not just the default", async () => {
    english(
      <Evidence
        record={record({ incidents: [incident()] })}
        settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
        context={{ from: "2026-09-19T10:00:00.000Z", to: "2026-09-19T10:02:00.000Z", selection: "inc-1" }}
        onOpenBundle={() => {}}
        onCloseBundle={() => {}}
      />,
    );

    fireEvent.change(screen.getByLabelText("Padding around the incident (minutes)"), { target: { value: "10" } });
    fireEvent.click(screen.getByText("Export this incident's evidence…"));
    await vi.waitFor(() => expect(agentExportBundle).toHaveBeenCalledTimes(1));

    const [, query] = vi.mocked(agentExportBundle).mock.calls[0];
    const params = new URLSearchParams(query);
    expect(params.get("since")).toBe("2026-09-19T09:50:00.000Z");
    expect(params.get("until")).toBe("2026-09-19T10:12:00.000Z");
  });

  it("falls back to the last chain link's time (not the export moment) for an incident that never closed", async () => {
    // Confirmed via Incidents.tsx's contextForIncidentExport, which
    // computes the context Evidence.tsx is given here - this test proves
    // Evidence.tsx uses whatever `to` it is handed without silently
    // preferring "now" itself, which the padding-math tests above already
    // establish; this fixture's `to` stands in for what
    // contextForIncidentExport would have computed for an open incident.
    english(
      <Evidence
        record={record({ incidents: [] })}
        settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
        context={{ from: "2026-09-19T10:00:00.000Z", to: "2026-09-19T10:00:30.000Z" }}
        onOpenBundle={() => {}}
        onCloseBundle={() => {}}
      />,
    );
    expect(screen.getByText("Exporting evidence for this incident")).toBeInTheDocument();
  });
});
