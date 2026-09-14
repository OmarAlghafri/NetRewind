import demoEventsRaw from "./demo-events.json";
import demoIncidentsRaw from "./demo-incidents.json";
import type { NetRewindEvent, Incident } from "../types";

// This is real data: an actual run of lab/inject.sh all (the project's own
// 14-scenario fault-injection gate), exported with `netrewind events -o json`
// and `netrewind incidents -o json` - the exact same JSON the CLI and the
// future local API (internal/api/v1) already produce. Demo mode exists so
// the desktop app has something true to show before any live agent
// connection exists (PRODUCT_RELEASE_PLAN_AR.md §5, Phase 2: "ابنِ أولاً وضع
// Demo/Import على corpus ثابت ثم local store").
export function loadDemoEvents(): NetRewindEvent[] {
  return demoEventsRaw as unknown as NetRewindEvent[];
}

export function loadDemoIncidents(): Incident[] {
  return demoIncidentsRaw as unknown as Incident[];
}
