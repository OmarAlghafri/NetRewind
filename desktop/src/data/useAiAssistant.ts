import { useCallback, useRef, useState } from "react";
import type { Incident, NetRewindEvent } from "../types";
import type { Lang } from "../i18n/translations";
import type { AiSettings } from "./aiSettings";
import type { AiAnalyzeResponse } from "./aiTypes";
import { AiCommandError, aiAnalyze, aiRuntimeStart } from "./ai";
import { isTauri } from "./tauri";
import { useAiSession, type AiFollowUpTurn } from "./aiSession";
import { AI_FEATURE_ENABLED } from "./aiFeature";

export type AiPanelState =
  | "not_available"
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
  /** Only set once state is "answered" - see AiEntry.answerId. */
  answerId?: string;
  errorCode?: string;
  errorMessage?: string;
  /** Only meaningful (and only shown by the panel) once state is one of
   *  the verdict/error states - re-running is always allowed, matching
   *  the plan's "button says analyze locally, never find the cause": the
   *  operator asks again, nothing runs on its own. */
  analyze: () => void;
  /** Every follow-up asked so far this session, oldest first - only ever
   *  set (and only ever non-empty) once state is "answered" (a follow-up
   *  re-uses the same evidence as the main answer, so it makes no sense
   *  before one exists). */
  followUps?: AiFollowUpTurn<AiAnalyzeResponse>[];
  /** True while a follow-up request is in flight - separate from `state`
   *  itself, which stays "answered" throughout (unlike the initial
   *  analyze(), asking a follow-up must not hide the answer already on
   *  screen while it runs). Only meaningful once state is "answered". */
  followUpPending?: boolean;
  /** Re-runs the whole analysis with a real question instead of the
   *  default blank one, appending the result to `followUps` - see
   *  AiFollowUpTurn's own doc comment for why this is independent turns,
   *  not a threaded conversation the model itself remembers. Only present
   *  once state is "answered"; a no-op while one is already pending. */
  askFollowUp?: (question: string) => void;
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
  const [followUpPending, setFollowUpPending] = useState(false);
  // The guard below reads this ref, not the `followUpPending` state: two
  // askFollowUp() calls fired back to back in the same tick (a double
  // click before React re-renders) would otherwise both close over the
  // same pre-update `followUpPending`, since setFollowUpPending(true)
  // does not take effect for a *second* call to the same callback
  // instance until the next render - the identical stale-closure hazard
  // documented on aiSession.tsx's own `entries` ref, here for a boolean
  // instead of a map.
  const followUpPendingRef = useRef(false);

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
        session.setEntry(incident.incident_id, { state: "result", result: response, answerId: crypto.randomUUID() });
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

  const askFollowUp = useCallback(
    (question: string) => {
      const trimmed = question.trim();
      if (!trimmed || followUpPendingRef.current) return;
      followUpPendingRef.current = true;
      setFollowUpPending(true);
      void (async () => {
        try {
          const response = await aiAnalyze({
            incident,
            events,
            history,
            question: trimmed,
            lang,
            policy: { max_history: 3 },
          });
          const current = session.getEntry<AiAnalyzeResponse, { code?: string; message: string }>(incident.incident_id);
          const nextFollowUps = [...(current?.followUps ?? []), { question: trimmed, answerId: crypto.randomUUID(), result: response }];
          session.setEntry(incident.incident_id, { ...current, state: "result", followUps: nextFollowUps });
        } catch {
          // A failed follow-up leaves the main answer and any earlier
          // follow-ups exactly as they were - it is not a reason to
          // discard what already worked. The panel's own input stays
          // filled so the operator can just retry.
        } finally {
          followUpPendingRef.current = false;
          setFollowUpPending(false);
        }
      })();
      // `session` is read fresh inside the async closure via
      // session.getEntry (not captured from this render), so this
      // callback itself does not need `session` as a dependency the way
      // `analyze` above does - there is no stale runtimeStatus read here,
      // only a stale `current.followUps` list, which re-reading at call
      // time already avoids.
    },
    [incident, events, history, lang],
  );

  // Checked before isTauri() and every other state: while the compile-time
  // gate is off, nothing else about this hook's state matters - not even
  // whether the shell is reachable, since there would be nothing to reach
  // for regardless.
  if (!AI_FEATURE_ENABLED) {
    return { state: "not_available", analyze };
  }
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
  if (response.verdict === "answered") {
    return {
      state: "answered",
      response,
      answerId: entry.answerId,
      analyze,
      followUps: entry.followUps ?? [],
      followUpPending,
      askFollowUp,
    };
  }
  if (response.verdict === "insufficient_evidence") return { state: "insufficient_evidence", response, analyze };
  if (response.verdict === "refused_by_model") return { state: "refused_by_model", response, analyze };
  return { state: "invalid", response, analyze };
}
