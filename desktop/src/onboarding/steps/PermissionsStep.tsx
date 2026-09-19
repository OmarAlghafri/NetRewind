import { useLanguage } from "../../i18n/LanguageContext";
import type { Record } from "../../data/useRecord";
import type { SourceSettings } from "../../data/source";

// With a live recorder connected, the checks are real: the recorder answers
// over the local channel, its store is open and counted, and its collectors
// report up/down/unsupported. Without one there is nothing to check, and
// the step says skipped rather than showing a tick for nothing.
export function PermissionsStep({ settings, record }: { settings: SourceSettings; record: Record }) {
  const { t } = useLanguage();
  const live = settings.kind === "live" && record.status === "ready" && record.health;

  return (
    <div>
      <h1 className="page-title">{t("wizard_permissions_title")}</h1>
      <p className="page-subtitle">{t("wizard_permissions_subtitle")}</p>

      {live && record.health ? (
        <div className="card">
          <strong>{t("wizard_permissions_live_title")}</strong>
          <div className="capability-row">
            <span>{t("wizard_permissions_reachable")}</span>
            <span style={{ display: "flex", alignItems: "center" }}>
              <span className="status-dot status-up" />
              <span className="ltr-field">{record.health.observer_id}</span>
            </span>
          </div>
          <div className="capability-row">
            <span>{t("wizard_permissions_store")}</span>
            <span style={{ display: "flex", alignItems: "center" }}>
              <span className={`status-dot ${record.health.store.error ? "status-down" : "status-up"}`} />
              <span className="ltr-field">
                {record.health.store.error
                  ? `${t("wizard_permissions_store_error")}: ${record.health.store.error}`
                  : record.health.store.events}
              </span>
            </span>
          </div>
          <div className="capability-row" style={{ borderBottom: "none" }}>
            <span>{t("wizard_permissions_collectors")}</span>
            <span style={{ display: "flex", alignItems: "center" }}>
              <span className={`status-dot ${record.health.collectors.down > 0 ? "status-down" : "status-up"}`} />
              <span className="ltr-field">
                {record.health.collectors.up} / {record.health.collectors.down} / {record.health.collectors.unsupported}
              </span>
            </span>
          </div>
        </div>
      ) : (
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
      )}
    </div>
  );
}
