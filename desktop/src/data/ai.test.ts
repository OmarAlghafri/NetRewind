import { afterEach, describe, expect, it } from "vitest";
import { agentRequest, aiAnalyze, aiRuntimeStart, aiRuntimeStop, aiStatus, AiCommandError } from "./ai";

type Invoke = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;

function installShell(invoke: Invoke) {
  (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = { invoke };
}

function removeShell() {
  delete (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}

afterEach(() => {
  removeShell();
});

describe("aiStatus", () => {
  it("returns the running sidecar's port", async () => {
    installShell(async (cmd) => {
      if (cmd !== "ai_status") throw new Error("unexpected command " + cmd);
      return { running: true, port: 41233 };
    });
    const status = await aiStatus();
    expect(status).toEqual({ running: true, port: 41233 });
  });

  it("splits a Rust '<code>: <detail>' rejection into an AiCommandError", async () => {
    installShell(async () => {
      throw "runtime_not_ready: llama-server.exe: did not answer within 120s";
    });
    await expect(aiStatus()).rejects.toMatchObject({
      code: "runtime_not_ready",
    });
  });

  it("rejects with an error instance a caller can catch as AiCommandError", async () => {
    installShell(async () => {
      throw "cli_missing: no staged binary";
    });
    await expect(aiStatus()).rejects.toBeInstanceOf(AiCommandError);
  });
});

describe("aiRuntimeStart", () => {
  it("passes the model file name, extra args and threads through", async () => {
    let seenArgs: Record<string, unknown> | undefined;
    installShell(async (cmd, args) => {
      seenArgs = args;
      if (cmd !== "ai_runtime_start") throw new Error("unexpected command " + cmd);
      return { running: true, port: 12345 };
    });
    const result = await aiRuntimeStart({ modelFileName: "small.gguf", extraArgs: ["--no-jinja"], threads: 4 });
    expect(result.port).toBe(12345);
    expect(seenArgs).toEqual({ modelFileName: "small.gguf", extraArgs: ["--no-jinja"], threads: 4 });
  });

  it("defaults extraArgs to an empty array when omitted", async () => {
    let seenArgs: Record<string, unknown> | undefined;
    installShell(async (_cmd, args) => {
      seenArgs = args;
      return { running: true, port: 1 };
    });
    await aiRuntimeStart({ modelFileName: "small.gguf" });
    expect(seenArgs?.extraArgs).toEqual([]);
  });
});

describe("aiRuntimeStop", () => {
  it("calls ai_runtime_stop with no arguments", async () => {
    let called = false;
    installShell(async (cmd) => {
      called = cmd === "ai_runtime_stop";
      return undefined;
    });
    await aiRuntimeStop();
    expect(called).toBe(true);
  });
});

describe("aiAnalyze", () => {
  it("sends the request as a JSON string and parses the CLI's stdout", async () => {
    let seenRequestJson: string | undefined;
    const response = {
      version: 1,
      verdict: "answered",
      guardrail: { refuse: false, ceiling: 70 },
      handles: [],
      output: { summary: "ok", ranked_hypotheses: [], evidence_handles: [], counter_evidence: [], unknowns: [], confidence_ceiling: 70, next_checks: [] },
      validation: { ok: true, retried: false, first_attempt_valid: true, violations: [] },
      timing: { prompt_ms: 1, predicted_ms: 2, total_ms: 3 },
    };
    installShell(async (cmd, args) => {
      if (cmd !== "ai_analyze") throw new Error("unexpected command " + cmd);
      seenRequestJson = args?.requestJson as string;
      return JSON.stringify(response);
    });

    const result = await aiAnalyze({ incident: null, events: [], question: "what happened?", lang: "en" });
    expect(result.verdict).toBe("answered");
    const parsed = JSON.parse(seenRequestJson ?? "{}") as Record<string, unknown>;
    expect(parsed.question).toBe("what happened?");
    // never carries a server field from the frontend - ai_analyze (Rust)
    // attaches the running sidecar's own url/token itself.
    expect(parsed.server).toBeUndefined();
  });

  it("wraps a rejection in AiCommandError with the code split out", async () => {
    installShell(async () => {
      throw "analysis_timeout: netrewind.exe did not finish within 180s";
    });
    await expect(aiAnalyze({ incident: null, events: [], question: "x", lang: "en" })).rejects.toMatchObject({
      code: "analysis_timeout",
    });
  });
});

describe("agentRequest", () => {
  it("sends the method, path and JSON-stringified body", async () => {
    let seenArgs: Record<string, unknown> | undefined;
    installShell(async (cmd, args) => {
      seenArgs = args;
      if (cmd !== "agent_request") throw new Error("unexpected command " + cmd);
      return { status: 200, body: JSON.stringify({ incident_id: "inc-1", outcome: "confirmed" }), headers: {} };
    });
    const result = await agentRequest<{ incident_id: string }>("", "PUT", "/v1/notes/incidents/inc-1", { outcome: "confirmed" });
    expect(result?.incident_id).toBe("inc-1");
    expect(seenArgs?.method).toBe("PUT");
    expect(seenArgs?.path).toBe("/v1/notes/incidents/inc-1");
    expect(JSON.parse(seenArgs?.body as string)).toEqual({ outcome: "confirmed" });
  });

  it("returns null for a 204 response", async () => {
    installShell(async () => ({ status: 204, body: "", headers: {} }));
    const result = await agentRequest("", "DELETE", "/v1/notes/incidents/inc-1");
    expect(result).toBeNull();
  });

  it("throws an ApiError-shaped error for a non-2xx status", async () => {
    installShell(async () => ({
      status: 409,
      body: JSON.stringify({ error: { code: "history_disabled", message: "history is not enabled" } }),
      headers: {},
    }));
    await expect(agentRequest("", "POST", "/v1/notes/threads/inc-1", {})).rejects.toMatchObject({
      code: "history_disabled",
    });
  });
});
