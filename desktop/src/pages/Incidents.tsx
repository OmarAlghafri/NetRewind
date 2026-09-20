import { useLanguage } from "../i18n/LanguageContext";
import type { Lang } from "../i18n/translations";
import type { Incident, NetRewindEvent } from "../types";
import { nsToDate } from "../types";
import type { RuleSummary } from "../data/types";
import { IncidentCard } from "../components/IncidentCard";
import { InspectorPanel } from "../components/InspectorPanel";
import { SeverityBadge } from "../components/SeverityBadge";
import { TechnicalValue } from "../components/TechnicalValue";
import { AiAssistantPanel } from "../components/ai/AiAssistantPanel";
import { AI_FEATURE_ENABLED } from "../data/aiFeature";
import { findRule, titleFor } from "../i18n/rulesCatalogue";
import { formatTime } from "../i18n/format";
import { labelForFamily } from "../i18n/kindCatalogue";
import type { Page } from "../components/Sidebar";
import type { InvestigationContext } from "../routing/useRoute";
import type { SourceSettings } from "../data/source";

/** How far outside an incident's own [opened, end] span the local-AI
 *  assistant's evidence window reaches - matching internal/ai's own
 *  explainWindowEvents (cmd/netrewind/explain.go), so what the desktop
 *  panel offers a model is the same window the CLI's `netrewind explain`
 *  would. */
const AI_WINDOW_PAD_NS = 5 * 60 * 1e9;
/** A budget on how many events the panel offers, not a hard protocol
 *  limit - a "nearest events first, chain events always included" trim
 *  is a nicer version of this to build once a real model/window makes the
 *  difference visible; for now this keeps a busy window from becoming an
 *  unbounded prompt. */
const AI_MAX_EVENTS = 200;

/** The event window offered to the local-AI assistant for one incident:
 *  every event in [opened_at - pad, end + pad], always including every
 *  chain-cited event even in the (should not happen) case one fell
 *  outside that span, oldest first. */
export function selectAiEvents(incident: Incident, allEvents: NetRewindEvent[]): NetRewindEvent[] {
  const lastLinkAt = incident.chain.length > 0 ? incident.chain[incident.chain.length - 1].at : incident.opened_at;
  const end = incident.closed_at ?? lastLinkAt;
  const start = incident.opened_at - AI_WINDOW_PAD_NS;
  const stop = end + AI_WINDOW_PAD_NS;
  const chainIds = new Set(incident.chain.map((l) => l.event_id));

  const inWindow = allEvents.filter((e) => e.ts_wall >= start && e.ts_wall <= stop);
  const inWindowIds = new Set(inWindow.map((e) => e.event_id));
  const missingChainEvents = allEvents.filter((e) => chainIds.has(e.event_id) && !inWindowIds.has(e.event_id));

  return [...inWindow, ...missingChainEvents].sort((a, b) => a.ts_wall - b.ts_wall).slice(0, AI_MAX_EVENTS);
}

/** The prior-incident pool RankSimilar (internal/ai, Go) chooses from -
 *  every other incident sharing this one's rule, letting the Go side's own
 *  weighting decide which (if any) are actually offered as H-handles. */
export function selectAiHistory(incident: Incident, allIncidents: Incident[]): Incident[] {
  return allIncidents.filter((i) => i.incident_id !== incident.incident_id && i.rule_id === incident.rule_id);
}

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

/**
 * The "master" half of master/detail (execution order §9 Phase 5): a
 * compact, single-line summary - time, severity, translated title,
 * confidence - deliberately without the chain/evidence/advice detail
 * `IncidentCard` renders, so a list long enough to need this view is not
 * just the same list at a smaller font.
 */
