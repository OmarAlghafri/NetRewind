// The bridge to the Rust side. Every call here fails cleanly outside Tauri
// (a plain browser running the Vite dev server has no `invoke`), so the
// pages can offer live and bundle sources only when they can actually work.

import type { Incident, NetRewindEvent } from "../types";
import type { BundleManifest } from "./types";

interface AgentResponse {
  status: number;
  body: string;
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

/** GET a JSON document from the recorder; an API error becomes an ApiError. */
export async function agentGet<T>(endpoint: string, path: string): Promise<T> {
  const resp = await invoke<AgentResponse>("agent_get", { endpoint, path });
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
  return JSON.parse(resp.body) as T;
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
