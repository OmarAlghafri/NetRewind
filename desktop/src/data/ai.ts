// One typed wrapper per local-AI Tauri command (desktop/src-tauri/src/lib.rs:
// agent_request, ai_status, ai_runtime_start, ai_runtime_stop, ai_analyze),
// in the same style as tauri.ts's agent_get/agent_export_bundle wrappers.

import { invoke, isAgentErrorPayload, isTauri } from "./tauri";
import type { AiAnalyzeRequest, AiAnalyzeResponse } from "./aiTypes";

export { isTauri };

/** desktop/src-tauri/src/lib.rs's AiStatusResponse. */
export interface AiRuntimeStatus {
  running: boolean;
  port: number | null;
}

/** Thrown by every function here when a Rust command rejects - the
 *  message is already "<code>: <detail>" (see lib.rs's own
 *  format!("{}: ...", ai::codes::X)); `code` is split out so a caller can
 *  look it up in aiStatusCatalogue without re-parsing the string. */
export class AiCommandError extends Error {
  constructor(
    public readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

/** lib.rs's ai_* commands reject with a plain "<code>: <detail>" string
 *  (unlike agent.rs's AgentError struct) - this splits it back into the
 *  two parts aiStatusCatalogue.messageFor expects. A rejection that is
 *  not this shape (a JS-level error, e.g. Tauri itself failing to invoke)
 *  is re-thrown unchanged rather than misrepresented as an AI-subsystem
 *  error code. */
export function toAiCommandError(e: unknown): unknown {
  if (isAgentErrorPayload(e)) return e; // agent_request still uses the AgentError shape
  if (typeof e === "string") {
    const sep = e.indexOf(":");
    if (sep > 0) {
      return new AiCommandError(e.slice(0, sep).trim(), e.trim());
    }
    return new AiCommandError(e.trim(), e.trim());
  }
  return e;
}

export async function aiStatus(): Promise<AiRuntimeStatus> {
  try {
    return await invoke<AiRuntimeStatus>("ai_status");
  } catch (e) {
    throw toAiCommandError(e);
  }
}

export interface AiRuntimeStartOptions {
  modelFileName: string;
  extraArgs?: string[];
  threads?: number;
}

export async function aiRuntimeStart(opts: AiRuntimeStartOptions): Promise<AiRuntimeStatus> {
  try {
    return await invoke<AiRuntimeStatus>("ai_runtime_start", {
      modelFileName: opts.modelFileName,
      extraArgs: opts.extraArgs ?? [],
      threads: opts.threads,
    });
  } catch (e) {
    throw toAiCommandError(e);
  }
}

export async function aiRuntimeStop(): Promise<void> {
  try {
    await invoke<void>("ai_runtime_stop");
  } catch (e) {
    throw toAiCommandError(e);
  }
}

/** Runs one analysis. `request` never carries a `server` field - the
 *  running sidecar's own url/token is attached server-side by
 *  ai_analyze itself (lib.rs), so the token this session's llama-server
 *  was started with never has to reach the frontend to be sent back. */
export async function aiAnalyze(
  request: Omit<AiAnalyzeRequest, "server">,
  timeoutSeconds?: number,
): Promise<AiAnalyzeResponse> {
  try {
    const stdout = await invoke<string>("ai_analyze", {
      requestJson: JSON.stringify(request),
      timeoutSeconds,
    });
    return JSON.parse(stdout) as AiAnalyzeResponse;
  } catch (e) {
    throw toAiCommandError(e);
  }
}

/** Redacts a report the panel composed client-side (aiReport.ts) before
 *  it reaches the clipboard - the one place that text leaves the device.
 *  Spawns `netrewind ai report` (internal/redact) so this is the same
 *  redaction a debug prompt log or a persisted follow-up thread gets,
 *  not a second implementation living in the frontend. */
export async function aiReportRedact(text: string): Promise<string> {
  try {
    return await invoke<string>("ai_report_redact", { text });
  } catch (e) {
    throw toAiCommandError(e);
  }
}

/** PUT/POST/DELETE against `/v1/notes/*` - the one write surface the
 *  record's own read-only API has (internal/api/v1/notes.go). `agentGet`
 *  (tauri.ts) stays the only way to read anything, including notes
 *  themselves; this is only ever a write. */
export async function agentRequest<T>(
  endpoint: string,
  method: "PUT" | "POST" | "DELETE",
  path: string,
  body?: unknown,
): Promise<T | null> {
  const resp = await invoke<{ status: number; body: string; headers: Record<string, string> }>("agent_request", {
    endpoint,
    method,
    path,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (resp.status === 204 || resp.body.trim() === "") return null;
  if (resp.status < 200 || resp.status >= 300) {
    let code = "http_" + resp.status;
    let message = resp.body;
    try {
      const parsed = JSON.parse(resp.body) as { error?: { code?: string; message?: string } };
      if (parsed.error) {
        code = parsed.error.code ?? code;
        message = parsed.error.message ?? message;
      }
    } catch {
      // not the API's error shape; the raw body is the best message there is
    }
    throw new AiCommandError(code, message);
  }
  return JSON.parse(resp.body) as T;
}
