import { useEffect, useState } from "react";
import type { Incident, NetRewindEvent } from "../../types";
import { nsToDate } from "../../types";
import { useLanguage } from "../../i18n/LanguageContext";
import { messageFor } from "../../i18n/aiStatusCatalogue";
import { useAiAssistant } from "../../data/useAiAssistant";
import { readAiSettings, type AiSettings } from "../../data/aiSettings";
import type { AiAnalyzeResponse, AiHandle } from "../../data/aiTypes";
import type { Page } from "../Sidebar";
import type { InvestigationContext } from "../../routing/useRoute";
import type { SourceSettings } from "../../data/source";
import { getNotesAnnotation, postNotesFeedback, putNotesAnnotation, type NotesAnnotation, type NotesOutcome } from "../../data/notes";
import { aiReportRedact } from "../../data/ai";
import { buildAiReport } from "./aiReport";
import type { Lang } from "../../i18n/translations";
import { Button } from "../Button";
import { TechnicalValue } from "../TechnicalValue";

type TFn = (key: Parameters<ReturnType<typeof useLanguage>["t"]>[0]) => string;

/** Resolves an evidence handle (e.g. "E3") back to the real event it cites
 *  (AiHandle.ref) and jumps to it on the Timeline, narrowed to this
 *  analysis's own evidence window and highlighting the cited row - the
 *  plan's "resolved and clickable" requirement for Part D. Only "event"
 *  handles navigate anywhere; "history"/"annotation" handles point at a
 *  prior incident or a note, neither of which is a Timeline row. */
function navigateToHandle(
  navigate: (page: Page, context?: InvestigationContext) => void,
  handles: AiHandle[],
  events: NetRewindEvent[],
  handle: string,
) {
  const resolved = handles.find((h) => h.handle === handle && h.kind === "event");
  if (!resolved) return;
  if (events.length === 0) {
    navigate("timeline", { selection: resolved.ref });
    return;
  }
  const wallTimes = events.map((e) => e.ts_wall);
  navigate("timeline", {
    from: nsToDate(Math.min(...wallTimes)).toISOString(),
    to: nsToDate(Math.max(...wallTimes)).toISOString(),
    selection: resolved.ref,
  });
}

/**
 * The local-AI assistant for one incident - rendered as a sibling below
 * IncidentCard inside InspectorPanel (Incidents.tsx), never inside
 * IncidentCard itself (the onboarding wizard reuses that component and has
 * no local model to offer). "The button says analyze locally, never find
 * the cause" (execution order §4.10): the assistant only ever runs when
 * asked, and its answer is always shown as a hypothesis, never as a
 * second, competing conclusion beside the deterministic engine's own.
 *
 * `events`/`history` are the caller's own selected evidence window and
 * candidate prior-incident pool - this component has no opinion on what
 * to offer, only on how to ask and how to show what came back.
 */
export function AiAssistantPanel({
  incident,
  events,
  history,
  navigate,
  settings,
}: {
  incident: Incident;
  events: NetRewindEvent[];
  history: Incident[];
  /** Optional: absent only in contexts with no Timeline to jump to (there
   *  are none today, but this mirrors Incidents.tsx's own optional
   *  `navigate` for the same "not every caller has a router" reason) -
   *  evidence handles simply render as plain text without it. */
  navigate?: (page: Page, context?: InvestigationContext) => void;
  /** Optional, same reason as `navigate`: the recorder source, needed only
   *  for reading/writing operator notes (Part B). Notes are offered only
   *  for a `"live"` source - a demo or bundle recording has no recorder to
   *  write them to. */
  settings?: SourceSettings;
}) {
  const { t, lang } = useLanguage();
  // AI settings (model/profile/threads) are read fresh on every render
  // rather than threaded down as a prop through Incidents.tsx/
  // InspectorPanel: this panel is the only consumer of them today, and
  // adding a prop through two more components for one reader would be a
  // bigger change than the page rebuild that eventually gives Settings a
  // shared, passed-down copy needs to justify. `settings` (the source, for
  // notes) is a real prop because Incidents.tsx already threads it.
  const aiSettings = readAiSettings();
  const assistant = useAiAssistant(incident, events, history, lang, aiSettings);

  return (
    <section className="ai-panel" aria-label={t("ai_panel_title")}>
      <h3 className="ai-panel-title">{t("ai_panel_title")}</h3>
      <AiAssistantBody
        assistant={assistant}
        onAnalyze={assistant.analyze}
        incident={incident}
        events={events}
        navigate={navigate}
        settings={settings}
        aiSettings={aiSettings}
        lang={lang}
        t={t}
      />
    </section>
  );
}

