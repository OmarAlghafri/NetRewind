import { useLanguage } from "../i18n/LanguageContext";
import type { Incident } from "../types";
import { IncidentCard } from "../components/IncidentCard";

export function Incidents({ incidents }: { incidents: Incident[] }) {
  const { t } = useLanguage();
  const sorted = [...incidents].sort((a, b) => b.opened_at - a.opened_at);

  return (
    <div>
      <h1 className="page-title">{t("incidents_title")}</h1>
      <p className="page-subtitle">{t("incidents_subtitle")}</p>
      {sorted.length === 0 ? (
        <div className="empty-state">{t("incidents_empty")}</div>
      ) : (
        sorted.map((inc) => <IncidentCard key={inc.incident_id} incident={inc} />)
      )}
    </div>
  );
}
