import { useCallback, useEffect, useRef, useState } from "react";
import type { Incident, NetRewindEvent } from "../types";
import type { BundleAnnotation, Capability, Health, RuleSummary, BundleManifest } from "./types";
import type { SourceSettings } from "./source";
import type { AgentErrorPayload } from "../i18n/agentErrorCatalogue";
import { loadDemoEvents, loadDemoIncidents } from "../demo/loadDemoData";
import { CancelledError, agentCancel, agentGetRaw, bundleOpen, isAgentErrorPayload, isTauri } from "./tauri";

/** How far back a live view reaches on its first load. The API's own
 *  default is one hour; a viewer opened after an overnight incident needs
 *  the previous day. Every poll after the first uses a cursor instead
 *  (ADR 0005) - this only bounds the initial fill. */
export const LIVE_WINDOW_HOURS = 24;
/** Upper bound on events fetched per request; the recorder folds repeats,
 *  so this covers a busy day on a segment with churn even before delta
 *  polling makes most requests return far fewer rows than this. */
export const LIVE_EVENT_LIMIT = 5000;
/** Backoff after a failed poll: doubles each consecutive failure, capped
 *  here, with up to 30% jitter so many viewers failing at once do not all
 *  retry in lockstep. */
const BACKOFF_BASE_MS = 2000;
const BACKOFF_MAX_MS = 60000;

export type RecordStatus = "loading" | "ready" | "error";

export interface Record {
  status: RecordStatus;
  /** Set when status is "error": what went wrong. `"shell_required"`/
   *  `"no_bundle"` are internal sentinels `SourceBanner.tsx` translates by
   *  name; a live poll failure is the structured `AgentErrorPayload` a
   *  Tauri command now rejects with (ADR 0004 §4.5) so the banner can show
   *  a translated sentence instead of `agent.rs`'s own English one; a
   *  bundle-open failure (a different Rust command, out of this ADR's
   *  scope) is still a plain string. */
  error: string | AgentErrorPayload;
  events: NetRewindEvent[];
  incidents: Incident[];
  /** Live source only. */
  health: Health | null;
  /** Live source (the registry) or a bundle's manifest. */
  capabilities: Capability[];
  /** Live source only: the full correlation catalogue. */
  rules: RuleSummary[];
  /** Bundle source only. */
  manifest: BundleManifest | null;
  bundleSigned: boolean;
  bundleHasSignature: boolean;
  /** Bundle source only; `null` when the opened bundle carries no
   *  notes.json at all (an older bundle, or one exported with notes
   *  disabled) - distinct from an empty array, which means the sender's
   *  own notes.Store was queried and genuinely had none. Read-only,
   *  labelled "from this bundle" wherever shown - ADR 0008. */
  bundleNotes: BundleAnnotation[] | null;
  /** When the current data was fetched. */
  refreshedAt: Date | null;
  /** True on an error that still has previous data on screen - "showing
   *  data as of refreshedAt, retrying" rather than a blank error page. */
  stale: boolean;
  refresh: () => void;
}

const EMPTY: Omit<Record, "refresh"> = {
  status: "loading",
  error: "",
  events: [],
  incidents: [],
  health: null,
  capabilities: [],
  rules: [],
  manifest: null,
  bundleSigned: false,
  bundleHasSignature: false,
  bundleNotes: null,
  refreshedAt: null,
  stale: false,
};

interface EventsResponse {
  events: NetRewindEvent[];
  next_cursor?: string;
  has_more?: boolean;
}
interface IncidentsResponse {
  incidents: Incident[];
  next_cursor?: string;
  has_more?: boolean;
}
interface CapabilitiesResponse {
  capabilities: Capability[];
}
interface RulesResponse {
  rules: RuleSummary[];
}

/** Replaces or appends each item in `delta` by its own id, preserving
 *  `existing`'s order for anything already present - the shape a fold
 *  needs (an existing event's count/ts_last changed in place, its
 *  position among what's already on screen should not) and a plain new
 *  row needs (append after everything already known, which is correct
 *  because a delta's new rows are, by construction, newer than the
 *  cursor's previous position). */
