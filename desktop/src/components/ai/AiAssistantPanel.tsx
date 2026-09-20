import type { Incident, NetRewindEvent } from "../../types";
import { useLanguage } from "../../i18n/LanguageContext";
import { messageFor } from "../../i18n/aiStatusCatalogue";
import { useAiAssistant } from "../../data/useAiAssistant";
import { readAiSettings } from "../../data/aiSettings";
import { Button } from "../Button";
import { TechnicalValue } from "../TechnicalValue";

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
}: {
  incident: Incident;
  events: NetRewindEvent[];
  history: Incident[];
}) {
  const { t, lang } = useLanguage();
  // Settings are read fresh on every render rather than threaded down as a
  // prop through Incidents.tsx/InspectorPanel: this panel is the only
  // consumer of aiSettings today, and adding a prop through two more
  // components for one reader would be a bigger change than the page
  // rebuild that eventually gives Settings a shared, passed-down copy
  // needs to justify.
  const settings = readAiSettings();
  const assistant = useAiAssistant(incident, events, history, lang, settings);

  return (
    <section className="ai-panel" aria-label={t("ai_panel_title")}>
      <h3 className="ai-panel-title">{t("ai_panel_title")}</h3>
      <AiAssistantBody assistant={assistant} onAnalyze={assistant.analyze} lang={lang} t={t} />
    </section>
  );
}

type Assistant = ReturnType<typeof useAiAssistant>;

function AiAssistantBody({
  assistant,
  onAnalyze,
  lang,
  t,
}: {
  assistant: Assistant;
  onAnalyze: () => void;
  lang: "ar" | "en";
  t: (key: Parameters<ReturnType<typeof useLanguage>["t"]>[0]) => string;
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
      return <AiAnsweredResult assistant={assistant} onAnalyze={onAnalyze} t={t} />;
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
  t,
}: {
  assistant: Assistant;
  onAnalyze: () => void;
  t: (key: Parameters<ReturnType<typeof useLanguage>["t"]>[0]) => string;
}) {
  const output = assistant.response?.output;
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
          {output.evidence_handles.map((h) => (
            <TechnicalValue key={h} className="ai-panel-handle">
              {h}
            </TechnicalValue>
          ))}
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

      <RetryButton onAnalyze={onAnalyze} t={t} />
    </div>
  );
}
