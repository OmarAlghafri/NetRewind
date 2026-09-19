// The bridge to the Rust side. Every call here fails cleanly outside Tauri
// (a plain browser running the Vite dev server has no `invoke`), so the
// pages can offer live and bundle sources only when they can actually work.

import type { Incident, NetRewindEvent } from "../types";
import type { BundleManifest } from "./types";
import type { AgentErrorPayload } from "../i18n/agentErrorCatalogue";

interface AgentResponse {
  status: number;
  body: string;
  /** Lower-cased header names ("etag", not "ETag") - see agent.rs's Response. */
  headers: Record<string, string>;
}

/** The exact code desktop/src-tauri/src/lib.rs's `agent_cancel` fails an
 *  in-flight `agent_get` with (`agent::codes::CANCELLED`) when it is told
 *  to interrupt it - matched here so a deliberate cancellation is never
 *  mistaken for the recorder actually being unreachable. */
const CANCELLED_CODE = "cancelled";

/** Thrown by `agentGetRaw`/`agentGet` when the request was interrupted by
 *  `agentCancel` before it finished - never a real transport failure, and
 *  callers should treat it as "abandoned," not as an error to show. */
export class CancelledError extends Error {
  constructor() {
    super("request was cancelled");
  }
}

/** A Tauri command's `Err(AgentError)` rejects the JS promise with exactly
 *  that struct (Tauri serialises the error value, it does not wrap it in
 *  an `Error`) - so this is a plain object check, not an `instanceof`.
 *  Exported so `useRecord.ts` can tell a real transport failure (translate
 *  it) from anything else a caught rejection might be (a plain string,
 *  a generic Error - fall back to showing it as-is). */
export function isAgentErrorPayload(e: unknown): e is AgentErrorPayload {
  return (
    typeof e === "object" &&
    e !== null &&
    typeof (e as { code?: unknown }).code === "string" &&
    typeof (e as { technical_detail?: unknown }).technical_detail === "string"
  );
}

function isCancelledRejection(e: unknown): boolean {
  return isAgentErrorPayload(e) && e.code === CANCELLED_CODE;
}

export interface BundleContents {
  manifest: BundleManifest;
  events: NetRewindEvent[];
  incidents: Incident[];
  signed: boolean;
  has_signature: boolean;
}

export interface ExportResult {
  bytes: number;
  path: string;
}

type Invoke = <T>(cmd: string, args?: Record<string, unknown>) => Promise<T>;

function tauriInternals(): { invoke: Invoke } | null {
  const w = window as unknown as { __TAURI_INTERNALS__?: { invoke?: Invoke } };
  const internals = w.__TAURI_INTERNALS__;
  if (internals && typeof internals.invoke === "function") return { invoke: internals.invoke };
  return null;
}

/** True when running inside the desktop shell, where the Rust commands exist. */
export function isTauri(): boolean {
  return tauriInternals() !== null;
}

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const internals = tauriInternals();
  if (!internals) throw new Error("not running inside the NetRewind desktop shell");
  return internals.invoke<T>(cmd, args);
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

export function agentDefaultEndpoint(): Promise<string> {
  return invoke<string>("agent_default_endpoint");
}

export interface AgentGetOptions {
  /** Extra request headers - `If-None-Match` for a conditional GET against
   *  /v1/rules or /v1/capabilities (ADR 0005). */
  headers?: Record<string, string>;
  /** Registers this call so a later `agentCancel(requestId)` can interrupt
   *  it - real cancellation of the in-flight IPC read, not just the
   *  caller choosing to ignore whatever answer eventually arrives. */
  requestId?: string;
}

export interface AgentGetResult<T> {
  status: number;
  /** `null` only for a 304 (conditional GET, nothing changed) - a real
   *  answer with no body to parse, not an error. */
  data: T | null;
  headers: Record<string, string>;
}

/** The full answer, including status/headers, for a caller that needs a
 *  304 or a header (conditional GET) - agentGet below is the simpler
 *  form for a caller that only ever expects 200. */
