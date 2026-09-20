// Mirrors cmd/netrewind/ai.go's aiAnalyzeRequest/aiAnalyzeResponse exactly,
// field for field - the same JSON `netrewind ai analyze` reads from stdin
// and writes to stdout, which is also exactly what ai_analyze (the Tauri
// command, desktop/src-tauri/src/lib.rs) sends and returns, since it
// spawns that same CLI. One shape, not a frontend-only approximation of it.

import type { Incident, NetRewindEvent } from "../types";

/** internal/registry.Snapshot's JSON shape (docs/api.md's /v1/capabilities). */
export interface CapabilitySnapshot {
  name: string;
  platform: string;
  privilege: string;
  coverage: string[];
  status: "up" | "down" | "unsupported" | "unknown";
  reason?: string;
  reason_code?: string;
  reason_params?: Record<string, string>;
  last_change?: string;
  last_seen?: string;
}

/** One operator note offered to the model as an A-handle - see
 *  internal/ai.AnnotationRef. `id` is the fingerprint or incident id the
 *  handle resolves back to; never a raw database row. */
export interface AiAnnotationInput {
  id: string;
  text: string;
}

export interface AiPolicy {
  max_tokens?: number;
  retry_max_tokens?: number;
  no_retry?: boolean;
  timeout_seconds?: number;
  max_history?: number;
}

/** The stdin contract - see cmd/netrewind/ai.go's aiAnalyzeRequest. */
export interface AiAnalyzeRequest {
  incident: Incident | null;
  events: NetRewindEvent[];
  capabilities?: CapabilitySnapshot[] | null;
  annotations?: AiAnnotationInput[];
  history?: Incident[];
  question: string;
  lang: "en" | "ar";
  server: { url: string; token?: string };
  policy?: AiPolicy;
}

/** internal/ai.Reason's JSON shape. */
export interface AiGuardrailReason {
  code: string;
  params?: Record<string, string>;
}

/** internal/ai.GuardrailResult's JSON shape. */
export interface AiGuardrailResult {
  refuse: boolean;
  ceiling: number;
  reasons?: AiGuardrailReason[];
  blind_families?: string[];
}

/** One offered evidence handle - see cmd/netrewind/ai.go's aiHandleWire. */
export interface AiHandle {
  handle: string;
  kind: "event" | "history" | "annotation" | "";
  ref: string;
}

/** One ranked prior incident actually offered - aiRetrievalWire. */
export interface AiRetrieval {
  handle: string;
  incident_id: string;
  rule_id: string;
  root_cause_kind: string;
  root_cause_entity: string;
}

/** internal/ai.Hypothesis's JSON shape. */
export interface AiHypothesis {
  cause: string;
  entity: string;
  confidence: number;
}

/** internal/ai.ModelOutput's JSON shape - the model's own constrained answer. */
export interface AiModelOutput {
  summary: string;
  ranked_hypotheses: AiHypothesis[];
  evidence_handles: string[];
  counter_evidence: string[];
  unknowns: string[];
  confidence_ceiling: number;
  next_checks: string[];
}

/** internal/ai.Violation's JSON shape - see ai/i18n's aiGuardrailCatalogue
 *  (violation codes) for how these are shown, distinct from
 *  aiStatusCatalogue (transport/runtime codes). */
export interface AiViolation {
  code: string;
  detail?: string;
}

export interface AiValidation {
  ok: boolean;
  retried: boolean;
  first_attempt_valid: boolean;
  violations: AiViolation[];
}

export interface AiTiming {
  prompt_ms: number;
  predicted_ms: number;
  total_ms: number;
}

export type AiVerdict = "answered" | "insufficient_evidence" | "refused_by_model" | "invalid";

/** The stdout contract - see cmd/netrewind/ai.go's aiAnalyzeResponse. */
export interface AiAnalyzeResponse {
  version: number;
  verdict: AiVerdict;
  guardrail: AiGuardrailResult;
  handles: AiHandle[];
  retrieval?: AiRetrieval[];
  output: AiModelOutput;
  validation: AiValidation;
  timing: AiTiming;
  error?: string;
}
