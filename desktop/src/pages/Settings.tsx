import { useLanguage } from "../i18n/LanguageContext";

export function Settings({ onReopenWizard }: { onReopenWizard: () => void }) {
  const { t, lang, setLang } = useLanguage();
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
        <strong>Recording / التسجيل</strong>
        <p style={{ color: "var(--text-muted)", fontSize: "0.88rem" }}>
          Demo mode is active - no agent connection. Recording, privacy, alerts/export, updates and local AI
          settings appear here once a live agent is connected (PRODUCT_RELEASE_PLAN_AR.md §3).
        </p>
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