type Assistant = ReturnType<typeof useAiAssistant>;

type NavigateFn = (page: Page, context?: InvestigationContext) => void;

function AiAssistantBody({
  assistant,
  onAnalyze,
  incident,
  events,
  navigate,
  settings,
  aiSettings,
  lang,
  t,
}: {
  assistant: Assistant;
  onAnalyze: () => void;
  incident: Incident;
  events: NetRewindEvent[];
  navigate?: NavigateFn;
  settings?: SourceSettings;
  aiSettings: AiSettings;
  lang: "ar" | "en";
  t: TFn;
}) {
  switch (assistant.state) {
    case "shell_required":
      return <p className="ai-panel-message">{t("ai_panel_shell_required")}</p>;
    case "disabled":
      return (
        <div className="ai-panel-message">
          <p>{t("ai_panel_disabled")}</p>
          <p className="ai-panel-hint">{t("ai_panel_disabled_hint")}</p>
        </div>
      );
    case "not_configured":
      return (
        <div className="ai-panel-message">
          <p>{t("ai_panel_not_configured")}</p>
          <p className="ai-panel-hint">{t("ai_panel_not_configured_hint")}</p>
        </div>
      );
    case "checking_runtime":
      return <p className="ai-panel-message">{t("ai_panel_checking_runtime")}</p>;
    case "idle":
      return (
        <div className="ai-panel-idle">
          <p className="ai-panel-hint">{t("ai_panel_privacy_line")}</p>
          <Button variant="primary" onClick={onAnalyze}>
            {t("ai_panel_analyze_button")}
          </Button>
        </div>
      );
    case "analyzing":
      return (
        <p className="ai-panel-message" role="status" aria-live="polite">
          {t("ai_panel_analyzing")}
        </p>
      );
    case "insufficient_evidence":
      return (
        <div className="ai-panel-result">
          <p>{t("ai_panel_insufficient_evidence")}</p>
          {assistant.response?.guardrail.reasons && assistant.response.guardrail.reasons.length > 0 && (
            <ul className="ai-panel-list">
              {assistant.response.guardrail.reasons.map((r, i) => (
                <li key={i}>
                  <TechnicalValue>{r.code}</TechnicalValue>
                </li>
              ))}
            </ul>
          )}
          <RetryButton onAnalyze={onAnalyze} t={t} />
        </div>
      );
    case "refused_by_model":
      return (
        <div className="ai-panel-result">
          <p className="ai-panel-hypothesis-banner">{t("ai_panel_hypothesis_banner")}</p>
          <p>{t("ai_panel_refused")}</p>
          <ul className="ai-panel-list">
            {assistant.response?.output.unknowns.map((u, i) => <li key={i}>{u}</li>)}
          </ul>
          <RetryButton onAnalyze={onAnalyze} t={t} />
        </div>
      );
    case "invalid":
      return (
        <div className="ai-panel-result">
          <p>{t("ai_panel_invalid")}</p>
          <details className="ai-panel-technical-details">
            <summary>{t("evidence_raw_title")}</summary>
            <ul className="ai-panel-list">
              {assistant.response?.validation.violations.map((v, i) => (
                <li key={i}>
                  <TechnicalValue>{v.code}</TechnicalValue>
                  {v.detail ? ": " : ""}
                  {v.detail ? <TechnicalValue>{v.detail}</TechnicalValue> : null}
                </li>
              ))}
            </ul>
          </details>
          <RetryButton onAnalyze={onAnalyze} t={t} />
        </div>
      );
    case "error":
      return (
        <div className="ai-panel-result">
          <p>{t("ai_panel_error")}</p>
          <p className="ai-panel-hint">
            {assistant.errorCode ? messageFor({ code: assistant.errorCode, message: assistant.errorMessage ?? "" }, lang).message : assistant.errorMessage}
          </p>
          <RetryButton onAnalyze={onAnalyze} t={t} />
        </div>
      );
    case "answered":
      return (
        <AiAnsweredResult
          assistant={assistant}
          onAnalyze={onAnalyze}
          incident={incident}
          events={events}
          navigate={navigate}
          settings={settings}
          aiSettings={aiSettings}
          lang={lang}
          t={t}
        />
      );
  }
}