function mergeById<T extends { event_id?: string; incident_id?: string }>(
  existing: T[],
  delta: T[],
  id: (item: T) => string,
): T[] {
  if (delta.length === 0) return existing;
  const index = new Map<string, number>();
  const out = existing.slice();
  out.forEach((item, i) => index.set(id(item), i));
  for (const item of delta) {
    const key = id(item);
    const at = index.get(key);
    if (at !== undefined) {
      out[at] = item;
    } else {
      index.set(key, out.length);
      out.push(item);
    }
  }
  return out;
}

interface LiveState {
  events: NetRewindEvent[];
  incidents: Incident[];
  health: Health | null;
  capabilities: Capability[];
  rules: RuleSummary[];
  eventsCursor: string | null;
  incidentsCursor: string | null;
  rulesETag: string | null;
  capsETag: string | null;
}

const EMPTY_LIVE: LiveState = {
  events: [],
  incidents: [],
  health: null,
  capabilities: [],
  rules: [],
  eventsCursor: null,
  incidentsCursor: null,
  rulesETag: null,
  capsETag: null,
};

/** Every request a poll issues shares this prefix so a superseded poll's
 *  requests can all be found and cancelled by generation number, without
 *  guessing which of the (up to) five sub-calls were still in flight. */
function subcallIds(generation: number): string[] {
  return ["health", "caps", "events", "incidents", "rules"].map((name) => `poll-${generation}-${name}`);
}

/** Cancels whatever a previous, now-superseded poll may still have in
 *  flight - real cancellation of the IPC read (desktop/src-tauri's
 *  agent_cancel), not only useRecord's own generation counter choosing to
 *  ignore the eventual answer. Safe to call for requests that already
 *  finished: agentCancel/agent_cancel are no-ops in that case, not errors. */
function cancelPoll(generation: number): void {
  if (!isTauri()) return;
  for (const id of subcallIds(generation)) void agentCancel(id);
}

export interface LivePollResult {
  record: Omit<Record, "refresh" | "status" | "error" | "stale">;
  /** What the *next* call's `prev` should be - carries the new
   *  cursors/ETags forward. Kept separate from `record` because the
   *  public `Record` shape the UI reads has no business knowing cursors
   *  or ETags exist. */
  next: LiveState;
}

/**
 * Fetches one live poll. `prev` is the previous LiveState (EMPTY_LIVE for
 * the first call): its cursors/ETags decide whether this is a full window
 * fetch (first call, or a cursor was never obtained) or a delta
 * (subsequent calls) for events/incidents, and whether rules/capabilities
 * are re-fetched at all or answered from a 304.
 */
