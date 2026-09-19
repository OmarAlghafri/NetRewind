import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { IncidentCard } from "./IncidentCard";
import type { Incident } from "../types";

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(<LanguageProvider>{ui}</LanguageProvider>);
}

function baseIncident(over: Partial<Incident> = {}): Incident {
  return {
    incident_id: "inc-1",
    opened_at: 0,
    status: "open",
    title: "A link went down",
    severity: "warn",
    confidence: 80,
    root_cause: { kind: "link.down", entity: "nrlab0", event_id: "e1", confidence: 80 },
    chain: [],
    rule_id: "link-down-isolated-hosts",
    ...over,
  };
}

// PRD U3: raw evidence must be reachable ("قابلة للفتح") from an incident's
// chain, not silently dropped - internal/incident.Link.Evidence existed on
// the wire and the TS type but nothing rendered it before this.
describe("IncidentCard evidence disclosure", () => {
  it("shows describe/matched_count/last_event_id with friendly labels, collapsed by default", () => {
    const incident = baseIncident({
      chain: [
        {
          seq: 0,
          event_id: "e1",
          kind: "flow.handshake_fail",
          at: 0,
          subject: "10.99.0.11",
          relation: "",
          why: "Repeated connections went unanswered.",
          evidence: {
            describe: "connections to 10.99.0.11 port 9999 went unanswered",
            matched_count: 3,
            last_event_id: "01M2DZZQ6KDC900N5S7FK4Y9Y2",
          },
        },
      ],
    });
    english(<IncidentCard incident={incident} />);

    const details = screen.getByText("Raw evidence").closest("details");
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute("open");
    expect(details).toHaveTextContent("What happened");
    expect(details).toHaveTextContent("connections to 10.99.0.11 port 9999 went unanswered");
    expect(details).toHaveTextContent("Matched");
    expect(details).toHaveTextContent("3");
    expect(details).toHaveTextContent("Last event");
    expect(details).toHaveTextContent("01M2DZZQ6KDC900N5S7FK4Y9Y2");
  });

  it("falls back to the raw key name for evidence this build does not have a label for", () => {
    const incident = baseIncident({
      chain: [
        {
          seq: 0,
          event_id: "e1",
          kind: "link.down",
          at: 0,
          subject: "nrlab0",
          relation: "",
          why: "An interface went down.",
          evidence: { some_future_field: "x" },
        },
      ],
    });
    english(<IncidentCard incident={incident} />);
    expect(screen.getByText("some_future_field")).toBeInTheDocument();
  });

  it("renders no disclosure at all when a link has no evidence", () => {
    const incident = baseIncident({
      chain: [
        { seq: 0, event_id: "e1", kind: "link.down", at: 0, subject: "nrlab0", relation: "", why: "went down" },
      ],
    });
    english(<IncidentCard incident={incident} />);
    expect(screen.queryByText("Raw evidence")).not.toBeInTheDocument();
  });
});

// PRD U5: the export trigger lives on the incident itself.
describe("IncidentCard export action", () => {
  it("calls onExport with this incident when given", () => {
    const onExport = vi.fn();
    const incident = baseIncident();
    english(<IncidentCard incident={incident} onExport={onExport} />);
    fireEvent.click(screen.getByText("Export this incident's evidence…"));
    expect(onExport).toHaveBeenCalledTimes(1);
    expect(onExport).toHaveBeenCalledWith(incident);
  });

  it("shows no export button at all when onExport is not given", () => {
    english(<IncidentCard incident={baseIncident()} />);
    expect(screen.queryByText("Export this incident's evidence…")).not.toBeInTheDocument();
  });
});
