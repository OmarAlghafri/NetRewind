import { useLanguage } from "../i18n/LanguageContext";
import type { NetRewindEvent } from "../types";
import { nsToDate } from "../types";
import { SeverityBadge } from "../components/SeverityBadge";
import { TechnicalValue } from "../components/TechnicalValue";
import { formatTime } from "../i18n/format";

export function Timeline({ events }: { events: NetRewindEvent[] }) {
  const { t, lang } = useLanguage();
  const sorted = [...events].sort((a, b) => a.ts_wall - b.ts_wall);

  return (
    <div>
      <h1 className="page-title">{t("timeline_title")}</h1>
      <p className="page-subtitle">{t("timeline_subtitle")}</p>
      <div className="card">
        {sorted.length === 0 ? (
          <div className="empty-state">{t("incidents_empty")}</div>
        ) : (
          sorted.map((e) => (
            <div className="timeline-row" key={e.event_id}>
              <TechnicalValue className="timeline-time" dateTime={nsToDate(e.ts_wall).toISOString()}>
                {formatTime(nsToDate(e.ts_wall), lang)}
              </TechnicalValue>
              <SeverityBadge severity={e.severity} />
              <span className="ltr-field" style={{ fontWeight: 600 }}>
                {e.kind}
              </span>
              <span className="ltr-field" style={{ color: "var(--text-muted)" }}>
                {e.subject?.label}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
