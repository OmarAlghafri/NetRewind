import { expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { AiSessionProvider } from "../data/aiSession";
import { Incidents } from "./Incidents";
import type { Incident, NetRewindEvent } from "../types";

// Deliberately does NOT mock ../data/aiFeature - proves Incidents.tsx's
// own page-level gate (AI_FEATURE_ENABLED && events.length > 0) hides the
// panel entirely against the real, currently-false constant, on top of
// (not instead of) useAiAssistant's own gate covered elsewhere.

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(
    <LanguageProvider>
      <AiSessionProvider>{ui}</AiSessionProvider>
    </LanguageProvider>,
  );
}

const incident: Incident = {
  incident_id: "a",
  opened_at: 1000,
  status: "closed",
  title: "Gateway hijacked",
  severity: "warn",
  confidence: 75,
  root_cause: { kind: "l2.arp_binding_changed", entity: "10.0.0.1", event_id: "e1", confidence: 75 },
  chain: [{ seq: 0, event_id: "e1", kind: "link.down", at: 1000, subject: "eth0", relation: "", why: "" }],
  rule_id: "gateway-hijack",
};

const event: NetRewindEvent = {
  event_id: "e1",
  schema_v: 1,
  ts_wall: 1000,
  ts_mono: 0,
  observer_id: "obs",
  source: "netlink",
  kind: "link.down",
  severity: "warn",
  confidence: 80,
  subject: { kind: "iface", id: "eth0", label: "eth0" },
};

it("never renders the local-AI panel while the real feature flag is off, even with events and a selection", () => {
  english(<Incidents incidents={[incident]} rules={[]} events={[event]} context={{ selection: "a" }} />);
  expect(screen.queryByRole("heading", { name: "Local AI assistant" })).not.toBeInTheDocument();
});
