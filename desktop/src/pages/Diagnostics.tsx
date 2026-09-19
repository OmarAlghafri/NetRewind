import { useMemo, useState } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { Lang } from "../i18n/translations";
import type { NetRewindEvent, Incident } from "../types";
import { SeverityBadge } from "../components/SeverityBadge";
import { Button } from "../components/Button";
import { TechnicalValue } from "../components/TechnicalValue";
import { labelForFamily } from "../i18n/kindCatalogue";
import { dict } from "../i18n/translations";
import type { SourceSettings } from "../data/source";
import type { Record } from "../data/useRecord";

declare const __APP_VERSION__: string;

const SEVERITY_ORDER = ["info", "notice", "warn", "error"] as const;

/**
 * "what to copy into a support request" (docs/desktop.md) - a plain-text
 * summary of exactly what this page already shows on screen, nothing more.
 * Secret-free: a bundle path can contain a local username or folder
 * structure (e.g. `C:\Users\jsmith\Desktop\incident.tar.gz`), so only its
 * file name is included, never the full path. Nothing here is a DNS name,
 * an address, or event/incident content - those live in an evidence
 * bundle (which redacts DNS names of its own accord, see docs/api.md),
 * not this summary, which exists specifically so a user does not have to
 * hand over a bundle just to tell support what version and source they
 * are running.
 *
 * Labels come from the same dictionary the page itself renders with
 * (`dict[lang]`, not `t()` - a pure function, called with an explicit
 * language rather than through the hook, so it can be unit tested without
 * mounting a component) - the summary reads in whichever language the
 * screen it was copied from was showing, consistent with what the person
 * pasting it actually saw.
 */
export function buildSupportSummary(params: {
  lang: Lang;
  appVersion: string;
  sourceKind: string;
  endpoint?: string;
  bundlePath?: string;
  lastRefresh: string;
  eventsTotal: number;
  incidentsTotal: number;
  byFamily: (readonly [string, number])[];
  bySeverity: (readonly [string, number])[];
}): string {
  const d = dict[params.lang];
  const lines: string[] = [
    "NetRewind",
    `${d.diagnostics_app_version}: ${params.appVersion}`,
    `${d.diagnostics_source_kind}: ${params.sourceKind}`,
  ];
  if (params.endpoint !== undefined) {
    lines.push(`${d.diagnostics_endpoint}: ${params.endpoint || "(default)"}`);
  }
  if (params.bundlePath !== undefined) {
    const fileName = params.bundlePath.split(/[/\\]/).pop() || params.bundlePath;
    lines.push(`${d.diagnostics_bundle_path}: ${fileName}`);
  }
  lines.push(`${d.diagnostics_last_refresh}: ${params.lastRefresh}`);
  lines.push(`${d.diagnostics_events_label}: ${params.eventsTotal}`);
  lines.push(`${d.diagnostics_incidents_label}: ${params.incidentsTotal}`);
  if (params.byFamily.length > 0) {
    lines.push("", `${d.diagnostics_by_family_title}:`);
    for (const [family, count] of params.byFamily) lines.push(`  ${family}: ${count}`);
  }
  if (params.bySeverity.length > 0) {
    lines.push("", `${d.diagnostics_by_severity_title}:`);
    for (const [severity, count] of params.bySeverity) lines.push(`  ${severity}: ${count}`);
  }
  return lines.join("\n");
}

