// Composes the "copy report" text the local-AI panel offers: three
// labelled sections (engine facts / operator notes / local-model text),
// exactly as the plan's Part D describes it. A pure function, like
// Diagnostics.tsx's own buildSupportSummary - reads labels straight from
// dict[lang] (not through the useLanguage() hook) so it is unit-testable
// without mounting a component, and reads in whichever language the panel
// that composed it was showing.

import { dict, type Lang } from "../../i18n/translations";
import type { Incident } from "../../types";
import type { AiAnalyzeResponse } from "../../data/aiTypes";
import type { NotesAnnotation, NotesOutcome } from "../../data/notes";

const OUTCOME_KEY: Record<NotesOutcome, "ai_panel_notes_outcome_confirmed" | "ai_panel_notes_outcome_false_positive" | "ai_panel_notes_outcome_unresolved"> = {
  confirmed: "ai_panel_notes_outcome_confirmed",
  false_positive: "ai_panel_notes_outcome_false_positive",
  unresolved: "ai_panel_notes_outcome_unresolved",
};

/**
 * Builds the report text. The caller is responsible for redaction
 * (aiReportRedact, data/ai.ts) - this function only composes what goes
 * into the document, it never decides what leaves the device.
 */
export function buildAiReport(params: {
  incident: Incident;
  response: AiAnalyzeResponse;
  note: NotesAnnotation | null;
  lang: Lang;
}): string {
  const { incident, response, note, lang } = params;
  const d = dict[lang];
  const output = response.output;
  const lines: string[] = [d.ai_report_title, ""];

  lines.push(`== ${d.ai_report_engine_facts_heading} ==`);
  lines.push(`${d.ai_report_rule_label}: ${incident.rule_id}`);
  lines.push(
    `${d.ai_report_root_cause_label}: ${incident.root_cause.kind} (${incident.root_cause.entity}), ${d.confidence}: ${incident.root_cause.confidence}%`,
  );
  lines.push(`${d.ai_report_severity_label}: ${incident.severity}`);
  if (incident.chain.length > 0) {
    lines.push(`${d.ai_report_chain_heading}:`);
    for (const link of incident.chain) lines.push(`  - ${link.kind} (${link.subject})`);
  }

  lines.push("", `== ${d.ai_report_notes_heading} ==`);
  if (note) {
    lines.push(`${d.ai_panel_notes_outcome_label}: ${d[OUTCOME_KEY[note.outcome]]}`);
    if (note.cause_note) lines.push(`${d.ai_panel_notes_cause_label}: ${note.cause_note}`);
    if (note.resolution_note) lines.push(`${d.ai_panel_notes_resolution_label}: ${note.resolution_note}`);
  } else {
    lines.push(d.ai_report_notes_none);
  }

  lines.push("", `== ${d.ai_report_model_text_heading} ==`);
  lines.push(output.summary);
  if (output.ranked_hypotheses.length > 0) {
    for (const h of output.ranked_hypotheses) {
      lines.push(`  - ${h.cause} (${h.entity}), ${d.confidence}: ${h.confidence}%, ${d.ai_panel_ceiling_label}: ${response.guardrail.ceiling}%`);
    }
  }
  if (output.counter_evidence.length > 0) {
    lines.push(`${d.ai_panel_counter_evidence_label}:`);
    for (const c of output.counter_evidence) lines.push(`  - ${c}`);
  }
  if (output.unknowns.length > 0) {
    lines.push(`${d.ai_panel_unknowns_label}:`);
    for (const u of output.unknowns) lines.push(`  - ${u}`);
  }
  if (output.next_checks.length > 0) {
    lines.push(`${d.ai_panel_next_checks_label}:`);
    for (const c of output.next_checks) lines.push(`  - ${c}`);
  }

  return lines.join("\n");
}
