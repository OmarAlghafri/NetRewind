// Shapes served by the recorder's local API (internal/api/v1) and carried in
// evidence bundles (internal/bundle). Field names mirror the Go JSON tags.

export type CollectorStatus = "unknown" | "up" | "down" | "unsupported";

export interface Capability {
  name: string;
  platform: string;
  privilege: string;
  coverage: string[];
  status: CollectorStatus;
  reason?: string;
  last_change: string;
  last_seen: string;
}

export interface Health {
  status: string;
  version: string;
  schema_version: number;
  api_version: number;
  observer_id: string;
  started_at?: string;
  uptime_seconds: number;
  store: { path?: string; events: number; error?: string };
  collectors: { up: number; down: number; unsupported: number };
}

export interface RuleSummary {
  id: string;
  title: string;
  severity: string;
  confidence: number;
  window: string;
  root_cause: string;
  advice: string;
}

export interface BundleManifest {
  format_version: number;
  schema_version: number;
  app_version: string;
  observer_id: string;
  created_at: string;
  window_from: string;
  window_to: string;
  event_count: number;
  incident_count: number;
  truncated: boolean;
  redacted: boolean;
  capabilities?: Capability[];
}
