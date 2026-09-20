import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { AiSessionProvider } from "../data/aiSession";
import { DEFAULT_AI_SETTINGS, writeAiSettings } from "../data/aiSettings";
import { Incidents, contextForIncidentExport, filterAndSortIncidents, selectAiEvents, selectAiHistory } from "./Incidents";
import type { Incident, NetRewindEvent } from "../types";

// Every page eventually sits under both providers in App.tsx - matched
// here so a test rendering something that transitively uses
// useAiSession() (AiAssistantPanel, reached only when `events` is passed
// and non-empty) does not throw "used outside AiSessionProvider" just
// because this helper predates that panel existing.
function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  window.localStorage.removeItem("netrewind.ai");
  return render(
    <LanguageProvider>
      <AiSessionProvider>{ui}</AiSessionProvider>
    </LanguageProvider>,
  );
}

function event(over: Partial<NetRewindEvent> = {}): NetRewindEvent {
  return {
    event_id: "e1",
    schema_v: 1,
    ts_wall: Date.parse("2026-09-19T10:01:00Z") * 1e6,
    ts_mono: 0,
    observer_id: "obs",
    source: "netlink",
    kind: "link.down",
    severity: "warn",
    confidence: 80,
    subject: { kind: "iface", id: "eth0", label: "eth0" },
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

// PRD U5: what "this incident's own window" means for export, independent
// of Evidence.tsx's padding math (covered in Evidence.test.tsx).
describe("contextForIncidentExport", () => {
  it("uses opened_at and closed_at for a closed incident", () => {
    const ctx = contextForIncidentExport(incident());
    expect(ctx).toEqual({
      from: "2026-09-19T10:00:00.000Z",
      to: "2026-09-19T10:02:00.000Z",
      selection: "inc-1",
    });
  });

  it("falls back to the last chain link's time for an incident that never closed", () => {
    const ctx = contextForIncidentExport(
      incident({
        closed_at: undefined,
        chain: [
          { seq: 0, event_id: "e1", kind: "link.down", at: Date.parse("2026-09-19T10:00:00Z") * 1e6, subject: "nrlab0", relation: "", why: "" },
          { seq: 1, event_id: "e2", kind: "l2.neighbor_failed", at: Date.parse("2026-09-19T10:00:45Z") * 1e6, subject: "nrlab0", relation: "causes", why: "" },
        ],
      }),
    );
    expect(ctx.to).toBe("2026-09-19T10:00:45.000Z");
  });

  it("falls back to opened_at when the incident never closed and has no chain", () => {
    const ctx = contextForIncidentExport(incident({ closed_at: undefined, chain: [] }));
    expect(ctx.to).toBe(ctx.from);
  });
});

// execution order §9 Phase 5: "Rebuild Incidents (... search/sort/filter)".
describe("filterAndSortIncidents", () => {
  const set = [
    incident({ incident_id: "a", title: "Gateway hijacked", severity: "error", confidence: 90, rule_id: "gateway-hijack", root_cause: { kind: "l2.arp_binding_changed", entity: "10.0.0.1", event_id: "e", confidence: 90 }, opened_at: 3 }),
    incident({ incident_id: "b", title: "A link went down", severity: "warn", confidence: 80, rule_id: "link-down-isolated-hosts", root_cause: { kind: "link.down", entity: "nrlab0", event_id: "e", confidence: 80 }, opened_at: 2 }),
    incident({ incident_id: "c", title: "DHCP server rogue", severity: "notice", confidence: 60, rule_id: "rogue-dhcp-server", root_cause: { kind: "dhcp.server_seen", entity: "10.0.0.9", event_id: "e", confidence: 60 }, opened_at: 1 }),
  ];

  it("defaults to newest-first with no context", () => {
    const result = filterAndSortIncidents(set, {}, [], "en");
    expect(result.map((i) => i.incident_id)).toEqual(["a", "b", "c"]);
  });

  it("sorts by severity (error, warn, notice, info), newest first within a tie", () => {
    const result = filterAndSortIncidents(set, { sort: "severity" }, [], "en");
    expect(result.map((i) => i.incident_id)).toEqual(["a", "b", "c"]);
  });

  it("sorts by confidence, highest first", () => {
    const result = filterAndSortIncidents(set, { sort: "confidence" }, [], "en");
    expect(result.map((i) => i.incident_id)).toEqual(["a", "b", "c"]);
  });

  it("filters by severity - an absent filter matches everything", () => {
    expect(filterAndSortIncidents(set, {}, [], "en")).toHaveLength(3);
    expect(filterAndSortIncidents(set, { severities: ["error"] }, [], "en").map((i) => i.incident_id)).toEqual(["a"]);
    expect(filterAndSortIncidents(set, { severities: ["warn", "notice"] }, [], "en").map((i) => i.incident_id)).toEqual(["b", "c"]);
  });

  it("filters by family (the root cause kind's family)", () => {
    expect(filterAndSortIncidents(set, { families: ["l2"] }, [], "en").map((i) => i.incident_id)).toEqual(["a"]);
    expect(filterAndSortIncidents(set, { families: ["dhcp"] }, [], "en").map((i) => i.incident_id)).toEqual(["c"]);
  });

  it("searches the title case-insensitively", () => {
    expect(filterAndSortIncidents(set, { query: "gateway" }, [], "en").map((i) => i.incident_id)).toEqual(["a"]);
    expect(filterAndSortIncidents(set, { query: "GATEWAY" }, [], "en").map((i) => i.incident_id)).toEqual(["a"]);
  });

  it("searches the rule_id and the root-cause entity too", () => {
    expect(filterAndSortIncidents(set, { query: "rogue-dhcp" }, [], "en").map((i) => i.incident_id)).toEqual(["c"]);
    expect(filterAndSortIncidents(set, { query: "10.0.0.9" }, [], "en").map((i) => i.incident_id)).toEqual(["c"]);
  });

  it("combines a search with a severity filter (both must match)", () => {
    const result = filterAndSortIncidents(set, { query: "a", severities: ["warn"] }, [], "en");
    expect(result.map((i) => i.incident_id)).toEqual(["b"]);
  });
});

describe("Incidents page", () => {
  it("navigates to evidence with this incident's own window and id when export is clicked", () => {
    const navigate = vi.fn();
    english(<Incidents incidents={[incident()]} rules={[]} navigate={navigate} />);
    fireEvent.click(screen.getByText("Export this incident's evidence…"));
    expect(navigate).toHaveBeenCalledWith("evidence", {
      from: "2026-09-19T10:00:00.000Z",
      to: "2026-09-19T10:02:00.000Z",
      selection: "inc-1",
    });
  });

  it("shows no export button when navigate is not given", () => {
    english(<Incidents incidents={[incident()]} rules={[]} />);
    expect(screen.queryByText("Export this incident's evidence…")).not.toBeInTheDocument();
  });

  it("shows only the matching incident and hides the rest when a search query is already in the route", () => {
    english(
      <Incidents
        incidents={[incident({ incident_id: "a", title: "Gateway hijacked" }), incident({ incident_id: "b", title: "A link went down" })]}
        rules={[]}
        context={{ query: "gateway" }}
      />,
    );
    expect(screen.getByText("Gateway hijacked")).toBeInTheDocument();
    expect(screen.queryByText("A link went down")).not.toBeInTheDocument();
  });

  it("shows the no-matches empty state (distinct from the no-incidents-at-all one) when a filter matches nothing", () => {
    english(<Incidents incidents={[incident()]} rules={[]} context={{ query: "no such incident" }} />);
    expect(screen.getByText("No incidents match the current filters")).toBeInTheDocument();
    expect(screen.queryByText("No incidents in this window — that does not necessarily mean nothing went wrong")).not.toBeInTheDocument();
  });

  it("checking a severity checkbox navigates with that severity added to the route context", () => {
    const navigate = vi.fn();
    english(
      <Incidents
        incidents={[incident({ severity: "error" })]}
        rules={[]}
        context={{ query: "already set" }}
        navigate={navigate}
      />,
    );
    fireEvent.click(screen.getByRole("checkbox", { name: "error" }));
    expect(navigate).toHaveBeenCalledWith("incidents", { query: "already set", severities: ["error"] });
  });

  it("unchecking an already-selected severity removes it rather than clearing the whole filter", () => {
    const navigate = vi.fn();
    english(
      <Incidents
        incidents={[incident({ severity: "error" })]}
        rules={[]}
        context={{ severities: ["error", "warn"] }}
        navigate={navigate}
      />,
    );
    fireEvent.click(screen.getByRole("checkbox", { name: "error" }));
    expect(navigate).toHaveBeenCalledWith("incidents", { severities: ["warn"] });
  });
});

// execution order §9 Phase 5: "Rebuild Incidents (master/detail, ...)".
describe("Incidents master/detail", () => {
  const two = [
    incident({ incident_id: "a", title: "Gateway hijacked" }),
    incident({ incident_id: "b", title: "A link went down" }),
  ];

  it("renders every incident as a full card, with no compact list or panel, when nothing is selected", () => {
    english(<Incidents incidents={two} rules={[]} />);
    expect(screen.queryByRole("region", { name: /Gateway hijacked|A link went down/ })).not.toBeInTheDocument();
    // Both full cards' own titles are present (IncidentCard renders the
    // title directly, not inside the compact row).
    expect(screen.getAllByText("Gateway hijacked")).toHaveLength(1);
    expect(screen.getAllByText("A link went down")).toHaveLength(1);
  });

  it("selecting an incident's focus link switches to master/detail and shows it in the panel", () => {
    const navigate = vi.fn();
    english(<Incidents incidents={two} rules={[]} context={{}} navigate={navigate} />);
    const [firstFocusLink] = screen.getAllByText("Show this incident in a side panel with the rest of the list");
    fireEvent.click(firstFocusLink);
    expect(navigate).toHaveBeenCalledWith("incidents", { selection: "a" });
  });

  it("shows the compact list plus the InspectorPanel with the selected incident's full detail", () => {
    english(<Incidents incidents={two} rules={[]} context={{ selection: "a" }} />);
    // The panel (a labelled region) carries the selected incident's title.
    expect(screen.getByRole("region", { name: "Gateway hijacked" })).toBeInTheDocument();
    // Both incidents still appear as compact rows in the list beside it.
    expect(screen.getByRole("button", { name: /Gateway hijacked/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /A link went down/ })).toBeInTheDocument();
  });

  it("clicking a different row in the list changes the selection", () => {
    const navigate = vi.fn();
    english(<Incidents incidents={two} rules={[]} context={{ selection: "a" }} navigate={navigate} />);
    fireEvent.click(screen.getByRole("button", { name: /A link went down/ }));
    expect(navigate).toHaveBeenCalledWith("incidents", { selection: "b" });
  });

  it("clicking the already-selected row again deselects it", () => {
    const navigate = vi.fn();
    english(<Incidents incidents={two} rules={[]} context={{ selection: "a" }} navigate={navigate} />);
    fireEvent.click(screen.getByRole("button", { name: /Gateway hijacked/ }));
    expect(navigate).toHaveBeenCalledWith("incidents", { selection: undefined });
  });

  it("the panel's close button clears the selection", () => {
    const navigate = vi.fn();
    english(<Incidents incidents={two} rules={[]} context={{ selection: "a" }} navigate={navigate} />);
    fireEvent.click(screen.getByRole("button", { name: "Close panel" }));
    expect(navigate).toHaveBeenCalledWith("incidents", { selection: undefined });
  });

  it("falls back to the default full-card list when the selected id does not match any incident", () => {
    english(<Incidents incidents={two} rules={[]} context={{ selection: "no-such-id" }} />);
    expect(screen.queryByRole("region")).not.toBeInTheDocument();
    expect(screen.getAllByText("Gateway hijacked")).toHaveLength(1);
  });

  it("falls back to the default list when the selected incident has been filtered out", () => {
    const set = [
      incident({ incident_id: "a", title: "Gateway hijacked", severity: "error" }),
      incident({ incident_id: "b", title: "A link went down", severity: "warn" }),
    ];
    // "a" is selected, but the severity filter excludes it - the panel
    // must not claim to show an incident that is not even in the
    // filtered list any more.
    english(<Incidents incidents={set} rules={[]} context={{ selection: "a", severities: ["warn"] }} />);
    expect(screen.queryByRole("region")).not.toBeInTheDocument();
    expect(screen.queryByText("Gateway hijacked")).not.toBeInTheDocument();
    expect(screen.getAllByText("A link went down")).toHaveLength(1);
  });
});

describe("selectAiEvents", () => {
  it("includes events inside the padded window and excludes ones well outside it", () => {
    const inc = incident({ opened_at: Date.parse("2026-09-19T10:00:00Z") * 1e6, closed_at: Date.parse("2026-09-19T10:02:00Z") * 1e6 });
    const nearby = event({ event_id: "near", ts_wall: Date.parse("2026-09-19T09:57:00Z") * 1e6 }); // 3 min before open - inside the 5 min pad
    const farAway = event({ event_id: "far", ts_wall: Date.parse("2026-09-19T08:00:00Z") * 1e6 }); // 2 hours before - outside the pad
    const result = selectAiEvents(inc, [nearby, farAway]);
    expect(result.map((e) => e.event_id)).toEqual(["near"]);
  });

  it("always includes a chain-cited event even if it fell outside the padded window", () => {
    const inc = incident({
      opened_at: Date.parse("2026-09-19T10:00:00Z") * 1e6,
      closed_at: Date.parse("2026-09-19T10:02:00Z") * 1e6,
      chain: [{ seq: 0, event_id: "chain-1", kind: "link.down", at: Date.parse("2026-09-19T10:01:00Z") * 1e6, subject: "eth0", relation: "", why: "" }],
    });
    // Same event_id as the chain link, but stored with a timestamp far
    // outside the window - should not happen in real data, but the
    // selection must not silently drop a cited event either way.
    const chainEvent = event({ event_id: "chain-1", ts_wall: Date.parse("2026-09-19T01:00:00Z") * 1e6 });
    const result = selectAiEvents(inc, [chainEvent]);
    expect(result.map((e) => e.event_id)).toEqual(["chain-1"]);
  });

  it("returns events oldest first regardless of input order", () => {
    const inc = incident({ opened_at: Date.parse("2026-09-19T10:00:00Z") * 1e6, closed_at: Date.parse("2026-09-19T10:02:00Z") * 1e6 });
    const first = event({ event_id: "first", ts_wall: Date.parse("2026-09-19T10:00:30Z") * 1e6 });
    const second = event({ event_id: "second", ts_wall: Date.parse("2026-09-19T10:01:30Z") * 1e6 });
    expect(selectAiEvents(inc, [second, first]).map((e) => e.event_id)).toEqual(["first", "second"]);
  });
});

describe("selectAiHistory", () => {
  it("returns other incidents with the same rule, excluding the target itself", () => {
    const target = incident({ incident_id: "a", rule_id: "gateway-hijack" });
    const sameRule = incident({ incident_id: "b", rule_id: "gateway-hijack" });
    const differentRule = incident({ incident_id: "c", rule_id: "link-flap" });
    const result = selectAiHistory(target, [target, sameRule, differentRule]);
    expect(result.map((i) => i.incident_id)).toEqual(["b"]);
  });
});

describe("Incidents page: local-AI panel", () => {
  const withEvents = [incident({ incident_id: "a", title: "Gateway hijacked", chain: [{ seq: 0, event_id: "e1", kind: "link.down", at: Date.parse("2026-09-19T10:01:00Z") * 1e6, subject: "eth0", relation: "", why: "" }] })];

  it("shows the assistant panel when events are available and an incident is selected", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    english(<Incidents incidents={withEvents} rules={[]} events={[event()]} context={{ selection: "a" }} />);
    expect(screen.getByRole("heading", { name: "Local AI assistant" })).toBeInTheDocument();
    // Outside the Tauri shell (every vitest run), the panel must fail
    // closed to "this only works inside the desktop application" rather
    // than silently offering a button that could never work.
    await waitFor(() => expect(screen.getByText("This assistant only works inside the desktop application.")).toBeInTheDocument());
  });

  it("does not show the assistant panel when no events were given, even with an incident selected", () => {
    english(<Incidents incidents={withEvents} rules={[]} context={{ selection: "a" }} />);
    expect(screen.queryByRole("heading", { name: "Local AI assistant" })).not.toBeInTheDocument();
  });

  it("does not show the assistant panel when nothing is selected, even with events available", () => {
    english(<Incidents incidents={withEvents} rules={[]} events={[event()]} />);
    expect(screen.queryByRole("heading", { name: "Local AI assistant" })).not.toBeInTheDocument();
  });
});
