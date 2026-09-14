import { useLanguage } from "../../i18n/LanguageContext";

// Same "genuinely disabled, not a fake no-op" pattern as pages/Evidence.tsx:
// there is no live internal/api/v1-over-internal/ipc connection wired into
// this app yet (ADR 0001), and no internal/bundle import flow wired into
// this UI either (matches Evidence.tsx's own import note). Only the demo
// recording actually does something when chosen, so it is the only option
// presented as available; the other two are real <button disabled>
// elements, not clickable controls that quietly do nothing.
export function AgentChoiceStep() {
  const { t } = useLanguage();

  return (
    <div>
      <h1 className="page-title">{t("wizard_agent_title")}</h1>
      <p className="page-subtitle">{t("wizard_agent_subtitle")}</p>

      <div className="card">
        <strong>{t("wizard_agent_demo_title")}</strong>
        <p className="wizard-step-note">{t("wizard_agent_demo_body")}</p>
        <div className="capability-row" style={{ borderBottom: "none", paddingBottom: 0 }}>
          <span className="status-dot status-up" />
          <span style={{ fontSize: "0.85rem", color: "var(--text-muted)" }}>{t("wizard_agent_available")}</span>
        </div>
      </div>

      <div className="card">
        <strong>{t("wizard_agent_live_title")}</strong>
        <p className="wizard-step-note">{t("wizard_agent_live_body")}</p>
        <button disabled className="disabled-button">
          {t("wizard_agent_live_button")}
        </button>
      </div>

      <div className="card">
        <strong>{t("wizard_agent_import_title")}</strong>
        <p className="wizard-step-note">{t("wizard_agent_import_body")}</p>
        <button disabled className="disabled-button">
          {t("wizard_agent_import_button")}
        </button>
      </div>
    </div>
  );
}
