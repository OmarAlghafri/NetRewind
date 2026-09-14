import { useState } from "react";
import { useLanguage } from "../../i18n/LanguageContext";
import type { SourceSettings } from "../../data/source";
import { agentGet, isTauri, pickBundleToOpen } from "../../data/tauri";
import type { Health } from "../../data/types";

// The three ways to get a record on screen. Demo always works; the live
// recorder and a bundle need the desktop shell, and each is tried for real
// here (a health call, a verified open) before the wizard records it as the
// chosen source.
export function AgentChoiceStep({
  settings,
  onChange,
}: {
  settings: SourceSettings;
  onChange: (next: SourceSettings) => void;
}) {
  const { t } = useLanguage();
  const inShell = isTauri();
  const [connecting, setConnecting] = useState(false);
  const [liveMessage, setLiveMessage] = useState<{ ok: boolean; text: string } | null>(null);
  const [bundleMessage, setBundleMessage] = useState<{ ok: boolean; text: string } | null>(null);

  const connect = async () => {
    setConnecting(true);
    setLiveMessage(null);
    try {
      const h = await agentGet<Health>(settings.endpoint, "/v1/health");
      onChange({ ...settings, kind: "live" });
      setLiveMessage({ ok: true, text: `${t("wizard_agent_connected")} (${h.observer_id} ${h.version})` });
    } catch (e) {
      setLiveMessage({ ok: false, text: `${t("wizard_agent_connect_failed")}: ${e instanceof Error ? e.message : String(e)}` });
    } finally {
      setConnecting(false);
    }
  };

  const openBundle = async () => {
    setBundleMessage(null);
    try {
      const path = await pickBundleToOpen();
      if (!path) return;
      onChange({ ...settings, kind: "bundle", bundlePath: path });
      setBundleMessage({ ok: true, text: `${t("wizard_agent_bundle_opened")}: ${path}` });
    } catch (e) {
      setBundleMessage({ ok: false, text: `${t("evidence_open_failed")}: ${e instanceof Error ? e.message : String(e)}` });
    }
  };

  const selected = (kind: SourceSettings["kind"]) =>
    settings.kind === kind ? (
      <div className="capability-row" style={{ borderBottom: "none", paddingBottom: 0 }}>
        <span className="status-dot status-up" />
        <span style={{ fontSize: "0.85rem", color: "var(--text-muted)" }}>{t("wizard_agent_selected")}</span>
      </div>
    ) : null;

  return (
    <div>
      <h1 className="page-title">{t("wizard_agent_title")}</h1>
      <p className="page-subtitle">{t("wizard_agent_subtitle")}</p>

      <div className="card">
        <strong>{t("wizard_agent_demo_title")}</strong>
        <p className="wizard-step-note">{t("wizard_agent_demo_body")}</p>
        {settings.kind !== "demo" && (
          <button className="wizard-btn" onClick={() => onChange({ ...settings, kind: "demo" })}>
            {t("wizard_agent_use_demo")}
          </button>
        )}
        {selected("demo")}
      </div>

      <div className="card">
        <strong>{t("wizard_agent_live_title")}</strong>
        <p className="wizard-step-note">{t("wizard_agent_live_body")}</p>
        {inShell ? (
          <button className="wizard-btn wizard-btn-primary" onClick={connect} disabled={connecting}>
            {connecting ? t("wizard_agent_connecting") : t("wizard_agent_live_button")}
          </button>
        ) : (
          <p className="wizard-step-note">{t("settings_shell_note")}</p>
        )}
        {liveMessage && (
          <p className={liveMessage.ok ? "inline-ok" : "inline-error"} role="status">
            <span className="ltr-field">{liveMessage.text}</span>
          </p>
        )}
        {selected("live")}
      </div>

      <div className="card">
        <strong>{t("wizard_agent_import_title")}</strong>
        <p className="wizard-step-note">{t("wizard_agent_import_body")}</p>
        {inShell ? (
          <button className="wizard-btn wizard-btn-primary" onClick={openBundle}>
            {t("wizard_agent_import_button")}
          </button>
        ) : (
          <p className="wizard-step-note">{t("settings_shell_note")}</p>
        )}
        {bundleMessage && (
          <p className={bundleMessage.ok ? "inline-ok" : "inline-error"} role="status">
            <span className="ltr-field">{bundleMessage.text}</span>
          </p>
        )}
        {selected("bundle")}
      </div>
    </div>
  );
}
