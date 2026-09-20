import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { LanguageProvider } from "../../i18n/LanguageContext";
import { AiSessionProvider } from "../../data/aiSession";
import { DEFAULT_AI_SETTINGS, writeAiSettings } from "../../data/aiSettings";
import { AiAssistantPanel } from "./AiAssistantPanel";
import type { Incident, NetRewindEvent } from "../../types";

type Invoke = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;

function installShell(invoke: Invoke) {
  (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = { invoke };
}
function removeShell() {
  delete (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}

afterEach(() => {
  removeShell();
  window.localStorage.clear();
});

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(
    <LanguageProvider>
      <AiSessionProvider>{ui}</AiSessionProvider>
    </LanguageProvider>,
  );
}

const incident: Incident = {
  incident_id: "inc-1",
  opened_at: Date.parse("2026-09-19T10:00:00Z") * 1e6,
  status: "closed",
  title: "Gateway hijacked",
  severity: "warn",
  confidence: 75,
  root_cause: { kind: "l2.arp_binding_changed", entity: "10.0.0.1", event_id: "e1", confidence: 75 },
  chain: [{ seq: 0, event_id: "e1", kind: "l2.arp_binding_changed", at: Date.parse("2026-09-19T10:00:30Z") * 1e6, subject: "10.0.0.1", relation: "", why: "" }],
  rule_id: "gateway-hijack",
};

const events: NetRewindEvent[] = [
  {
    event_id: "e1",
    schema_v: 1,
    ts_wall: Date.parse("2026-09-19T10:00:30Z") * 1e6,
    ts_mono: 0,
    observer_id: "obs",
    source: "netlink",
    kind: "l2.arp_binding_changed",
    severity: "warn",
    confidence: 75,
    subject: { kind: "host", id: "10.0.0.1", label: "10.0.0.1" },
  },
];

function installAnsweredShell(handles: { handle: string; kind: string; ref: string }[]) {
  installShell(async (cmd) => {
    if (cmd === "ai_status") return { running: true, port: 1234 };
    if (cmd === "ai_analyze") {
      return JSON.stringify({
        version: 1,
        verdict: "answered",
        guardrail: { refuse: false, ceiling: 75 },
        handles,
        output: {
          summary: "ARP binding changed on the gateway.",
          ranked_hypotheses: [{ cause: "l2.arp_binding_changed", entity: "10.0.0.1", confidence: 75 }],
          evidence_handles: handles.map((h) => h.handle),
          counter_evidence: [],
          unknowns: [],
          confidence_ceiling: 75,
          next_checks: [],
        },
        validation: { ok: true, retried: false, first_attempt_valid: true, violations: [] },
        timing: { prompt_ms: 1, predicted_ms: 2, total_ms: 3 },
      });
    }
    throw new Error("unexpected command " + cmd);
  });
}

describe("AiAssistantPanel: evidence handle navigation", () => {
  it("jumps to the Timeline, narrowed to the evidence window and highlighting the cited event", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([{ handle: "E1", kind: "event", ref: "e1" }]);
    const navigate = vi.fn();

    english(<AiAssistantPanel incident={incident} events={events} history={[]} navigate={navigate} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    const handleButton = await screen.findByText("E1");
    fireEvent.click(handleButton);

    expect(navigate).toHaveBeenCalledWith("timeline", {
      from: new Date(Date.parse("2026-09-19T10:00:30Z")).toISOString(),
      to: new Date(Date.parse("2026-09-19T10:00:30Z")).toISOString(),
      selection: "e1",
    });
  });

  it("does not navigate for a history or annotation handle - neither is a Timeline row", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([{ handle: "H1", kind: "history", ref: "inc-prior" }]);
    const navigate = vi.fn();

    english(<AiAssistantPanel incident={incident} events={events} history={[]} navigate={navigate} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    await waitFor(() => expect(screen.getByText("H1")).toBeInTheDocument());

    // A history handle renders as plain text, not a clickable button.
    expect(screen.getByText("H1").closest("button")).toBeNull();
    fireEvent.click(screen.getByText("H1"));
    expect(navigate).not.toHaveBeenCalled();
  });

  it("renders event handles as plain text (not buttons) when no navigate callback is given", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([{ handle: "E1", kind: "event", ref: "e1" }]);

    english(<AiAssistantPanel incident={incident} events={events} history={[]} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    await waitFor(() => expect(screen.getByText("E1")).toBeInTheDocument());
    expect(screen.getByText("E1").closest("button")).toBeNull();
  });
});
