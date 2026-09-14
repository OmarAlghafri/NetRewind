// Mirrors internal/event.Event and internal/incident.Incident exactly
// (docs/schema.md), field for field, so the frontend never invents a shape
// the Go side does not actually produce.

export interface EntityRef {
  kind: string;
  id: string;
  label: string;
  attrs?: Record<string, unknown>;
}

export interface NetRewindEvent {
  event_id: string;
  schema_v: number;
  ts_wall: number; // nanoseconds since epoch, UTC
  ts_mono: number;
  observer_id: string;
  source: string;
  kind: string; // "family.action", e.g. "l2.arp_binding_changed"
  severity: "info" | "notice" | "warn" | "error";
  confidence: number;
  subject: EntityRef;
  related?: EntityRef[];
  attrs?: Record<string, unknown>;
  evidence?: Record<string, unknown>;
  dedup_key?: string;
  count?: number;
}

export type Relation = "causes" | "correlates" | "precedes" | "";

export interface IncidentLink {
  seq: number;
  event_id: string;
  kind: string;
  at: number;
  subject: string;
  relation: Relation;
  why: string;
  evidence?: Record<string, unknown>;
}

export interface RootCause {
  kind: string;
  entity: string;
  event_id: string;
  confidence: number;
}

export interface Incident {
  incident_id: string;
  opened_at: number;
  closed_at?: number;
  status: "open" | "closed";
  title: string;
  severity: "info" | "notice" | "warn" | "error";
  confidence: number;
  root_cause: RootCause;
  chain: IncidentLink[];
  victims?: string[];
  rule_id: string;
  advice?: string;
}

export function nsToDate(ns: number): Date {
  return new Date(ns / 1e6);
}