// Everything here is computed client-side from events/incidents already in
// memory - no new data source, and nothing a support engineer couldn't
// re-derive by hand from the same recording. "Family" is the part of an
// event's "kind" before the first "." (e.g. "link" from "link.down"),
// mirroring how internal/correlate's own rule files group kinds (rules/*.yaml
// "kinds:" lists).
export function Diagnostics({
  events,
  incidents,
  settings,
  record,
}: {
  events: NetRewindEvent[];
  incidents: Incident[];
  settings: SourceSettings;
  record: Record;
}) {
  const { t, lang } = useLanguage();
  const [copyStatus, setCopyStatus] = useState<"idle" | "copied" | "failed">("idle");

  const byFamily = useMemo(() => {
    const counts = new Map<string, number>();
    for (const e of events) {
      const family = e.kind.split(".")[0] ?? e.kind;
      counts.set(family, (counts.get(family) ?? 0) + 1);
    }
    return Array.from(counts.entries()).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  }, [events]);

  const bySeverity = useMemo(() => {
    const counts = new Map<string, number>();
    for (const e of events) {
      counts.set(e.severity, (counts.get(e.severity) ?? 0) + 1);
    }
    return SEVERITY_ORDER.filter((s) => counts.has(s)).map((s) => [s, counts.get(s) as number] as const);
  }, [events]);

  const supportSummary = useMemo(
    () =>
      buildSupportSummary({
        lang,
        appVersion: __APP_VERSION__,
        sourceKind: settings.kind,
        endpoint: settings.kind === "live" ? settings.endpoint : undefined,
        bundlePath: settings.kind === "bundle" ? settings.bundlePath : undefined,
        lastRefresh: record.refreshedAt
          ? record.refreshedAt.toLocaleTimeString(lang === "ar" ? "ar-EG" : "en-US", { hour12: false })
          : "-",
        eventsTotal: events.length,
        incidentsTotal: incidents.length,
        byFamily,
        bySeverity,
      }),
    [lang, settings, record.refreshedAt, events.length, incidents.length, byFamily, bySeverity],
  );

  const copySummary = async () => {
    try {
      await navigator.clipboard.writeText(supportSummary);
      setCopyStatus("copied");
    } catch {
      setCopyStatus("failed");
    }
    window.setTimeout(() => setCopyStatus("idle"), 2500);
  };

  return (
    <div>
      <h1 className="page-title">{t("diagnostics_title")}</h1>
      <p className="page-subtitle">{t("diagnostics_subtitle")}</p>

      <div className="card">
        <strong>{t("diagnostics_source_title")}</strong>
        <div className="capability-row">
          <span>{t("diagnostics_app_version")}</span>
          <span className="ltr-field">{__APP_VERSION__}</span>
        </div>
        <div className="capability-row">
          <span>{t("diagnostics_source_kind")}</span>
          <span className="ltr-field">{settings.kind}</span>
        </div>
        {settings.kind === "live" && (
          <div className="capability-row">
            <span>{t("diagnostics_endpoint")}</span>
            <span className="ltr-field">{settings.endpoint || "(default)"}</span>
          </div>
        )}
        {settings.kind === "bundle" && (
          <div className="capability-row">
            <span>{t("diagnostics_bundle_path")}</span>
            <span className="ltr-field" style={{ wordBreak: "break-all" }}>
              {settings.bundlePath}
            </span>
          </div>
        )}
        <div className="capability-row" style={{ borderBottom: "none" }}>
          <span>{t("diagnostics_last_refresh")}</span>
          <span className="ltr-field">
            {record.refreshedAt
              ? record.refreshedAt.toLocaleTimeString(lang === "ar" ? "ar-EG" : "en-US", { hour12: false })
              : "-"}
          </span>
        </div>
      </div>

      <div className="card">
        <strong>{t("diagnostics_totals_title")}</strong>
        <div className="capability-row">
          <span>{t("diagnostics_events_label")}</span>
          <span className="ltr-field">{events.length}</span>
        </div>
        <div className="capability-row">
          <span>{t("diagnostics_incidents_label")}</span>
          <span className="ltr-field">{incidents.length}</span>
        </div>
      </div>

      <div className="card">
        <strong>{t("diagnostics_by_family_title")}</strong>
        {byFamily.length === 0 ? (
          <div className="empty-state">{t("diagnostics_empty")}</div>
        ) : (
          byFamily.map(([family, count]) => {
            const { name, known } = labelForFamily(family, lang);
            return (
              <div className="capability-row" key={family}>
                <span>
                  {known && <>{name} </>}
                  <TechnicalValue>{family}</TechnicalValue>
                </span>
                <span>
                  <span className="ltr-field">{count}</span> {t("diagnostics_event_count_label")}
                </span>
              </div>
            );
          })
        )}
      </div>

      <div className="card">
        <strong>{t("diagnostics_by_severity_title")}</strong>
        {bySeverity.length === 0 ? (
          <div className="empty-state">{t("diagnostics_empty")}</div>
        ) : (
          bySeverity.map(([severity, count]) => (
            <div className="capability-row" key={severity}>
              <SeverityBadge severity={severity} />
              <span className="ltr-field">{count}</span>
            </div>
          ))
        )}
      </div>

      <div className="card">
        <strong>{t("diagnostics_support_summary_title")}</strong>
        <p style={{ color: "var(--text-muted)", fontSize: "0.88rem", marginTop: 6 }}>
          {t("diagnostics_support_summary_body")}
        </p>
        <textarea
          className="field-input ltr-field support-summary-preview"
          dir="ltr"
          readOnly
          rows={8}
          value={supportSummary}
          aria-label={t("diagnostics_support_summary_title")}
        />
        <div style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 8 }}>
          <Button variant="secondary" onClick={copySummary}>
            {t("diagnostics_copy_summary_button")}
          </Button>
          {copyStatus === "copied" && (
            <span className="inline-ok" role="status">
              {t("diagnostics_copy_summary_done")}
            </span>
          )}
          {copyStatus === "failed" && (
            <span className="inline-error" role="alert">
              {t("diagnostics_copy_summary_failed")}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
