import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { eventsInRange, Timeline } from "./Timeline";
import type { NetRewindEvent } from "../types";

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(<LanguageProvider>{ui}</LanguageProvider>);
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

describe("eventsInRange", () => {
  const events = [
    event({ event_id: "e1", ts_wall: Date.parse("2026-09-19T10:00:00Z") * 1e6 }),
    event({ event_id: "e2", ts_wall: Date.parse("2026-09-19T10:05:00Z") * 1e6 }),
    event({ event_id: "e3", ts_wall: Date.parse("2026-09-19T10:10:00Z") * 1e6 }),
  ];

  it("returns every event when the context has no from/to", () => {
    expect(eventsInRange(events, {})).toHaveLength(3);
  });

  it("keeps only events within an inclusive [from, to] window", () => {
    const result = eventsInRange(events, { from: "2026-09-19T10:00:30Z", to: "2026-09-19T10:09:00Z" });
    expect(result.map((e) => e.event_id)).toEqual(["e2"]);
  });

  it("ignores an unparsable from/to rather than filtering everything out", () => {
    expect(eventsInRange(events, { from: "not-a-date" })).toHaveLength(3);
  });
});

describe("Timeline page", () => {
  it("renders every event when no context is given", () => {
    english(<Timeline events={[event({ event_id: "e1" }), event({ event_id: "e2" })]} />);
    expect(screen.getAllByText("link.down")).toHaveLength(2);
  });

  it("narrows to the context's from/to window - the local-AI evidence-click case", () => {
    english(
      <Timeline
        events={[
          event({ event_id: "e1", ts_wall: Date.parse("2026-09-19T09:00:00Z") * 1e6, kind: "l2.arp_binding_changed" }),
          event({ event_id: "e2", ts_wall: Date.parse("2026-09-19T10:00:00Z") * 1e6, kind: "link.down" }),
        ]}
        context={{ from: "2026-09-19T09:55:00Z", to: "2026-09-19T10:05:00Z" }}
      />,
    );
    expect(screen.queryByText("l2.arp_binding_changed")).not.toBeInTheDocument();
    expect(screen.getByText("link.down")).toBeInTheDocument();
  });

  it("highlights the row matching context.selection", () => {
    english(
      <Timeline
        events={[event({ event_id: "e1" }), event({ event_id: "e2", kind: "l2.arp_binding_changed" })]}
        context={{ selection: "e2" }}
      />,
    );
    const row = screen.getByText("l2.arp_binding_changed").closest(".timeline-row");
    expect(row).toHaveClass("timeline-row-selected");
    const otherRow = screen.getByText("link.down").closest(".timeline-row");
    expect(otherRow).not.toHaveClass("timeline-row-selected");
  });
});