function RetryButton({ onAnalyze, t }: { onAnalyze: () => void; t: (key: Parameters<ReturnType<typeof useLanguage>["t"]>[0]) => string }) {
  return (
    <Button variant="ghost" onClick={onAnalyze}>
      {t("ai_panel_retry_button")}
    </Button>
  );
}

function AiAnsweredResult({
  assistant,
  onAnalyze,
  incident,
  events,
  navigate,
  settings,
  aiSettings,
  lang,
  t,
}: {
  assistant: Assistant;
  onAnalyze: () => void;
  incident: Incident;
  events: NetRewindEvent[];
  navigate?: NavigateFn;
  settings?: SourceSettings;
  aiSettings: AiSettings;
  lang: Lang;
  t: TFn;
}) {
  const output = assistant.response?.output;
  const handles = assistant.response?.handles ?? [];
  if (!output) return null;
  return (
    <div className="ai-panel-result">
      <p className="ai-panel-hypothesis-banner">{t("ai_panel_hypothesis_banner")}</p>
      <p dir="auto">{output.summary}</p>

      {output.ranked_hypotheses.length > 0 && (
        <ul className="ai-panel-hypotheses">
          {output.ranked_hypotheses.map((h, i) => (
            <li key={i}>
              <TechnicalValue>{h.cause}</TechnicalValue> — <TechnicalValue>{h.entity}</TechnicalValue>
              {"  ("}
              {t("confidence")}: <span className="ltr-field">{h.confidence}%</span>
              {", "}
              {t("ai_panel_ceiling_label")}: <span className="ltr-field">{assistant.response?.guardrail.ceiling}%</span>
              {")"}
            </li>
          ))}
        </ul>
      )}

      {output.evidence_handles.length > 0 && (
        <p>
          {t("ai_panel_evidence_label")}:{" "}
          {output.evidence_handles.map((h) => {
            const isEventHandle = handles.some((r) => r.handle === h && r.kind === "event");
            return isEventHandle && navigate ? (
              <button
                key={h}
                type="button"
                className="ai-panel-handle-button"
                onClick={() => navigateToHandle(navigate, handles, events, h)}
                title={t("ai_panel_evidence_jump_hint")}
              >
                <TechnicalValue className="ai-panel-handle">{h}</TechnicalValue>
              </button>
            ) : (
              <TechnicalValue key={h} className="ai-panel-handle">
                {h}
              </TechnicalValue>
            );
          })}
        </p>
      )}

      {output.counter_evidence.length > 0 && (
        <div>
          <strong>{t("ai_panel_counter_evidence_label")}:</strong>
          <ul className="ai-panel-list">
            {output.counter_evidence.map((c, i) => <li key={i} dir="auto">{c}</li>)}
          </ul>
        </div>
      )}

      {output.unknowns.length > 0 && (
        <div>
          <strong>{t("ai_panel_unknowns_label")}:</strong>
          <ul className="ai-panel-list">
            {output.unknowns.map((u, i) => <li key={i} dir="auto">{u}</li>)}
          </ul>
        </div>
      )}

      {output.next_checks.length > 0 && (
        <div>
          <strong>{t("ai_panel_next_checks_label")}:</strong>
          <ul className="ai-panel-list">
            {output.next_checks.map((c, i) => <li key={i} dir="auto">{c}</li>)}
          </ul>
        </div>
      )}

      {assistant.response && assistant.response.retrieval && assistant.response.retrieval.length > 0 && (
        <div>
          <strong>{t("ai_panel_retrieval_label")}:</strong>
          <ul className="ai-panel-list">
            {assistant.response.retrieval.map((r) =>
              navigate ? (
                <li key={r.handle}>
                  <button
                    type="button"
                    className="ai-panel-handle-button"
                    onClick={() => navigate("incidents", { selection: r.incident_id })}
                    title={t("ai_panel_retrieval_jump_hint")}
                  >
                    <TechnicalValue>{r.rule_id}</TechnicalValue> — <TechnicalValue>{r.root_cause_kind}</TechnicalValue>
                    {r.root_cause_entity ? (
                      <>
                        {" "}
                        (<TechnicalValue>{r.root_cause_entity}</TechnicalValue>)
                      </>
                    ) : null}
                  </button>
                </li>
              ) : (
                <li key={r.handle}>
                  <TechnicalValue>{r.rule_id}</TechnicalValue> — <TechnicalValue>{r.root_cause_kind}</TechnicalValue>
                </li>
              ),
            )}
          </ul>
        </div>
      )}

      <NotesSection incident={incident} settings={settings} aiSettings={aiSettings} answerId={assistant.answerId} t={t} />

      {assistant.response && (
        <CopyReportButton incident={incident} response={assistant.response} settings={settings} lang={lang} t={t} />
      )}

      <RetryButton onAnalyze={onAnalyze} t={t} />
    </div>
  );
}

