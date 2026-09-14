import { useMemo } from "react";
import { useLanguage } from "../../i18n/LanguageContext";
import type { Record } from "../../data/useRecord";
import { CapabilityTable } from "../../components/CapabilityTable";

// Real numbers from the record that is actually loaded. With a live
// recorder (or a bundle, whose manifest carries the producing recorder's
// registry) the capability matrix itself is shown; in demo mode only the
// sources present in the recording can be known.
export function CapabilityStep({ record }: { record: Record }) {
  const { t } = useLanguage();
  const { events, incidents, capabilities } = record;

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

      {capabilities.length > 0 ? (
        <div className="card">
          <strong>{t("capabilities_title")}</strong>
          <CapabilityTable capabilities={capabilities} />
        </div>
      ) : (
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
      )}
    </div>
  );
}
