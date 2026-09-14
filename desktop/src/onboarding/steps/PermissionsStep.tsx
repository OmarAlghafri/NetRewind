import { useLanguage } from "../../i18n/LanguageContext";

// There is no live agent or real data folder in this build (Demo mode
// only), so there is nothing to actually check permissions or disk space
// against. Per DECISIONS.md #10's discipline against implying a capability
// the app has not actually proven, this step states honestly what a real
// connection would check (disk space per docs/product/data-policy.md's
// retention tiers, data-folder ACLs, and the agent's own network
// privilege per docs/product/threat-model.md §3.3) and marks the check
// itself as skipped, not passed.
export function PermissionsStep() {
  const { t } = useLanguage();
  return (
    <div>
      <h1 className="page-title">{t("wizard_permissions_title")}</h1>
      <p className="page-subtitle">{t("wizard_permissions_subtitle")}</p>

      <div className="card">
        <strong>{t("wizard_permissions_would_check_title")}</strong>
        <ul style={{ marginBottom: 0, paddingInlineStart: "1.3em" }}>
          <li>{t("wizard_permissions_check_disk")}</li>
          <li>{t("wizard_permissions_check_folder")}</li>
          <li>{t("wizard_permissions_check_privilege")}</li>
        </ul>
      </div>

      <div className="card">
        <div className="capability-row">
          <span>{t("wizard_permissions_status_label")}</span>
          <span style={{ display: "flex", alignItems: "center" }}>
            <span className="status-dot status-unknown" />
            {t("wizard_permissions_skipped")}
          </span>
        </div>
        <p className="wizard-step-note">{t("wizard_permissions_note")}</p>
      </div>
    </div>
  );
}