/**
 * "Copy report" (Part D): composes the three-section report (aiReport.ts),
 * looks up the operator's own note first when a live source makes that
 * possible (silently proceeding with none otherwise - a report is still
 * useful without one), redacts the whole document through the CLI's
 * shared internal/redact.Redactor (aiReportRedact), and only then writes
 * it to the clipboard - the same order Diagnostics.tsx's own copy button
 * follows (build text, then copy), with a redaction step this text
 * specifically needs and Diagnostics's support summary does not (it is
 * already secret-free by construction).
 */
function CopyReportButton({
  incident,
  response,
  settings,
  lang,
  t,
}: {
  incident: Incident;
  response: AiAnalyzeResponse;
  settings?: SourceSettings;
  lang: Lang;
  t: TFn;
}) {
  const [status, setStatus] = useState<"idle" | "copying" | "done" | "failed">("idle");

  const copy = async () => {
    setStatus("copying");
    try {
      let note: NotesAnnotation | null = null;
      if (settings && settings.kind === "live") {
        note = await getNotesAnnotation(settings.endpoint, incident.incident_id);
      }
      const report = buildAiReport({ incident, response, note, lang });
      const redacted = await aiReportRedact(report);
      await navigator.clipboard.writeText(redacted);
      setStatus("done");
    } catch {
      setStatus("failed");
    }
    window.setTimeout(() => setStatus("idle"), 2500);
  };

  return (
    <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
      <Button variant="secondary" onClick={() => void copy()} disabled={status === "copying"}>
        {t("ai_panel_copy_report_button")}
      </Button>
      {status === "done" && (
        <span className="inline-ok" role="status">
          {t("ai_panel_copy_report_done")}
        </span>
      )}
      {status === "failed" && (
        <span className="inline-error" role="alert">
          {t("ai_panel_copy_report_failed")}
        </span>
      )}
    </div>
  );
}

/**
 * The "memory" half of the feature (execution order's own framing: a model
 * that "has memory"): the operator's own conclusion (outcome + free-text
 * notes) and helpful/not-helpful feedback on this answer, both written to
 * the recorder's notes store (ADR 0008) - never to the model, never
 * leaving the device. Only offered for a `"live"` source: a demo or bundle
 * recording has no recorder to write these to.
 */
function NotesSection({
  incident,
  settings,
  aiSettings,
  answerId,
  t,
}: {
  incident: Incident;
  settings?: SourceSettings;
  aiSettings: AiSettings;
  answerId?: string;
  t: TFn;
}) {
  if (!settings || settings.kind !== "live") {
    return (
      <div className="ai-panel-notes">
        <p className="ai-panel-hint">{t("ai_panel_notes_unavailable")}</p>
      </div>
    );
  }
  return (
    <div className="ai-panel-notes">
      <AnnotationForm incident={incident} endpoint={settings.endpoint} t={t} />
      {answerId && <FeedbackButtons incident={incident} endpoint={settings.endpoint} answerId={answerId} aiSettings={aiSettings} t={t} />}
    </div>
  );
}

const OUTCOMES: readonly NotesOutcome[] = ["confirmed", "false_positive", "unresolved"];