export async function fetchLivePoll(endpoint: string, prev: LiveState, generation: number): Promise<LivePollResult> {
  const ids = subcallIds(generation);
  const [healthId, capsId, eventsId, incidentsId, rulesId] = ids;

  const health = (await agentGetRaw<Health>(endpoint, "/v1/health", { requestId: healthId })).data as Health;

  const eventsPath = prev.eventsCursor
    ? `/v1/events?cursor=${encodeURIComponent(prev.eventsCursor)}&limit=${LIVE_EVENT_LIMIT}`
    : `/v1/events?since=${encodeURIComponent(
        new Date(Date.now() - LIVE_WINDOW_HOURS * 3600 * 1000).toISOString(),
      )}&limit=${LIVE_EVENT_LIMIT}`;
  const incidentsPath = prev.incidentsCursor
    ? `/v1/incidents?cursor=${encodeURIComponent(prev.incidentsCursor)}&limit=${LIVE_EVENT_LIMIT}`
    : `/v1/incidents?since=${encodeURIComponent(
        new Date(Date.now() - LIVE_WINDOW_HOURS * 3600 * 1000).toISOString(),
      )}&limit=${LIVE_EVENT_LIMIT}`;

  const [capsResult, eventsResult, incidentsResult, rulesResult] = await Promise.all([
    agentGetRaw<CapabilitiesResponse>(endpoint, "/v1/capabilities", {
      requestId: capsId,
      headers: prev.capsETag ? { "If-None-Match": prev.capsETag } : undefined,
    }),
    agentGetRaw<EventsResponse>(endpoint, eventsPath, { requestId: eventsId }),
    agentGetRaw<IncidentsResponse>(endpoint, incidentsPath, { requestId: incidentsId }),
    agentGetRaw<RulesResponse>(endpoint, "/v1/rules", {
      requestId: rulesId,
      headers: prev.rulesETag ? { "If-None-Match": prev.rulesETag } : undefined,
    }),
  ]);

  const eventsBody = eventsResult.data;
  const incidentsBody = incidentsResult.data;
  const newEvents = prev.eventsCursor
    ? mergeById(prev.events, eventsBody?.events ?? [], (e) => e.event_id ?? "")
    : (eventsBody?.events ?? []);
  const newIncidents = prev.incidentsCursor
    ? mergeById(prev.incidents, incidentsBody?.incidents ?? [], (i) => i.incident_id ?? "")
    : (incidentsBody?.incidents ?? []);
  // A 304 means "unchanged", not "empty" - keep whatever capabilities/
  // rules were already known rather than replacing them with nothing.
  const capabilities = capsResult.status === 304 ? prev.capabilities : (capsResult.data?.capabilities ?? []);
  const rules = rulesResult.status === 304 ? prev.rules : (rulesResult.data?.rules ?? []);

  const next: LiveState = {
    events: newEvents,
    incidents: newIncidents,
    health,
    capabilities,
    rules,
    // A missing next_cursor (an older/unexpected server, or a body-less
    // 304-shaped edge case) falls back to null - the *next* poll then
    // re-does a full since-based fetch instead of quietly polling nothing
    // with an empty cursor string forever.
    eventsCursor: eventsBody?.next_cursor ?? null,
    incidentsCursor: incidentsBody?.next_cursor ?? null,
    rulesETag: rulesResult.headers.etag ?? prev.rulesETag,
    capsETag: capsResult.headers.etag ?? prev.capsETag,
  };

  return {
    record: {
      events: newEvents,
      incidents: newIncidents,
      health,
      capabilities,
      rules,
      manifest: null,
      bundleSigned: false,
      bundleHasSignature: false,
      bundleNotes: null,
      refreshedAt: new Date(),
    },
    next,
  };
}

/** Kept as the previous fetchLive's exact signature/behaviour (a full,
 *  since-based fetch of everything) for anything that only wants one
 *  reading - the eval/one-shot case, and existing tests written against
 *  it. useRecord itself calls fetchLivePoll directly so it can carry
 *  cursors/ETags across polls. */
export async function fetchLive(endpoint: string): Promise<Omit<Record, "refresh" | "status" | "error" | "stale">> {
  return (await fetchLivePoll(endpoint, EMPTY_LIVE, 0)).record;
}

/**
 * The record for the chosen source. Demo is synchronous and always ready;
 * live polls the recorder over a cursor-based delta after its first load
 * (ADR 0005), with the previous poll's in-flight requests actually
 * cancelled (not merely ignored on arrival) the moment a new one
 * supersedes them, and exponential backoff with jitter after a failure
 * instead of hammering an unreachable recorder at the fixed interval; a
 * bundle is read once when its path changes.
 */
