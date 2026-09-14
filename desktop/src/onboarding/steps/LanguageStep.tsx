import { useLanguage } from "../../i18n/LanguageContext";

// PRODUCT_RELEASE_PLAN_AR.md §5, Phase 2, first wizard step: "اختيار
// اللغة". Calls the same setLang() used by Sidebar.tsx/Settings.tsx's
// language toggle, so the choice takes effect immediately, everywhere -
// this step is not a separate, disconnected preference.
export function LanguageStep() {
  const { t, lang, setLang } = useLanguage();
  return (
    <div>
      <h1 className="page-title">{t("wizard_language_title")}</h1>
      <p className="page-subtitle">{t("wizard_language_subtitle")}</p>
      <div className="lang-toggle" style={{ maxWidth: 280, marginTop: 4 }}>
        <button className={lang === "ar" ? "active" : ""} onClick={() => setLang("ar")}>
          العربية
        </button>
        <button className={lang === "en" ? "active" : ""} onClick={() => setLang("en")}>
          English
        </button>
      </div>
    </div>
  );
}
