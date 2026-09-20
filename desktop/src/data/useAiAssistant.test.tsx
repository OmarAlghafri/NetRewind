import { afterEach, describe, expect, it } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { AiSessionProvider } from "./aiSession";
import { DEFAULT_AI_SETTINGS, type AiSettings } from "./aiSettings";
import { useAiAssistant } from "./useAiAssistant";
import type { Incident, NetRewindEvent } from "../types";

type Invoke = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;

function installShell(invoke: Invoke) {
  (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = { invoke };
}
function removeShell() {
  delete (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}
afterEach(() => removeShell());

function wrapper({ children }: { children: ReactNode }) {
  return <AiSessionProvider>{children}</AiSessionProvider>;
}

const incident: Incident = {
  incident_id: "inc-1",
  opened_at: 1000,
  status: "closed",
  title: "test",
  severity: "warn",
  confidence: 75,
  root_cause: { kind: "l2.arp_binding_changed", entity: "10.0.0.1", event_id: "e1", confidence: 75 },
  chain: [{ seq: 0, event_id: "e1", kind: "l2.arp_binding_changed", at: 1000, subject: "10.0.0.1", relation: "", why: "" }],
  rule_id: "gateway-hijack",
};

const configuredSettings: AiSettings = { ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" };

// Stable module-level references, not `[]` written inline at each call
// site: an inline literal is a *new* array on every render, which alone
// would force useAiAssistant's internal useCallback to recompute every
// time regardless of whether its dependency list is actually complete -
// masking exactly the stale-closure bug these tests exist to catch (see
// useAiAssistant.ts's own comment on why `session` must be a real
// dependency, found via this exact test suite).
const EMPTY_EVENTS: NetRewindEvent[] = [];
const EMPTY_HISTORY: Incident[] = [];

describe("useAiAssistant outside the shell", () => {
  it("reports shell_required", () => {
    removeShell();
    const { result } = renderHook(() => useAiAssistant(incident, EMPTY_EVENTS, EMPTY_HISTORY, "en", configuredSettings), { wrapper });
    expect(result.current.state).toBe("shell_required");
  });
});

describe("useAiAssistant when the feature is off or unconfigured", () => {
  it("reports disabled when settings.enabled is false", () => {
    installShell(async () => ({ running: false, port: null }));
    const { result } = renderHook(() => useAiAssistant(incident, EMPTY_EVENTS, EMPTY_HISTORY, "en", DEFAULT_AI_SETTINGS), { wrapper });
    expect(result.current.state).toBe("disabled");
  });

  it("reports not_configured when enabled but no model file name is set", () => {
    installShell(async () => ({ running: false, port: null }));
    const settings: AiSettings = { ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "" };
    const { result } = renderHook(() => useAiAssistant(incident, EMPTY_EVENTS, EMPTY_HISTORY, "en", settings), { wrapper });
    expect(result.current.state).toBe("not_configured");
  });
});

describe("useAiAssistant's analyze flow", () => {
  it("goes idle -> analyzing -> answered on a clean run, starting the runtime first if needed", async () => {
    const calls: string[] = [];
    installShell(async (cmd) => {
      calls.push(cmd);
      if (cmd === "ai_status") return { running: false, port: null };
      if (cmd === "ai_runtime_start") return { running: true, port: 1234 };
      if (cmd === "ai_analyze") {
        return JSON.stringify({
          version: 1,
          verdict: "answered",
          guardrail: { refuse: false, ceiling: 75 },
          handles: [],
          output: { summary: "ok", ranked_hypotheses: [], evidence_handles: [], counter_evidence: [], unknowns: [], confidence_ceiling: 75, next_checks: [] },
          validation: { ok: true, retried: false, first_attempt_valid: true, violations: [] },
          timing: { prompt_ms: 1, predicted_ms: 2, total_ms: 3 },
        });
      }
      throw new Error("unexpected command " + cmd);
    });

    const { result } = renderHook(() => useAiAssistant(incident, EMPTY_EVENTS, EMPTY_HISTORY, "en", configuredSettings), { wrapper });
    await waitFor(() => expect(result.current.state).toBe("idle"));

    act(() => result.current.analyze());
    expect(result.current.state).toBe("analyzing");

    await waitFor(() => expect(result.current.state).toBe("answered"));
    expect(result.current.response?.output.summary).toBe("ok");
    expect(calls).toContain("ai_runtime_start");
    expect(calls).toContain("ai_analyze");
  });

  it("skips ai_runtime_start when a sidecar is already running", async () => {
    const calls: string[] = [];
    installShell(async (cmd) => {
      calls.push(cmd);
      if (cmd === "ai_status") return { running: true, port: 5555 };
      if (cmd === "ai_analyze") {
        return JSON.stringify({
          version: 1,
          verdict: "insufficient_evidence",
          guardrail: { refuse: true, ceiling: 100, reasons: [{ code: "gap_in_window" }] },
          handles: [],
          output: { summary: "", ranked_hypotheses: [], evidence_handles: [], counter_evidence: [], unknowns: [], confidence_ceiling: 0, next_checks: [] },
          validation: { ok: true, retried: false, first_attempt_valid: true, violations: [] },
          timing: { prompt_ms: 0, predicted_ms: 0, total_ms: 0 },
        });
      }
      throw new Error("unexpected command " + cmd);
    });

    const { result } = renderHook(() => useAiAssistant(incident, EMPTY_EVENTS, EMPTY_HISTORY, "en", configuredSettings), { wrapper });
    await waitFor(() => expect(result.current.state).toBe("idle"));

    act(() => result.current.analyze());
    await waitFor(() => expect(result.current.state).toBe("insufficient_evidence"));
    expect(calls).not.toContain("ai_runtime_start");
  });

  it("reports an error state with the code split out when the analyze call rejects", async () => {
    installShell(async (cmd) => {
      if (cmd === "ai_status") return { running: true, port: 1 };
      if (cmd === "ai_analyze") throw "analysis_timeout: netrewind.exe did not finish within 180s";
      throw new Error("unexpected command " + cmd);
    });
    const { result } = renderHook(() => useAiAssistant(incident, EMPTY_EVENTS, EMPTY_HISTORY, "en", configuredSettings), { wrapper });
    await waitFor(() => expect(result.current.state).toBe("idle"));

    act(() => result.current.analyze());
    await waitFor(() => expect(result.current.state).toBe("error"));
    expect(result.current.errorCode).toBe("analysis_timeout");
  });

  it("keeps separate incidents' analyze results independent when re-rendered with a different incident", async () => {
    installShell(async (cmd) => {
      if (cmd === "ai_status") return { running: true, port: 1 };
      if (cmd === "ai_analyze") {
        return JSON.stringify({
          version: 1,
          verdict: "refused_by_model",
          guardrail: { refuse: false, ceiling: 50 },
          handles: [],
          output: { summary: "", ranked_hypotheses: [], evidence_handles: [], counter_evidence: [], unknowns: ["nothing conclusive"], confidence_ceiling: 0, next_checks: [] },
          validation: { ok: true, retried: false, first_attempt_valid: true, violations: [] },
          timing: { prompt_ms: 0, predicted_ms: 0, total_ms: 0 },
        });
      }
      throw new Error("unexpected command " + cmd);
    });
    const other: Incident = { ...incident, incident_id: "inc-2" };

    const { result, rerender } = renderHook(
      ({ inc }: { inc: Incident }) => useAiAssistant(inc, EMPTY_EVENTS, EMPTY_HISTORY, "en", configuredSettings),
      { wrapper, initialProps: { inc: incident } },
    );
    await waitFor(() => expect(result.current.state).toBe("idle"));
    act(() => result.current.analyze());
    await waitFor(() => expect(result.current.state).toBe("refused_by_model"));

    rerender({ inc: other });
    expect(result.current.state).toBe("idle"); // inc-2 has no entry yet - must not show inc-1's result
  });
});
