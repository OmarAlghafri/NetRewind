import { useLanguage } from "../../i18n/LanguageContext";

// The claims in wizard_privacy_body are drawn directly from
// docs/product/data-policy.md and docs/product/threat-model.md - no payload
// capture (architectural, internal/event), DNS names behind a dedicated
// switch, local-only SQLite storage by default, local-AI-stays-local, and
// evidence bundles redacted unless the user opts in - nothing beyond what
// those documents and the running Demo mode actually support. The closing
// sentence about Settings matches the real, current limitation already
// stated in pages/Settings.tsx ("Recording, privacy... settings appear here
// once a live agent is connected").
export function PrivacyStep() {
  const { t } = useLanguage();
  return (
    <div>
      <h1 className="page-title">{t("wizard_privacy_title")}</h1>
      <p className="page-subtitle">{t("wizard_privacy_subtitle")}</p>
      <div className="card">
        <p style={{ margin: 0, fontSize: "0.92rem", lineHeight: 1.6 }}>{t("wizard_privacy_body")}</p>
      </div>
    </div>
  );
}
