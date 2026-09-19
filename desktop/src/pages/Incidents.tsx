import { useLanguage } from "../i18n/LanguageContext";
import type { Incident } from "../types";
import { nsToDate } from "../types";
import type { RuleSummary } from "../data/types";
import { IncidentCard } from "../components/IncidentCard";
import type { Page } from "../components/Sidebar";
import type { InvestigationContext } from "../routing/useRoute";

// PRD U5: exporting "a specific incident" means the Evidence page's window
// is this incident's own, not "the last hour" - opened_at to closed_at, or
// to the last chain link's own time for an incident still open (a better
// proxy for "when this was actually observed" than the export moment,
// which could be hours later). Evidence.tsx applies the adjustable padding
// around this core window; this only says what the core window is.
export function contextForIncidentExport(incident: Incident): InvestigationContext {
  const lastLinkAt = incident.chain.length > 0 ? incident.chain[incident.chain.length - 1].at : undefined;
  const to = incident.closed_at ?? lastLinkAt ?? incident.opened_at;
  return {
    from: nsToDate(incident.opened_at).toISOString(),
    to: nsToDate(to).toISOString(),
    selection: incident.incident_id,
  };
}

export function Incidents({
  incidents,
  rules,
  navigate,
}: {
  incidents: Incident[];
  rules: RuleSummary[];
  navigate?: (page: Page, context?: InvestigationContext) => void;
}) {
  const { t } = useLanguage();
  const sorted = [...incidents].sort((a, b) => b.opened_at - a.opened_at);
  const onExport = navigate ? (inc: Incident) => navigate("evidence", contextForIncidentExport(inc)) : undefined;

  return (
    <div>
      <h1 className="page-title">{t("incidents_title")}</h1>
      <p className="page-subtitle">{t("incidents_subtitle")}</p>
      {sorted.length === 0 ? (
        <div className="empty-state">{t("incidents_empty")}</div>
      ) : (
        sorted.map((inc) => <IncidentCard key={inc.incident_id} incident={inc} rules={rules} onExport={onExport} />)
      )}
    </div>
  );
}
