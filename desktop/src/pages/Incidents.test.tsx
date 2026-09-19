import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { Incidents, contextForIncidentExport } from "./Incidents";
import type { Incident } from "../types";

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(<LanguageProvider>{ui}</LanguageProvider>);
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
});
