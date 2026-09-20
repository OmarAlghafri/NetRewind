// The local-AI assistant's own settings: whether it is turned on at all,
// which model profile to use, thread-count override, and the two privacy
// opt-ins (persisted history, saving raw prompts for debugging). Persisted
// in localStorage exactly like source.ts's SourceSettings, and normalized
// the same defensive way (a value from an older or corrupted save must
// never crash the settings page, only fall back to a safe default).

export type AiProfile = "small" | "balanced" | "full";

export interface AiSettings {
  enabled: boolean;
  profile: AiProfile;
  /** 0 means "use the runtime's own default" (physical cores, clamped 1-8). */
  threads: number;
  /** Persist follow-up-question threads in the recorder's notes store
   *  (internal/notes) - off by default, matching the recorder's own
   *  default (ADR 0008 §13.2: "off means forgotten, not merely stop
   *  adding more" applies the moment this is turned back off too). */
  historyOptIn: boolean;
  /** Save the raw prompt sent to the model to a local debug log -
   *  off by default; a diagnostic aid, never uploaded anywhere. */
  debugSavePrompts: boolean;
}

export const DEFAULT_AI_SETTINGS: AiSettings = {
  enabled: false,
  profile: "balanced",
  threads: 0,
  historyOptIn: false,
  debugSavePrompts: false,
};

const STORAGE_KEY = "netrewind.ai";

export function readAiSettings(): AiSettings {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULT_AI_SETTINGS;
    const parsed = JSON.parse(raw) as Partial<AiSettings>;
    return normalizeAiSettings({ ...DEFAULT_AI_SETTINGS, ...parsed });
  } catch {
    return DEFAULT_AI_SETTINGS;
  }
}

export function writeAiSettings(s: AiSettings): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(normalizeAiSettings(s)));
  } catch {
    // a locked-down webview may refuse storage; the in-memory state still applies
  }
}

export function normalizeAiSettings(s: AiSettings): AiSettings {
  const profile: AiProfile = s.profile === "small" || s.profile === "full" ? s.profile : "balanced";
  const threads = Number(s.threads);
  return {
    enabled: Boolean(s.enabled),
    profile,
    threads: Number.isFinite(threads) ? Math.min(64, Math.max(0, Math.round(threads))) : DEFAULT_AI_SETTINGS.threads,
    historyOptIn: Boolean(s.historyOptIn),
    debugSavePrompts: Boolean(s.debugSavePrompts),
  };
}