export async function agentGetRaw<T>(
  endpoint: string,
  path: string,
  opts: AgentGetOptions = {},
): Promise<AgentGetResult<T>> {
  let resp: AgentResponse;
  try {
    resp = await invoke<AgentResponse>("agent_get", {
      endpoint,
      path,
      headers: opts.headers,
      requestId: opts.requestId,
    });
  } catch (e) {
    if (isCancelledRejection(e)) throw new CancelledError();
    throw e;
  }
  // Normalized here, once, rather than trusting every call site to guard
  // against a missing `headers` - a real (not hypothetical) gap: nothing
  // on the Rust side omits it, but a plain-object test fixture standing in
  // for the shell (this file's own test suite, and useRecord's) has no
  // reason to know that field exists unless told to include it, and
  // silently omitting it is exactly what a fixture predating this field
  // would do.
  const headers = resp.headers ?? {};
  if (resp.status === 304) {
    return { status: 304, data: null, headers };
  }
  if (resp.status !== 200) {
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
    throw new ApiError(resp.status, code, message);
  }
  return { status: 200, data: JSON.parse(resp.body) as T, headers };
}

/** GET a JSON document from the recorder; an API error becomes an ApiError.
 *  Never sends a conditional header, so never receives a 304 - always
 *  returns real data or throws. Use `agentGetRaw` directly for a
 *  conditional GET or a cancellable request. */
export async function agentGet<T>(endpoint: string, path: string): Promise<T> {
  const result = await agentGetRaw<T>(endpoint, path);
  return result.data as T;
}

/** Interrupts an in-flight `agentGet`/`agentGetRaw` call that was given
 *  this same `requestId`. A no-op if it already finished - cancellation
 *  racing completion is normal, not an error. */
export async function agentCancel(requestId: string): Promise<void> {
  try {
    await invoke<void>("agent_cancel", { requestId });
  } catch {
    // Best-effort: if the shell itself is going away there is nothing
    // more useful to do with this failing than swallow it.
  }
}

export function agentExportBundle(endpoint: string, query: string, dest: string): Promise<ExportResult> {
  return invoke<ExportResult>("agent_export_bundle", { endpoint, query, dest });
}

export function bundleOpen(path: string, publicKey: string): Promise<BundleContents> {
  return invoke<BundleContents>("bundle_open", { path, publicKey: publicKey.trim() === "" ? null : publicKey.trim() });
}

/** Opens the native file dialogs (tauri-plugin-dialog); null when cancelled. */
export async function pickBundleToOpen(): Promise<string | null> {
  const { open } = await import("@tauri-apps/plugin-dialog");
  const picked = await open({
    multiple: false,
    directory: false,
    filters: [{ name: "NetRewind evidence bundle", extensions: ["gz", "tgz", "tar.gz"] }],
  });
  return typeof picked === "string" ? picked : null;
}

export async function pickBundleToSave(defaultName: string): Promise<string | null> {
  const { save } = await import("@tauri-apps/plugin-dialog");
  const picked = await save({
    defaultPath: defaultName,
    filters: [{ name: "NetRewind evidence bundle", extensions: ["tar.gz"] }],
  });
  return typeof picked === "string" ? picked : null;
}

export interface LaunchOptions {
  source: "demo" | "live" | "bundle" | null;
  endpoint: string | null;
  bundle: string | null;
  page: string | null;
  lang: "ar" | "en" | null;
  no_wizard: boolean;
}

/** The command-line options the shell was started with; empty outside it. */
export async function launchOptions(): Promise<LaunchOptions> {
  const none: LaunchOptions = { source: null, endpoint: null, bundle: null, page: null, lang: null, no_wizard: false };
  if (!isTauri()) return none;
  try {
    const o = await invoke<LaunchOptions>("launch_options");
    return {
      source: o.source === "demo" || o.source === "live" || o.source === "bundle" ? o.source : null,
      endpoint: o.endpoint ?? null,
      bundle: o.bundle ?? null,
      page: o.page ?? null,
      lang: o.lang === "ar" || o.lang === "en" ? o.lang : null,
      no_wizard: Boolean(o.no_wizard),
    };
  } catch {
    return none;
  }
}
