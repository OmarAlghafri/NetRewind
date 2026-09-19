import { useLanguage } from "../../i18n/LanguageContext";

// The claims in the five points below are drawn directly from
// docs/product/data-policy.md and docs/product/threat-model.md - no payload
// capture (architectural, internal/event), DNS names behind a dedicated
// switch, local-only SQLite storage by default, local-AI-stays-local, and
// evidence bundles redacted unless the user opts in - nothing beyond what
// those documents and the running Demo mode actually support.
//
// execution order §5.3: the original single long paragraph is split into
// five short points (what is recorded, what is not, where it is stored,
// what ever leaves the device, what an exported bundle may contain) -
// same facts, restructured for a reader who skims rather than reads a wall
// of text before continuing the wizard.
const PRIVACY_POINTS = [
  "wizard_privacy_point_recorded",
  "wizard_privacy_point_not_recorded",
  "wizard_privacy_point_storage",
  "wizard_privacy_point_device",
  "wizard_privacy_point_bundle",
] as const;

export function PrivacyStep() {
  const { t } = useLanguage();
  return (
    <div>
      <h1 className="page-title">{t("wizard_privacy_title")}</h1>
      <p className="page-subtitle">{t("wizard_privacy_subtitle")}</p>
      <div className="card">
        <ul style={{ margin: 0, paddingInlineStart: "1.3em", fontSize: "0.92rem", lineHeight: 1.6 }}>
          {PRIVACY_POINTS.map((key) => (
            <li key={key} style={{ marginTop: 6 }}>
              {t(key)}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
