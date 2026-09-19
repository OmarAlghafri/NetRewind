import { useLanguage } from "../i18n/LanguageContext";
import { messageFor, type AgentErrorPayload } from "../i18n/agentErrorCatalogue";
import { formatTime } from "../i18n/format";
import { Button } from "./Button";
import { TechnicalValue } from "./TechnicalValue";
import type { Record } from "../data/useRecord";
import type { SourceSettings } from "../data/source";

function isStructuredError(error: Record["error"]): error is AgentErrorPayload {
  return typeof error === "object";
}

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
    // A structured connection failure (ADR 0004 §4.5) gets a translated
    // sentence as the primary message, with agent.rs's own English detail
    // kept available - not discarded - behind a native <details> disclosure
    // rather than shown by default the way the whole rejection used to be
    // wrapped in one TechnicalValue span regardless of what it was.
    const structured = isStructuredError(record.error) ? messageFor(record.error, lang) : null;
    const message = structured
      ? structured.message
      : record.error === "shell_required"
        ? t("status_shell_required")
        : record.error === "no_bundle"
          ? t("status_no_bundle")
          : (record.error as string);
    return (
      <div className="source-banner source-banner-error" role="alert">
        <span>
          <strong>{t("status_error")}</strong> — {message}
          {/* Only when the primary message is an actual translation of
              technical_detail, not a stand-in for it (an unrecognised
              code already shows technical_detail as `message` itself -
              a second copy behind "Technical detail" would just repeat
              it). */}
          {structured?.known && (
            <details className="source-banner-detail">
              <summary>{t("status_technical_detail")}</summary>
              <TechnicalValue>{(record.error as AgentErrorPayload).technical_detail}</TechnicalValue>
            </details>
          )}
        </span>
        <span className="source-banner-actions">
          <Button variant="secondary" onClick={record.refresh}>
            {t("status_retry")}
          </Button>
          {settings.kind !== "demo" && (
            <Button variant="ghost" onClick={onSwitchToDemo}>
              {t("status_switch_demo")}
            </Button>
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
            <TechnicalValue dateTime={record.refreshedAt.toISOString()}>
              {formatTime(record.refreshedAt, lang)}
            </TechnicalValue>
          </span>
        )}
      </span>
    </div>
  );
}
