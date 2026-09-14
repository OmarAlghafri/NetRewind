// Where the record shown in the app comes from, and the settings that go
// with it. Persisted in localStorage (guarded, like the language choice) so
// the app reopens on the same source.

export type SourceKind = "demo" | "live" | "bundle";

export interface SourceSettings {
  kind: SourceKind;
  /** Socket path (Linux) or pipe name (Windows); empty means the platform default. */
  endpoint: string;
  /** Seconds between polls of a live recorder. */
  refreshSeconds: number;
  /** Base64 ed25519 key; when set, an unsigned bundle is refused. */
  publicKey: string;
  /** Path of the bundle currently open, for the bundle source. */
  bundlePath: string;
}

export const DEFAULT_SETTINGS: SourceSettings = {
  kind: "demo",
  endpoint: "",
  refreshSeconds: 5,
  publicKey: "",
  bundlePath: "",
};

const STORAGE_KEY = "netrewind.source";

export function readSettings(): SourceSettings {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULT_SETTINGS;
    const parsed = JSON.parse(raw) as Partial<SourceSettings>;
    return normalize({ ...DEFAULT_SETTINGS, ...parsed });
  } catch {
    return DEFAULT_SETTINGS;
  }
}

export function writeSettings(s: SourceSettings): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(normalize(s)));
  } catch {
    // a locked-down webview may refuse storage; the in-memory state still applies
  }
}

export function normalize(s: SourceSettings): SourceSettings {
  const kind: SourceKind = s.kind === "live" || s.kind === "bundle" ? s.kind : "demo";
  const refresh = Number(s.refreshSeconds);
  return {
    kind,
    endpoint: typeof s.endpoint === "string" ? s.endpoint.trim() : "",
    refreshSeconds: Number.isFinite(refresh) ? Math.min(60, Math.max(2, Math.round(refresh))) : DEFAULT_SETTINGS.refreshSeconds,
    publicKey: typeof s.publicKey === "string" ? s.publicKey.trim() : "",
    bundlePath: typeof s.bundlePath === "string" ? s.bundlePath : "",
  };
}