function AnnotationForm({ incident, endpoint, t }: { incident: Incident; endpoint: string; t: TFn }) {
  const [outcome, setOutcome] = useState<NotesOutcome | "">("");
  const [causeNote, setCauseNote] = useState("");
  const [resolutionNote, setResolutionNote] = useState("");
  const [status, setStatus] = useState<"loading" | "idle" | "saving" | "saved" | "error">("loading");

  useEffect(() => {
    let cancelled = false;
    setStatus("loading");
    setOutcome("");
    setCauseNote("");
    setResolutionNote("");
    getNotesAnnotation(endpoint, incident.incident_id)
      .then((a) => {
        if (cancelled) return;
        if (a) {
          setOutcome(a.outcome);
          setCauseNote(a.cause_note ?? "");
          setResolutionNote(a.resolution_note ?? "");
        }
        setStatus("idle");
      })
      .catch(() => {
        if (!cancelled) setStatus("error");
      });
    return () => {
      cancelled = true;
    };
  }, [endpoint, incident.incident_id]);

  const save = async () => {
    if (!outcome) return;
    setStatus("saving");
    try {
      await putNotesAnnotation(endpoint, incident.incident_id, {
        ruleId: incident.rule_id,
        rootCauseKind: incident.root_cause.kind,
        rootCauseEntity: incident.root_cause.entity,
        openedAtNs: incident.opened_at,
        outcome,
        causeNote: causeNote || undefined,
        resolutionNote: resolutionNote || undefined,
      });
      setStatus("saved");
    } catch {
      setStatus("error");
    }
  };

  if (status === "loading") return <p className="ai-panel-message">{t("ai_panel_notes_loading")}</p>;

  return (
    <div className="ai-panel-notes-form">
      <strong>{t("ai_panel_notes_title")}</strong>
      <div className="ai-panel-notes-outcome" role="radiogroup" aria-label={t("ai_panel_notes_outcome_label")}>
        {OUTCOMES.map((o) => (
          <label className="field-check" key={o}>
            <input
              type="radio"
              name={`ai-notes-outcome-${incident.incident_id}`}
              checked={outcome === o}
              onChange={() => setOutcome(o)}
            />
            {t(`ai_panel_notes_outcome_${o}` as const)}
          </label>
        ))}
      </div>
      <label className="field-label">
        {t("ai_panel_notes_cause_label")}
        <textarea className="field-input" dir="auto" rows={2} value={causeNote} onChange={(e) => setCauseNote(e.target.value)} />
      </label>
      <label className="field-label">
        {t("ai_panel_notes_resolution_label")}
        <textarea className="field-input" dir="auto" rows={2} value={resolutionNote} onChange={(e) => setResolutionNote(e.target.value)} />
      </label>
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <Button variant="secondary" onClick={() => void save()} disabled={!outcome || status === "saving"}>
          {t("ai_panel_notes_save_button")}
        </Button>
        {status === "saved" && (
          <span className="inline-ok" role="status">
            {t("ai_panel_notes_saved")}
          </span>
        )}
        {status === "error" && (
          <span className="inline-error" role="alert">
            {t("ai_panel_notes_error")}
          </span>
        )}
      </div>
    </div>
  );
}

function FeedbackButtons({
  incident,
  endpoint,
  answerId,
  aiSettings,
  t,
}: {
  incident: Incident;
  endpoint: string;
  answerId: string;
  aiSettings: AiSettings;
  t: TFn;
}) {
  const [status, setStatus] = useState<"idle" | "sent" | "error">("idle");

  const send = async (helpful: boolean) => {
    try {
      await postNotesFeedback(endpoint, {
        answerId,
        incidentId: incident.incident_id,
        profile: aiSettings.profile,
        modelId: aiSettings.modelFileName,
        helpful,
        ruleId: incident.rule_id,
        rootCauseKind: incident.root_cause.kind,
        rootCauseEntity: incident.root_cause.entity,
      });
      setStatus("sent");
    } catch {
      setStatus("error");
    }
  };

  if (status === "sent") {
    return <p className="ai-panel-hint">{t("ai_panel_feedback_thanks")}</p>;
  }
  return (
    <div className="ai-panel-feedback">
      <span className="ai-panel-hint">{t("ai_panel_feedback_question")}</span>
      <Button variant="ghost" onClick={() => void send(true)}>
        {t("ai_panel_feedback_helpful")}
      </Button>
      <Button variant="ghost" onClick={() => void send(false)}>
        {t("ai_panel_feedback_not_helpful")}
      </Button>
      {status === "error" && (
        <span className="inline-error" role="alert">
          {t("ai_panel_feedback_error")}
        </span>
      )}
    </div>
  );
}