export function useRecord(settings: SourceSettings): Record {
  const [state, setState] = useState<Omit<Record, "refresh">>(EMPTY);
  const generation = useRef(0);
  const live = useRef<LiveState>(EMPTY_LIVE);
  // A ref, not state: read from inside a setTimeout closure that must see
  // the *current* count, which a value captured from render (including
  // `state.status`, which does not appear in the scheduling effect's own
  // dependency array) would not - that was tried first and was a real
  // stale-closure bug, not a style preference: scheduleNext is created
  // once per effect run and would otherwise always see the status from
  // the render that created it, never a later poll's actual outcome.
  const consecutiveFailures = useRef(0);
  const timerRef = useRef<number | undefined>(undefined);

  const load = useCallback(async () => {
    const prevGen = generation.current;
    const gen = ++generation.current;
    if (settings.kind === "live") cancelPoll(prevGen);

    const apply = (next: Partial<Omit<Record, "refresh">>) => {
      if (gen === generation.current) setState((prev) => ({ ...prev, ...next }));
    };

    if (settings.kind === "demo") {
      apply({
        ...EMPTY,
        status: "ready",
        events: loadDemoEvents(),
        incidents: loadDemoIncidents(),
        refreshedAt: new Date(),
      });
      return;
    }
    if (!isTauri()) {
      apply({ ...EMPTY, status: "error", error: "shell_required" });
      return;
    }
    if (settings.kind === "live") {
      try {
        const { record, next } = await fetchLivePoll(settings.endpoint, live.current, gen);
        if (gen !== generation.current) return; // superseded while awaiting - don't adopt a stale poll's cursors/state
        live.current = next;
        consecutiveFailures.current = 0;
        apply({ ...record, status: "ready", error: "", stale: false });
      } catch (e) {
        if (e instanceof CancelledError) return; // superseded on purpose, not a failure
        if (gen !== generation.current) return;
        consecutiveFailures.current += 1;
        // live.current is deliberately left as it was: a failed poll must
        // not throw away a cursor/ETag that is still valid, and the next
        // attempt should resume the delta where this one would have,
        // not fall back to a full window re-fetch just because one poll
        // failed.
        //
        // stale only means something once a poll has actually succeeded
        // before - the very first attempt failing has no prior good data
        // to call "stale", it is just an error with nothing to show yet.
        apply({
          status: "error",
          error: isAgentErrorPayload(e) ? e : e instanceof Error ? e.message : String(e),
          stale: live.current.health !== null,
        });
      }
      return;
    }
    // bundle
    if (!settings.bundlePath) {
      apply({ ...EMPTY, status: "error", error: "no_bundle" });
      return;
    }
    try {
      const c = await bundleOpen(settings.bundlePath, settings.publicKey);
      apply({
        ...EMPTY,
        status: "ready",
        events: c.events,
        incidents: c.incidents,
        capabilities: c.manifest.capabilities ?? [],
        manifest: c.manifest,
        bundleSigned: c.signed,
        bundleHasSignature: c.has_signature,
        bundleNotes: c.notes ?? null,
        refreshedAt: new Date(),
      });
    } catch (e) {
      apply({ ...EMPTY, status: "error", error: e instanceof Error ? e.message : String(e) });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settings.kind, settings.endpoint, settings.bundlePath, settings.publicKey]);

  useEffect(() => {
    live.current = EMPTY_LIVE;
    consecutiveFailures.current = 0;
    setState((prev) => ({ ...prev, status: "loading" }));
    void load();
    if (settings.kind !== "live") return;

    // A jittered, backing-off schedule instead of a fixed setInterval:
    // consecutiveFailures (a ref, always current - unlike `state.status`,
    // which this closure does not see update, since `state` is not a
    // dependency of this effect and re-running the effect on every state
    // change would tear down and restart the timer constantly) decides
    // the delay each time scheduleNext actually runs, not once when this
    // effect started.
    let cancelled = false;
    const scheduleNext = () => {
      if (cancelled) return;
      const base = Math.max(2, settings.refreshSeconds) * 1000;
      const delay =
        consecutiveFailures.current === 0
          ? base
          : Math.min(BACKOFF_BASE_MS * 2 ** (consecutiveFailures.current - 1), BACKOFF_MAX_MS);
      const jitter = delay * (Math.random() * 0.3);
      timerRef.current = window.setTimeout(() => {
        void load().then(scheduleNext);
      }, delay + jitter);
    };
    scheduleNext();
    return () => {
      cancelled = true;
      if (timerRef.current !== undefined) window.clearTimeout(timerRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load, settings.kind, settings.refreshSeconds]);

  return { ...state, refresh: () => void load() };
}
