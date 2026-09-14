import { useCallback, useEffect, useRef, useState } from "react";
import type { Incident, NetRewindEvent } from "../types";
import type { Capability, Health, RuleSummary, BundleManifest } from "./types";
import type { SourceSettings } from "./source";
import { loadDemoEvents, loadDemoIncidents } from "../demo/loadDemoData";
import { agentGet, bundleOpen, isTauri } from "./tauri";

/** How far back a live view reaches. The API's own default is one hour;
 *  a viewer opened after an overnight incident needs the previous day. */
export const LIVE_WINDOW_HOURS = 24;
/** Upper bound on events fetched per poll; the recorder folds repeats, so
 *  this covers a busy day on a segment with churn. */
export const LIVE_EVENT_LIMIT = 5000;

export type RecordStatus = "loading" | "ready" | "error";

export interface Record {
  status: RecordStatus;
  /** Set when status is "error": what went wrong, in the recorder's or the shell's words. */
  error: string;
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
  /** When the current data was fetched. */
  refreshedAt: Date | null;
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
  refreshedAt: null,
};

interface EventsResponse {
  events: NetRewindEvent[];
}
interface IncidentsResponse {
  incidents: Incident[];
}
interface CapabilitiesResponse {
  capabilities: Capability[];
}
interface RulesResponse {
  rules: RuleSummary[];
}

/** Fetches everything a live view shows, in one round of requests. */
export async function fetchLive(endpoint: string): Promise<Omit<Record, "refresh" | "status" | "error">> {
  const since = new Date(Date.now() - LIVE_WINDOW_HOURS * 3600 * 1000).toISOString();
  const health = await agentGet<Health>(endpoint, "/v1/health");
  const [caps, events, incidents, rules] = await Promise.all([
    agentGet<CapabilitiesResponse>(endpoint, "/v1/capabilities"),
    agentGet<EventsResponse>(endpoint, `/v1/events?since=${encodeURIComponent(since)}&limit=${LIVE_EVENT_LIMIT}`),
    agentGet<IncidentsResponse>(endpoint, `/v1/incidents?since=${encodeURIComponent(since)}&limit=${LIVE_EVENT_LIMIT}`),
    agentGet<RulesResponse>(endpoint, "/v1/rules"),
  ]);
  return {
    events: events.events ?? [],
    incidents: incidents.incidents ?? [],
    health,
    capabilities: caps.capabilities ?? [],
    rules: rules.rules ?? [],
    manifest: null,
    bundleSigned: false,
    bundleHasSignature: false,
    refreshedAt: new Date(),
  };
}

/**
 * The record for the chosen source. Demo is synchronous and always ready;
 * live polls the recorder on the configured interval and reports the
 * failure when it cannot be reached; a bundle is read once when its path
 * changes.
 */
export function useRecord(settings: SourceSettings): Record {
  const [state, setState] = useState<Omit<Record, "refresh">>(EMPTY);
  const generation = useRef(0);

  const load = useCallback(async () => {
    const gen = ++generation.current;
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
        const data = await fetchLive(settings.endpoint);
        apply({ ...data, status: "ready", error: "" });
      } catch (e) {
        apply({ status: "error", error: e instanceof Error ? e.message : String(e) });
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
        refreshedAt: new Date(),
      });
    } catch (e) {
      apply({ ...EMPTY, status: "error", error: e instanceof Error ? e.message : String(e) });
    }
  }, [settings.kind, settings.endpoint, settings.bundlePath, settings.publicKey]);

  useEffect(() => {
    setState((prev) => ({ ...prev, status: "loading" }));
    void load();
    if (settings.kind !== "live") return;
    const id = window.setInterval(() => void load(), Math.max(2, settings.refreshSeconds) * 1000);
    return () => window.clearInterval(id);
  }, [load, settings.kind, settings.refreshSeconds]);

  return { ...state, refresh: () => void load() };
}
