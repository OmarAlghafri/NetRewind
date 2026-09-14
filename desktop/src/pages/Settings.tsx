import { useEffect, useState } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { SourceKind, SourceSettings } from "../data/source";
import { agentDefaultEndpoint, agentGet, isTauri } from "../data/tauri";
import type { Health } from "../data/types";

export function Settings({
  settings,
  onChange,
  onReopenWizard,
}: {
  settings: SourceSettings;
  onChange: (next: SourceSettings) => void;
  onReopenWizard: () => void;
}) {
  const { t, lang, setLang } = useLanguage();
  const inShell = isTauri();
  const [draft, setDraft] = useState<SourceSettings>(settings);
  const [defaultEndpoint, setDefaultEndpoint] = useState("");
  const [saved, setSaved] = useState(false);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => setDraft(settings), [settings]);
  useEffect(() => {
    if (inShell) agentDefaultEndpoint().then(setDefaultEndpoint).catch(() => setDefaultEndpoint(""));
  }, [inShell]);

  const dirty = JSON.stringify(draft) !== JSON.stringify(settings);

  const save = () => {
    onChange(draft);
    setSaved(true);
    window.setTimeout(() => setSaved(false), 2000);
  };

  const testConnection = async () => {
    setTest(null);
    try {
      const h = await agentGet<Health>(draft.endpoint, "/v1/health");
      setTest({ ok: true, text: `${t("settings_connection_ok")}: ${h.observer_id} ${h.version}` });
    } catch (e) {
      setTest({ ok: false, text: `${t("settings_connection_failed")}: ${e instanceof Error ? e.message : String(e)}` });
    }
  };

  const sourceOption = (kind: SourceKind, label: string, enabled: boolean) => (
    <label className={`field-radio${enabled ? "" : " field-radio-disabled"}`} key={kind}>
      <input
        type="radio"
        name="source"
        value={kind}
        checked={draft.kind === kind}
        disabled={!enabled}
        onChange={() => setDraft({ ...draft, kind })}
      />
      {label}
    </label>
  );

  return (
    <div>
      <h1 className="page-title">{t("settings_title")}</h1>

      <div className="card">
        <strong>{t("settings_language")}</strong>
        <div className="lang-toggle" style={{ maxWidth: 240, marginTop: 10 }}>
          <button className={lang === "ar" ? "active" : ""} onClick={() => setLang("ar")}>
            العربية
          </button>
          <button className={lang === "en" ? "active" : ""} onClick={() => setLang("en")}>
            English
          </button>
        </div>
      </div>

      <div className="card">
        <strong>{t("settings_source_title")}</strong>
        <div style={{ marginTop: 8 }}>
          {sourceOption("demo", t("settings_source_demo"), true)}
          {sourceOption("live", t("settings_source_live"), inShell)}
          {sourceOption("bundle", t("settings_source_bundle"), inShell && draft.bundlePath !== "")}
        </div>
        {!inShell && <p className="wizard-step-note">{t("settings_shell_note")}</p>}

        {inShell && (
          <>
            <label className="field-label">
              {t("settings_endpoint_label")}
              <input
                className="field-input ltr-field"
                dir="ltr"
                value={draft.endpoint}
                placeholder={defaultEndpoint}
                onChange={(e) => setDraft({ ...draft, endpoint: e.target.value })}
              />
            </label>
            <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
              <button className="wizard-btn" onClick={testConnection}>
                {t("settings_test_connection")}
              </button>
              {test && (
                <span className={test.ok ? "inline-ok" : "inline-error"} role="status">
                  <span className="ltr-field">{test.text}</span>
                </span>
              )}
            </div>
            <label className="field-label">
              {t("settings_refresh_label")}
              <input
                className="field-input ltr-field"
                dir="ltr"
                type="number"
                min={2}
                max={60}
                value={draft.refreshSeconds}
                onChange={(e) => setDraft({ ...draft, refreshSeconds: Number(e.target.value) })}
              />
            </label>
            <label className="field-label">
              {t("settings_public_key_label")}
              <input
                className="field-input ltr-field"
                dir="ltr"
                value={draft.publicKey}
                onChange={(e) => setDraft({ ...draft, publicKey: e.target.value })}
              />
            </label>
            <p className="wizard-step-note">{t("settings_public_key_note")}</p>
          </>
        )}

        <div style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 6 }}>
          <button className="wizard-btn wizard-btn-primary" onClick={save} disabled={!dirty}>
            {t("settings_save")}
          </button>
          {saved && (
            <span className="inline-ok" role="status">
              {t("settings_saved")}
            </span>
          )}
        </div>
      </div>

      <div className="card">
        <strong>{t("settings_wizard_title")}</strong>
        <p style={{ color: "var(--text-muted)", fontSize: "0.88rem" }}>{t("settings_wizard_body")}</p>
        <button className="wizard-btn" onClick={onReopenWizard} style={{ marginTop: 4 }}>
          {t("settings_wizard_button")}
        </button>
      </div>
    </div>
  );
}
