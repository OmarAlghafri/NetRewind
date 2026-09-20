// One shared local-AI session for the whole app: a single ai_status poller
// (so opening the panel for incident A, then B, then back to A does not
// each time re-ask the Rust side "is anything running") and a cache keyed
// by incident id, so switching away from an incident mid-analysis and back
// finds the same in-flight or completed entry rather than starting a new
// analyze call (the plan's own "session cache prevents a second
// ai_analyze" requirement).

import { createContext, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { aiStatus, type AiRuntimeStatus } from "./ai";
import { isTauri } from "./tauri";

/** How often the shared poller re-checks whether a sidecar is running -
 *  cheap and local (no network), so a short interval costs nothing and
 *  keeps "the sidecar crashed" visible quickly. */
const STATUS_POLL_MS = 3000;

export type AiEntryState = "analyzing" | "result" | "error";

export interface AiEntry<TResult, TError> {
  state: AiEntryState;
  result?: TResult;
  error?: TError;
  /** A client-generated id for this one answer, present only once state is
   *  "result" - internal/notes.Feedback keys on it (see docs/api.md's
   *  POST /v1/notes/feedback), and the shipped analyze response carries no
   *  server-side answer id of its own to reuse. */
  answerId?: string;
}

interface AiSessionValue {
  /** null until the first poll answers; isTauri() === false short-circuits
   *  to a permanent {running: false} rather than ever polling at all. */
  runtimeStatus: AiRuntimeStatus | null;
  /** Re-runs ai_status immediately instead of waiting for the next tick -
   *  used right after ai_runtime_start/stop so the UI does not sit on
   *  stale status for up to STATUS_POLL_MS. */
  refreshStatus: () => void;
  getEntry: <T, E>(incidentId: string) => AiEntry<T, E> | undefined;
  setEntry: <T, E>(incidentId: string, entry: AiEntry<T, E>) => void;
}

const AiSessionContext = createContext<AiSessionValue | null>(null);

export function AiSessionProvider({ children }: { children: ReactNode }) {
  const [runtimeStatus, setRuntimeStatus] = useState<AiRuntimeStatus | null>(null);
  // A ref, not state: the polling effect below reads this on every tick
  // from inside a setTimeout closure, so it needs the entries map to
  // never be a stale closure over a single render's value - the exact
  // stale-closure hazard useRecord.ts's own consecutiveFailures ref
  // comment documents for the identical reason.
  const entries = useRef(new Map<string, AiEntry<unknown, unknown>>());
  // Included in the value memo's own deps below (not just used to force a
  // re-render) so the context value's reference actually changes on every
  // setEntry - correct even if a consumer sits under a memoized
  // component, which merely re-rendering this provider's non-memoized
  // children would not guarantee on its own.
  const [entriesVersion, setEntriesVersion] = useState(0);

  useEffect(() => {
    if (!isTauri()) {
      setRuntimeStatus({ running: false, port: null });
      return;
    }
    let cancelled = false;
    let timer: number | undefined;
    const poll = async () => {
      try {
        const status = await aiStatus();
        if (!cancelled) setRuntimeStatus(status);
      } catch {
        if (!cancelled) setRuntimeStatus({ running: false, port: null });
      }
      if (!cancelled) timer = window.setTimeout(poll, STATUS_POLL_MS);
    };
    void poll();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, []);

  const value = useMemo<AiSessionValue>(
    () => ({
      runtimeStatus,
      refreshStatus: () => {
        if (!isTauri()) return;
        void aiStatus()
          .then(setRuntimeStatus)
          .catch(() => setRuntimeStatus({ running: false, port: null }));
      },
      getEntry: <T, E>(incidentId: string) => entries.current.get(incidentId) as AiEntry<T, E> | undefined,
      setEntry: <T, E>(incidentId: string, entry: AiEntry<T, E>) => {
        entries.current.set(incidentId, entry as AiEntry<unknown, unknown>);
        setEntriesVersion((n) => n + 1);
      },
    }),
    // entriesVersion is listed only to force this memo (and therefore the
    // context value) to change reference on every setEntry, even though
    // no closure below reads its value directly.
    [runtimeStatus, entriesVersion],
  );

  return <AiSessionContext.Provider value={value}>{children}</AiSessionContext.Provider>;
}

export function useAiSession(): AiSessionValue {
  const ctx = useContext(AiSessionContext);
  if (!ctx) throw new Error("useAiSession must be used inside an AiSessionProvider");
  return ctx;
}
