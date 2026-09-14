import { useMemo } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { NetRewindEvent, Incident } from "../types";
import { SeverityBadge } from "../components/SeverityBadge";

const SEVERITY_ORDER = ["info", "notice", "warn", "error"] as const;

// Everything here is computed client-side from events/incidents already in
// memory - no new data source, and nothing a support engineer couldn't
// re-derive by hand from the same recording. "Family" is the part of an
// event's "kind" before the first "." (e.g. "link" from "link.down"),
// mirroring how internal/correlate's own rule files group kinds (rules/*.yaml
// "kinds:" lists).
export function Diagnostics({ events, incidents }: { events: NetRewindEvent[]; incidents: Incident[] }) {
  const { t } = useLanguage();

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

  return (
    <div>
      <h1 className="page-title">{t("diagnostics_title")}</h1>
      <p className="page-subtitle">{t("diagnostics_subtitle")}</p>

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
          byFamily.map(([family, count]) => (
            <div className="capability-row" key={family}>
              <span className="ltr-field">{family}</span>
              <span>
                <span className="ltr-field">{count}</span> {t("diagnostics_event_count_label")}
              </span>
            </div>
          ))
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
    </div>
  );
}
