import { useCallback } from "react";
import type { Incident, NetRewindEvent } from "../types";
import type { Lang } from "../i18n/translations";
import type { AiSettings } from "./aiSettings";
import type { AiAnalyzeResponse } from "./aiTypes";
import { AiCommandError, aiAnalyze, aiRuntimeStart } from "./ai";
import { isTauri } from "./tauri";
import { useAiSession } from "./aiSession";

export type AiPanelState =
  | "shell_required"
  | "disabled"
  | "not_configured"
  | "checking_runtime"
  | "idle"
  | "analyzing"
  | "answered"
  | "insufficient_evidence"
  | "refused_by_model"
  | "invalid"
  | "error";

export interface UseAiAssistantResult {
  state: AiPanelState;
  response?: AiAnalyzeResponse;
  errorCode?: string;
  errorMessage?: string;
  /** Only meaningful (and only shown by the panel) once state is one of
   *  the verdict/error states - re-running is always allowed, matching
   *  the plan's "button says analyze locally, never find the cause": the
   *  operator asks again, nothing runs on its own. */
  analyze: () => void;
}

/**
 * The panel's state machine for one incident, built on the session-wide
 * AiSessionProvider so switching incidents and back finds the same cached
 * entry rather than restarting. `events`/`history` are already the
 * caller's own selected window/candidate pool (Incidents.tsx builds
 * these, mirroring what the plan's own `selectEvents` does) - this hook
 * only orchestrates the runtime/analyze calls, never decides what
 * evidence to offer.
 */
export function useAiAssistant(
  incident: Incident,
  events: NetRewindEvent[],
  history: Incident[],
  lang: Lang,
  settings: AiSettings,
): UseAiAssistantResult {
  const session = useAiSession();
  const entry = session.getEntry<AiAnalyzeResponse, { code?: string; message: string }>(incident.incident_id);

  const analyze = useCallback(() => {
    session.setEntry(incident.incident_id, { state: "analyzing" });
    void (async () => {
      try {
        if (!session.runtimeStatus?.running) {
          await aiRuntimeStart({
            modelFileName: settings.modelFileName,
            threads: settings.threads > 0 ? settings.threads : undefined,
          });
          session.refreshStatus();
        }
        const response = await aiAnalyze({
          incident,
          events,
          history,
          question: "",
          lang,
          policy: { max_history: 3 },
        });
        session.setEntry(incident.incident_id, { state: "result", result: response });
      } catch (e) {
        const err =
          e instanceof AiCommandError
            ? { code: e.code, message: e.message }
            : { message: e instanceof Error ? e.message : String(e) };
        session.setEntry(incident.incident_id, { state: "error", error: err });
      }
    })();
    // `session` must be a real dependency, not omitted: it is a fresh
    // object every time AiSessionProvider's runtimeStatus/entries change
    // (see aiSession.tsx's own useMemo), and reading a `session` captured
    // by an earlier render's callback silently reads that render's stale
    // runtimeStatus.running - a real bug this exact test setup caught: a
    // sidecar already running was not detected as running, so every
    // analyze() call tried (and failed) to start a second one.
  }, [session, incident, events, history, lang, settings.modelFileName, settings.threads]);

  if (!isTauri()) {
    return { state: "shell_required", analyze };
  }
  if (!settings.enabled) {
    return { state: "disabled", analyze };
  }
  if (!settings.modelFileName) {
    return { state: "not_configured", analyze };
  }
  if (!entry) {
    return { state: session.runtimeStatus === null ? "checking_runtime" : "idle", analyze };
  }
  if (entry.state === "analyzing") {
    return { state: "analyzing", analyze };
  }
  if (entry.state === "error") {
    return { state: "error", errorCode: entry.error?.code, errorMessage: entry.error?.message, analyze };
  }
  // entry.state === "result"
  const response = entry.result;
  if (!response) return { state: "idle", analyze };
  if (response.verdict === "answered") return { state: "answered", response, analyze };
  if (response.verdict === "insufficient_evidence") return { state: "insufficient_evidence", response, analyze };
  if (response.verdict === "refused_by_model") return { state: "refused_by_model", response, analyze };
  return { state: "invalid", response, analyze };
}
