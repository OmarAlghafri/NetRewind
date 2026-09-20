// Typed wrappers over /v1/notes/* (internal/api/v1/notes.go, docs/api.md's
// "Notes (writable)" section) - the recorder's own second, isolated store
// for operator notes, feedback and (opt-in) follow-up threads. Reads go
// through agentGet (the record's own read path); every write goes through
// agentRequest (data/ai.ts), the one write surface the API has.

import { agentGet, ApiError } from "./tauri";
import { agentRequest } from "./ai";

/** internal/notes.Outcome's JSON values. */
export type NotesOutcome = "confirmed" | "false_positive" | "unresolved";

/** internal/notes.Annotation's JSON shape. */
export interface NotesAnnotation {
  incident_id: string;
  fingerprint: string;
  rule_id: string;
  root_cause_kind: string;
  root_cause_entity: string;
  opened_at_ns: number;
  outcome: NotesOutcome;
  cause_note?: string;
  resolution_note?: string;
  created_at_ms: number;
  updated_at_ms: number;
}

/** internal/notes.ThreadTurn's JSON shape. */
export interface NotesThreadTurn {
  at_ms: number;
  answer_id: string;
  question_redacted: string;
  summary_redacted: string;
}

/** internal/notes.Stats's JSON shape - GET /v1/notes/stats, what
 *  Diagnostics shows: counts and an on-disk size, never a path. */
export interface NotesStats {
  annotations: number;
  feedback: number;
  threads: number;
  bytes: number;
}

/** GET /v1/notes/incidents/{id}; null on a 404 (no note yet - not an
 *  error a caller needs to catch, matching agentGetRaw's own 304 idiom of
 *  a typed "nothing here" result instead of a thrown ApiError). */
export async function getNotesAnnotation(endpoint: string, incidentId: string): Promise<NotesAnnotation | null> {
  try {
    return await agentGet<NotesAnnotation>(endpoint, `/v1/notes/incidents/${encodeURIComponent(incidentId)}`);
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return null;
    throw e;
  }
}

export interface PutNotesAnnotationInput {
  ruleId: string;
  rootCauseKind: string;
  rootCauseEntity: string;
  openedAtNs: number;
  outcome: NotesOutcome;
  causeNote?: string;
  resolutionNote?: string;
}

/** PUT /v1/notes/incidents/{id} - creates or replaces the note, returning
 *  the stored row as confirmation (the API's own PUT response shape). */
export function putNotesAnnotation(
  endpoint: string,
  incidentId: string,
  input: PutNotesAnnotationInput,
): Promise<NotesAnnotation | null> {
  return agentRequest<NotesAnnotation>(endpoint, "PUT", `/v1/notes/incidents/${encodeURIComponent(incidentId)}`, {
    rule_id: input.ruleId,
    root_cause_kind: input.rootCauseKind,
    root_cause_entity: input.rootCauseEntity,
    opened_at_ns: input.openedAtNs,
    outcome: input.outcome,
    cause_note: input.causeNote,
    resolution_note: input.resolutionNote,
  });
}

export function deleteNotesAnnotation(endpoint: string, incidentId: string): Promise<null> {
  return agentRequest<null>(endpoint, "DELETE", `/v1/notes/incidents/${encodeURIComponent(incidentId)}`);
}

export interface GetNotesSimilarParams {
  ruleId: string;
  rootCauseKind: string;
  entity?: string;
  exclude?: string;
  limit?: number;
}

/** GET /v1/notes/similar - prior annotated incidents for the same rule and
 *  root cause, same rule+kind+entity first, then same rule+kind, newest
 *  first within each tier. Never null (the API's own guarantee). */
export function getNotesSimilar(endpoint: string, params: GetNotesSimilarParams): Promise<NotesAnnotation[]> {
  const q = new URLSearchParams({ rule_id: params.ruleId, root_cause_kind: params.rootCauseKind });
  if (params.entity) q.set("entity", params.entity);
  if (params.exclude) q.set("exclude", params.exclude);
  if (params.limit) q.set("limit", String(params.limit));
  return agentGet<NotesAnnotation[]>(endpoint, `/v1/notes/similar?${q.toString()}`);
}

export interface PostNotesFeedbackInput {
  answerId: string;
  incidentId: string;
  profile: string;
  modelId: string;
  helpful: boolean;
  ruleId?: string;
  rootCauseKind?: string;
  rootCauseEntity?: string;
}

export function postNotesFeedback(endpoint: string, input: PostNotesFeedbackInput): Promise<null> {
  return agentRequest<null>(endpoint, "POST", "/v1/notes/feedback", {
    answer_id: input.answerId,
    incident_id: input.incidentId,
    profile: input.profile,
    model_id: input.modelId,
    helpful: input.helpful,
    rule_id: input.ruleId,
    root_cause_kind: input.rootCauseKind,
    root_cause_entity: input.rootCauseEntity,
  });
}

export function getNotesThread(endpoint: string, incidentId: string): Promise<NotesThreadTurn[]> {
  return agentGet<NotesThreadTurn[]>(endpoint, `/v1/notes/threads/${encodeURIComponent(incidentId)}`);
}

export function postNotesThreadAppend(
  endpoint: string,
  incidentId: string,
  turn: { answerId: string; questionRedacted: string; summaryRedacted: string },
): Promise<null> {
  return agentRequest<null>(endpoint, "POST", `/v1/notes/threads/${encodeURIComponent(incidentId)}`, {
    answer_id: turn.answerId,
    question_redacted: turn.questionRedacted,
    summary_redacted: turn.summaryRedacted,
  });
}

export function getNotesSettings(endpoint: string): Promise<{ history_opt_in: boolean }> {
  return agentGet<{ history_opt_in: boolean }>(endpoint, "/v1/notes/settings");
}

export function putNotesSettings(endpoint: string, historyOptIn: boolean): Promise<null> {
  return agentRequest<null>(endpoint, "PUT", "/v1/notes/settings", { history_opt_in: historyOptIn });
}

/** GET /v1/notes/stats - see NotesStats. */
export function getNotesStats(endpoint: string): Promise<NotesStats> {
  return agentGet<NotesStats>(endpoint, "/v1/notes/stats");
}

export function forgetAllNotes(endpoint: string): Promise<null> {
  return agentRequest<null>(endpoint, "DELETE", "/v1/notes");
}
