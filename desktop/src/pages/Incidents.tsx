import { useLanguage } from "../i18n/LanguageContext";
import type { Lang } from "../i18n/translations";
import type { Incident } from "../types";
import { nsToDate } from "../types";
import type { RuleSummary } from "../data/types";
import { IncidentCard } from "../components/IncidentCard";
import { findRule, titleFor } from "../i18n/rulesCatalogue";
import { labelForFamily } from "../i18n/kindCatalogue";
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

export type IncidentSort = "newest" | "severity" | "confidence";
const SEVERITY_RANK: Record<string, number> = { error: 0, warn: 1, notice: 2, info: 3 };

function familyOf(incident: Incident): string {
  return incident.root_cause.kind.split(".")[0] ?? incident.root_cause.kind;
}

/**
 * execution order §9 Phase 5: "Rebuild Incidents (... search/sort/filter)".
 * A pure function (not inlined in the component) so it can be tested
 * directly against fixed fixtures, the same way contextForIncidentExport
 * is. Search matches the *translated* title (what the reader actually
 * sees on screen in the active language), not always the raw English
 * field, plus the always-technical rule_id and root-cause entity - typing
 * an Arabic phrase in Arabic mode must find the incident whose Arabic
 * title contains it, not silently search English text nobody is looking
 * at. Filters and sort are unset-safe: an absent `severities`/`families`
 * means "no filter", not "match nothing".
 */
export function filterAndSortIncidents(
  incidents: Incident[],
  context: InvestigationContext,
  rules: RuleSummary[],
  lang: Lang,
): Incident[] {
  let result = incidents;

  if (context.query?.trim()) {
    const q = context.query.trim().toLowerCase();
    result = result.filter((inc) => {
      const title = titleFor(findRule(inc.rule_id, rules), lang, inc.title);
      return (
        title.toLowerCase().includes(q) ||
        inc.rule_id.toLowerCase().includes(q) ||
        inc.root_cause.entity.toLowerCase().includes(q)
      );
    });
  }
  if (context.severities && context.severities.length > 0) {
    const wanted = new Set(context.severities);
    result = result.filter((inc) => wanted.has(inc.severity));
  }
  if (context.families && context.families.length > 0) {
    const wanted = new Set(context.families);
    result = result.filter((inc) => wanted.has(familyOf(inc)));
  }

  const sort: IncidentSort = (context.sort as IncidentSort) ?? "newest";
  const sorted = [...result];
  if (sort === "severity") {
    sorted.sort(
      (a, b) => (SEVERITY_RANK[a.severity] ?? 9) - (SEVERITY_RANK[b.severity] ?? 9) || b.opened_at - a.opened_at,
    );
  } else if (sort === "confidence") {
    sorted.sort((a, b) => b.confidence - a.confidence || b.opened_at - a.opened_at);
  } else {
    sorted.sort((a, b) => b.opened_at - a.opened_at);
  }
  return sorted;
}

const SEVERITY_OPTIONS = ["error", "warn", "notice", "info"] as const;

export function Incidents({
  incidents,
  rules,
  context = {},
  navigate,
}: {
  incidents: Incident[];
  rules: RuleSummary[];
  context?: InvestigationContext;
  navigate?: (page: Page, context?: InvestigationContext) => void;
}) {
  const { t, lang } = useLanguage();
  const onExport = navigate ? (inc: Incident) => navigate("evidence", contextForIncidentExport(inc)) : undefined;

  const families = Array.from(new Set(incidents.map(familyOf))).sort();
  const filtered = filterAndSortIncidents(incidents, context, rules, lang);

  const patch = (next: Partial<InvestigationContext>) => navigate?.("incidents", { ...context, ...next });
  const toggle = (list: string[] | undefined, value: string): string[] => {
    const current = list ?? [];
    return current.includes(value) ? current.filter((v) => v !== value) : [...current, value];
  };

  return (
    <div>
      <h1 className="page-title">{t("incidents_title")}</h1>
      <p className="page-subtitle">{t("incidents_subtitle")}</p>

      <input
        className="search-box"
        placeholder={t("incidents_search_placeholder")}
        value={context.query ?? ""}
        onChange={(e) => patch({ query: e.target.value || undefined })}
        dir="ltr"
      />

      {incidents.length > 0 && (
        <div className="incidents-filter-bar">
          <div className="incidents-filter-group">
            <span className="incidents-filter-label">{t("incidents_filter_severity_label")}</span>
            {SEVERITY_OPTIONS.map((sev) => (
              <label className="field-check" key={sev}>
                <input
                  type="checkbox"
                  checked={(context.severities ?? []).includes(sev)}
                  onChange={() => patch({ severities: toggle(context.severities, sev) })}
                />
                {t(`severity_${sev}` as const)}
              </label>
            ))}
          </div>

          {families.length > 1 && (
            <div className="incidents-filter-group">
              <span className="incidents-filter-label">{t("incidents_filter_family_label")}</span>
              {families.map((family) => {
                const { name, known } = labelForFamily(family, lang);
                return (
                  <label className="field-check" key={family}>
                    <input
                      type="checkbox"
                      checked={(context.families ?? []).includes(family)}
                      onChange={() => patch({ families: toggle(context.families, family) })}
                    />
                    {known ? name : family}
                  </label>
                );
              })}
            </div>
          )}

          <label className="field-label incidents-sort-select">
            {t("incidents_sort_label")}
            <select
              className="field-input"
              value={context.sort ?? "newest"}
              onChange={(e) => patch({ sort: e.target.value === "newest" ? undefined : e.target.value })}
            >
              <option value="newest">{t("incidents_sort_newest")}</option>
              <option value="severity">{t("incidents_sort_severity")}</option>
              <option value="confidence">{t("incidents_sort_confidence")}</option>
            </select>
          </label>
        </div>
      )}

      {incidents.length === 0 ? (
        <div className="empty-state">{t("incidents_empty")}</div>
      ) : filtered.length === 0 ? (
        <div className="empty-state">{t("incidents_no_matches")}</div>
      ) : (
        filtered.map((inc) => <IncidentCard key={inc.incident_id} incident={inc} rules={rules} onExport={onExport} />)
      )}
    </div>
  );
}
