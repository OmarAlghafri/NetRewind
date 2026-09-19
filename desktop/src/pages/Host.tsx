import { useMemo, useState } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { NetRewindEvent } from "../types";
import { nsToDate } from "../types";
import { SeverityBadge } from "../components/SeverityBadge";
import { TechnicalValue } from "../components/TechnicalValue";
import { formatTime } from "../i18n/format";

// Mirrors internal/web's /host?q= route: "everything recorded about this
// machine in the window - including events recorded while it answered to a
// different address" (the identity-resolution behaviour), except this reads
// against whatever data source is loaded (demo fixture today, the local API
// once an agent connection exists) rather than a direct SQLite query.
export function Host({ events }: { events: NetRewindEvent[] }) {
  const { t, lang } = useLanguage();
  const [query, setQuery] = useState("");

  const matches = useMemo(() => {
    if (!query.trim()) return [];
    const q = query.trim().toLowerCase();
    return events
      .filter((e) => {
        if (e.subject?.label?.toLowerCase().includes(q)) return true;
        if (e.subject?.id?.toLowerCase().includes(q)) return true;
        const attrs = e.attrs ?? {};
        return Object.values(attrs).some((v) => String(v).toLowerCase().includes(q));
      })
      .sort((a, b) => a.ts_wall - b.ts_wall);
  }, [events, query]);

  return (
    <div>
      <h1 className="page-title">{t("host_title")}</h1>
      <input
        className="search-box"
        placeholder={t("host_placeholder")}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        dir="ltr"
      />
      {query.trim() === "" ? null : matches.length === 0 ? (
        <div className="empty-state">{t("incidents_empty")}</div>
      ) : (
        <div className="card">
          {matches.map((e) => (
            <div className="timeline-row" key={e.event_id}>
              <TechnicalValue className="timeline-time" dateTime={nsToDate(e.ts_wall).toISOString()}>
                {formatTime(nsToDate(e.ts_wall), lang)}
              </TechnicalValue>
              <SeverityBadge severity={e.severity} />
              <span className="ltr-field" style={{ fontWeight: 600 }}>
                {e.kind}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
