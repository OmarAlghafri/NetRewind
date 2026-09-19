import { useMemo } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { Incident } from "../types";
import type { RuleSummary } from "../data/types";
import { getStaticRule, titleFor } from "../i18n/rulesCatalogue";
import { SeverityBadge } from "../components/SeverityBadge";
import { Button } from "../components/Button";
import type { Page } from "../components/Sidebar";
import type { InvestigationContext } from "../routing/useRoute";

/**
 * execution order §9 Phase 5: Rules "bilingual, match-count in range".
 * Absolute `from`/`to` (`InvestigationContext`'s existing fields), not a
 * relative "last 1h/24h/7d" window like Evidence.tsx's export selector:
 * that pattern computes the window against `new Date()` (now), which is
 * correct for a *live* recorder but would silently show zero matches for
 * demo/bundle incidents - fixed historical timestamps, not recent ones -
 * the exact regression this project's own discipline exists to catch
 * before shipping. Absolute bounds work identically regardless of source
 * mode; unset means no filter, today's exact behaviour.
 */
export function incidentsInRange(incidents: Incident[], context: InvestigationContext): Incident[] {
  const fromMs = context.from ? Date.parse(context.from) : undefined;
  const toMs = context.to ? Date.parse(context.to) : undefined;
  if ((fromMs === undefined || Number.isNaN(fromMs)) && (toMs === undefined || Number.isNaN(toMs))) {
    return incidents;
  }
  return incidents.filter((inc) => {
    const openedMs = inc.opened_at / 1e6;
    if (fromMs !== undefined && !Number.isNaN(fromMs) && openedMs < fromMs) return false;
    if (toMs !== undefined && !Number.isNaN(toMs) && openedMs > toMs) return false;
    return true;
  });
}

// <input type="datetime-local"> speaks "YYYY-MM-DDTHH:mm" in local time,
// not an ISO string with a timezone - converted at the edges so
// InvestigationContext keeps carrying real, unambiguous ISO instants.
function toDatetimeLocal(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromDatetimeLocal(value: string): string | undefined {
  if (!value) return undefined;
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

// With a live recorder the full catalogue it loaded is listed (from
// /v1/rules), with how often each rule concluded an incident in the loaded
// window. Without one (demo, bundle) only the rules that actually fired can
// be known, so this list can only ever be a count against a bare rule ID -
// except for a title, which the build-time rules.json snapshot can supply
// even with no live catalogue (ADR 0004), so a known rule at least reads as
// itself rather than an opaque id.
export function Rules({
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
  const inRange = incidentsInRange(incidents, context);

  const fired = useMemo(() => {
    const counts = new Map<string, number>();
    for (const inc of inRange) {
      counts.set(inc.rule_id, (counts.get(inc.rule_id) ?? 0) + 1);
    }
    return counts;
  }, [inRange]);

  const setRange = (patch: Partial<Pick<InvestigationContext, "from" | "to">>) =>
    navigate?.("rules", { ...context, ...patch });

  const rangeBar = navigate && (
    <div className="incidents-filter-bar">
      <label className="field-label">
        {t("rules_range_from_label")}
        <input
          type="datetime-local"
          className="field-input"
          dir="ltr"
          value={toDatetimeLocal(context.from)}
          onChange={(e) => setRange({ from: fromDatetimeLocal(e.target.value) })}
        />
      </label>
      <label className="field-label">
        {t("rules_range_to_label")}
        <input
          type="datetime-local"
          className="field-input"
          dir="ltr"
          value={toDatetimeLocal(context.to)}
          onChange={(e) => setRange({ to: fromDatetimeLocal(e.target.value) })}
        />
      </label>
      {(context.from || context.to) && (
        <Button variant="ghost" onClick={() => setRange({ from: undefined, to: undefined })}>
          {t("rules_range_clear")}
        </Button>
      )}
    </div>
  );

  if (rules.length > 0) {
    const sorted = [...rules].sort(
      (a, b) => (fired.get(b.id) ?? 0) - (fired.get(a.id) ?? 0) || a.id.localeCompare(b.id),
    );
    return (
      <div>
        <h1 className="page-title">{t("rules_title")}</h1>
        <p className="page-subtitle">{t("rules_catalog_title")}</p>
        {rangeBar}
        <div className="card">
          {sorted.map((r) => {
            const n = fired.get(r.id) ?? 0;
            return (
              <div className="capability-row capability-row-detail" key={r.id}>
                <div className="capability-main">
                  <span>
                    <span className="ltr-field capability-name">{r.id}</span>
                    <span style={{ marginInlineStart: 8 }}>
                      <SeverityBadge severity={r.severity as "info" | "notice" | "warn" | "error"} />
                    </span>
                  </span>
                  <span style={{ color: n > 0 ? "var(--text)" : "var(--text-muted)" }}>
                    {n > 0 ? (
                      <>
                        <span className="ltr-field">{n}</span> {t("rules_fired_count")}
                      </>
                    ) : (
                      t("rules_never_fired")
                    )}
                  </span>
                </div>
                <div style={{ marginTop: 4 }}>{titleFor(r, lang, r.title)}</div>
                <div className="capability-detail">
                  <span>
                    {t("rules_col_confidence")}: <span className="ltr-field">{r.confidence}%</span>
                  </span>
                  <span>
                    {t("rules_col_window")}: <span className="ltr-field">{r.window}</span>
                  </span>
                  <span>
                    {t("rules_root_cause")}: <span className="ltr-field">{r.root_cause}</span>
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    );
  }

  const firedList = Array.from(fired.entries()).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  return (
    <div>
      <h1 className="page-title">{t("rules_title")}</h1>
      <p className="page-subtitle">{t("rules_subtitle")}</p>
      {rangeBar}

      <div className="advice-box">{t("rules_note")}</div>

      <div className="card" style={{ marginTop: 14 }}>
        {firedList.length === 0 ? (
          <div className="empty-state">{t("rules_empty")}</div>
        ) : (
          firedList.map(([ruleId, count]) => {
            const known = getStaticRule(ruleId);
            return (
              <div className="capability-row" key={ruleId}>
                <span>
                  {known && <>{titleFor(known, lang, known.title)} </>}
                  <span className="ltr-field">{ruleId}</span>
                </span>
                <span>
                  <span className="ltr-field">{count}</span> {t("rules_fired_count")}
                </span>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
