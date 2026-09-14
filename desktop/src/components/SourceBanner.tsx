import { useLanguage } from "../i18n/LanguageContext";
import type { Record } from "../data/useRecord";
import type { SourceSettings } from "../data/source";

// The one line at the top of every page that says where the record on
// screen comes from and whether it is current. An error here is the whole
// story for a live source (the recorder is not reachable), so it carries
// the recorder's own message and a retry, not a generic "something went
// wrong".
export function SourceBanner({
  settings,
  record,
  onSwitchToDemo,
}: {
  settings: SourceSettings;
  record: Record;
  onSwitchToDemo: () => void;
}) {
  const { t, lang } = useLanguage();

  const label =
    settings.kind === "live" ? t("live_banner") : settings.kind === "bundle" ? t("bundle_banner") : t("demo_banner");

  if (record.status === "error") {
    const message =
      record.error === "shell_required"
        ? t("status_shell_required")
        : record.error === "no_bundle"
          ? t("status_no_bundle")
          : record.error;
    return (
      <div className="source-banner source-banner-error" role="alert">
        <span>
          <strong>{t("status_error")}</strong> — <span className="ltr-field">{message}</span>
        </span>
        <span className="source-banner-actions">
          <button className="wizard-btn" onClick={record.refresh}>
            {t("status_retry")}
          </button>
          {settings.kind !== "demo" && (
            <button className="wizard-btn wizard-btn-ghost" onClick={onSwitchToDemo}>
              {t("status_switch_demo")}
            </button>
          )}
        </span>
      </div>
    );
  }

  const cls = settings.kind === "live" ? "source-banner source-banner-live" : "source-banner";
  return (
    <div className={cls} role="status">
      <span>{label}</span>
      <span className="source-banner-meta">
        {record.status === "loading" && <span>{t("status_loading")}</span>}
        {record.status === "ready" && record.refreshedAt && settings.kind === "live" && (
          <span>
            {t("status_refreshed")}{" "}
            <span className="ltr-field">
              {record.refreshedAt.toLocaleTimeString(lang === "ar" ? "ar-EG" : "en-US", { hour12: false })}
            </span>
          </span>
        )}
      </span>
    </div>
  );
}