function IncidentRow({
  incident,
  rule,
  selected,
  onSelect,
  lang,
}: {
  incident: Incident;
  rule: RuleSummary | undefined;
  selected: boolean;
  onSelect: () => void;
  lang: Lang;
}) {
  const { t } = useLanguage();
  const title = titleFor(rule, lang, incident.title);
  return (
    <button
      type="button"
      className={`incident-row${selected ? " incident-row-selected" : ""}`}
      onClick={onSelect}
      aria-pressed={selected}
    >
      <TechnicalValue dateTime={nsToDate(incident.opened_at).toISOString()}>
        {formatTime(nsToDate(incident.opened_at), lang)}
      </TechnicalValue>
      <SeverityBadge severity={incident.severity} />
      <span className="incident-row-title">{title}</span>
      <span className="incident-row-confidence">
        {t("confidence")}: <span className="ltr-field">{incident.confidence}%</span>
      </span>
    </button>
  );
}

export function Incidents({
  incidents,
  rules,
  events = [],
  context = {},
  navigate,
  settings,
}: {
  incidents: Incident[];
  rules: RuleSummary[];
  /** Optional: absent callers (the onboarding wizard's sample step) get no
   *  local-AI panel rather than one with nothing to offer a model. */
  events?: NetRewindEvent[];
  context?: InvestigationContext;
  navigate?: (page: Page, context?: InvestigationContext) => void;
  /** Optional for the same reason as `events` - the source (endpoint,
   *  kind) the local-AI panel needs to read/write operator notes. */
  settings?: SourceSettings;
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

  // Master/detail (execution order §9 Phase 5) is opt-in, entered by
  // selecting a row: with nothing selected the page renders exactly as it
  // always has (every filtered incident as a full IncidentCard) - no
  // regression to the default experience, and every test written before
  // this feature existed keeps passing unchanged because none of them
  // ever set context.selection.
  const selected = context.selection ? filtered.find((inc) => inc.incident_id === context.selection) : undefined;
  const select = (id: string | undefined) => patch({ selection: id });

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
      ) : selected ? (
        <div className="incidents-layout">
          <div className="incidents-list">
            {filtered.map((inc) => (
              <IncidentRow
                key={inc.incident_id}
                incident={inc}
                rule={findRule(inc.rule_id, rules)}
                selected={inc.incident_id === selected.incident_id}
                onSelect={() => select(inc.incident_id === selected.incident_id ? undefined : inc.incident_id)}
                lang={lang}
              />
            ))}
          </div>
          <InspectorPanel title={titleFor(findRule(selected.rule_id, rules), lang, selected.title)} onClose={() => select(undefined)}>
            <IncidentCard incident={selected} rules={rules} onExport={onExport} />
            {AI_FEATURE_ENABLED && events.length > 0 && (
              <AiAssistantPanel
                incident={selected}
                events={selectAiEvents(selected, events)}
                history={selectAiHistory(selected, incidents)}
                navigate={navigate}
                settings={settings}
              />
            )}
          </InspectorPanel>
        </div>
      ) : (
        filtered.map((inc) => (
          <IncidentRowEntry key={inc.incident_id} incident={inc} rules={rules} onExport={onExport} onSelect={select} />
        ))
      )}
    </div>
  );
}

/**
 * The default (nothing selected) rendering: the full IncidentCard, same
 * as before master/detail existed, plus a small affordance to enter
 * master/detail by selecting this incident - a link the size of the
 * title, not a whole extra row, so it does not compete with the
 * export button for attention.
 */
function IncidentRowEntry({
  incident,
  rules,
  onExport,
  onSelect,
}: {
  incident: Incident;
  rules: RuleSummary[];
  onExport?: (incident: Incident) => void;
  onSelect: (id: string) => void;
}) {
  const { t } = useLanguage();
  return (
    <div className="incident-card-wrapper">
      <IncidentCard incident={incident} rules={rules} onExport={onExport} />
      <button type="button" className="incident-focus-link" onClick={() => onSelect(incident.incident_id)}>
        {t("incidents_focus_link")}
      </button>
    </div>
  );
}
