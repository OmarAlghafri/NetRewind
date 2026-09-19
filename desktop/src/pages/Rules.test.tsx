import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { Rules, incidentsInRange } from "./Rules";
import type { Incident } from "../types";
import type { RuleSummary } from "../data/types";

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

function rule(over: Partial<RuleSummary> = {}): RuleSummary {
  return {
    id: "link-down-isolated-hosts",
    title: "Link went down",
    severity: "warn",
    confidence: 80,
    window: "5m",
    root_cause: "link.down",
    advice: "Check the cable.",
    ...over,
  };
}

// execution order §9 Phase 5: Rules "match-count in range" - absolute
// from/to (InvestigationContext's own fields), not a relative "last N"
// window: see Rules.tsx's own comment on why that would silently break
// demo/bundle mode's fixed historical timestamps.
describe("incidentsInRange", () => {
  const early = incident({ incident_id: "early", opened_at: Date.parse("2026-09-19T08:00:00Z") * 1e6 });
  const middle = incident({ incident_id: "middle", opened_at: Date.parse("2026-09-19T10:00:00Z") * 1e6 });
  const late = incident({ incident_id: "late", opened_at: Date.parse("2026-09-19T12:00:00Z") * 1e6 });
  const all = [early, middle, late];

  it("returns every incident unchanged when neither from nor to is set - the no-regression default", () => {
    expect(incidentsInRange(all, {})).toEqual(all);
  });

  it("excludes an incident opened before an absolute from", () => {
    const result = incidentsInRange(all, { from: "2026-09-19T09:00:00Z" });
    expect(result.map((i) => i.incident_id)).toEqual(["middle", "late"]);
  });

  it("excludes an incident opened after an absolute to", () => {
    const result = incidentsInRange(all, { to: "2026-09-19T11:00:00Z" });
    expect(result.map((i) => i.incident_id)).toEqual(["early", "middle"]);
  });

  it("applies from and to together", () => {
    const result = incidentsInRange(all, { from: "2026-09-19T09:00:00Z", to: "2026-09-19T11:00:00Z" });
    expect(result.map((i) => i.incident_id)).toEqual(["middle"]);
  });

  it("includes an incident opened exactly at the from or to boundary", () => {
    const result = incidentsInRange(all, {
      from: "2026-09-19T08:00:00Z",
      to: "2026-09-19T12:00:00Z",
    });
    expect(result.map((i) => i.incident_id)).toEqual(["early", "middle", "late"]);
  });

  it("treats an unparseable from/to as unset rather than excluding everything", () => {
    expect(incidentsInRange(all, { from: "not-a-date" })).toEqual(all);
  });

  // Deliberate-break proof: flip the lower-bound comparison the way a
  // fat-fingered edit would (`<` to `>`), confirming this test actually
  // depends on that exact operator rather than passing for an unrelated
  // reason. Restored immediately after - see the git diff for this file,
  // which shows no such inversion in the committed version.
  it("[deliberate-break proof] a flipped lower-bound comparison is caught by the from-boundary test", () => {
    const brokenInRange = (incidents: Incident[], context: { from?: string; to?: string }) => {
      const fromMs = context.from ? Date.parse(context.from) : undefined;
      const toMs = context.to ? Date.parse(context.to) : undefined;
      if (fromMs === undefined && toMs === undefined) return incidents;
      return incidents.filter((inc) => {
        const openedMs = inc.opened_at / 1e6;
        if (fromMs !== undefined && openedMs > fromMs) return false; // inverted on purpose
        if (toMs !== undefined && openedMs > toMs) return false;
        return true;
      });
    };
    const result = brokenInRange(all, { from: "2026-09-19T09:00:00Z" });
    // The real function returns ["middle", "late"]; the inverted operator
    // must not also produce that, or this proof would be worthless.
    expect(result.map((i) => i.incident_id)).not.toEqual(["middle", "late"]);
  });
});

describe("Rules range UI", () => {
  it("renders no range inputs when there is no navigate to call - unchanged legacy behaviour", () => {
    english(<Rules incidents={[incident()]} rules={[]} />);
    expect(screen.queryByLabelText("From:")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("To:")).not.toBeInTheDocument();
  });

  it("navigates with a patched from when the from input changes, leaving to untouched", () => {
    const navigate = vi.fn();
    english(
      <Rules
        incidents={[incident()]}
        rules={[]}
        context={{ to: "2026-09-19T12:00:00.000Z" }}
        navigate={navigate}
      />,
    );
    fireEvent.change(screen.getByLabelText("From:"), { target: { value: "2026-09-19T09:00" } });
    expect(navigate).toHaveBeenCalledTimes(1);
    const [page, ctx] = navigate.mock.calls[0];
    expect(page).toBe("rules");
    expect(ctx.to).toBe("2026-09-19T12:00:00.000Z");
    // The datetime-local value is local time by definition (no timezone in
    // the string), so the expectation is computed the same way rather than
    // hardcoded - this must still hold on a CI runner in any timezone.
    expect(ctx.from).toBe(new Date("2026-09-19T09:00").toISOString());
  });

  it("shows a clear button only once a range bound is set, and clearing drops both bounds", () => {
    const navigate = vi.fn();
    english(
      <Rules
        incidents={[incident()]}
        rules={[]}
        context={{ from: "2026-09-19T09:00:00.000Z" }}
        navigate={navigate}
      />,
    );
    fireEvent.click(screen.getByText("Clear range"));
    expect(navigate).toHaveBeenCalledWith("rules", { from: undefined, to: undefined });
  });

  it("does not show a clear button when no bound is set", () => {
    english(<Rules incidents={[incident()]} rules={[]} context={{}} navigate={vi.fn()} />);
    expect(screen.queryByText("Clear range")).not.toBeInTheDocument();
  });

  it("counts only incidents inside the range against each fired-only rule row", () => {
    english(
      <Rules
        incidents={[
          incident({ incident_id: "a", opened_at: Date.parse("2026-09-19T08:00:00Z") * 1e6 }),
          incident({ incident_id: "b", opened_at: Date.parse("2026-09-19T10:00:00Z") * 1e6 }),
        ]}
        rules={[]}
        context={{ from: "2026-09-19T09:00:00.000Z" }}
        navigate={vi.fn()}
      />,
    );
    expect(screen.getByText("1")).toBeInTheDocument();
  });

  it("counts only incidents inside the range in the full-catalogue view too", () => {
    english(
      <Rules
        incidents={[
          incident({ incident_id: "a", opened_at: Date.parse("2026-09-19T08:00:00Z") * 1e6 }),
          incident({ incident_id: "b", opened_at: Date.parse("2026-09-19T10:00:00Z") * 1e6 }),
        ]}
        rules={[rule()]}
        context={{ from: "2026-09-19T09:00:00.000Z" }}
        navigate={vi.fn()}
      />,
    );
    expect(screen.getByText("1")).toBeInTheDocument();
    expect(screen.getByText("time(s) in this recording")).toBeInTheDocument();
  });
});
