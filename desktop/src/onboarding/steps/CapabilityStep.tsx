import { useMemo } from "react";
import { useLanguage } from "../../i18n/LanguageContext";
import type { Incident, NetRewindEvent } from "../../types";

// Computed the same way pages/Overview.tsx already does (distinct
// `source` values actually present in the loaded recording) - real numbers
// from the real demo data already in memory, not placeholders.
export function CapabilityStep({ events, incidents }: { events: NetRewindEvent[]; incidents: Incident[] }) {
  const { t } = useLanguage();

  const sources = useMemo(() => {
    const set = new Set<string>();
    for (const e of events) set.add(e.source);
    return Array.from(set).sort();
  }, [events]);

  return (
    <div>
      <h1 className="page-title">{t("wizard_capability_title")}</h1>
      <p className="page-subtitle">{t("wizard_capability_subtitle")}</p>

      <div className="card">
        <div className="capability-row">
          <span>{t("wizard_capability_events_label")}</span>
          <span className="ltr-field">{events.length}</span>
        </div>
        <div className="capability-row" style={{ borderBottom: "none" }}>
          <span>{t("wizard_capability_incidents_label")}</span>
          <span className="ltr-field">{incidents.length}</span>
        </div>
      </div>

      <div className="card">
        <strong>
          <span className="ltr-field">{sources.length}</span> {t("sources_in_recording")}
        </strong>
        {sources.map((s) => (
          <div className="capability-row" key={s}>
            <span className="ltr-field">{s}</span>
            <span className="status-dot status-up" title={t("seen_in_recording")} />
          </div>
        ))}
      </div>
    </div>
  );
}
