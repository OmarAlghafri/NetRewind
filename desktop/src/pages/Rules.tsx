import { useMemo } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { Incident } from "../types";
import type { RuleSummary } from "../data/types";
import { getStaticRule, titleFor } from "../i18n/rulesCatalogue";
import { SeverityBadge } from "../components/SeverityBadge";

// With a live recorder the full catalogue it loaded is listed (from
// /v1/rules), with how often each rule concluded an incident in the loaded
// window. Without one (demo, bundle) only the rules that actually fired can
// be known, so this list can only ever be a count against a bare rule ID -
// except for a title, which the build-time rules.json snapshot can supply
// even with no live catalogue (ADR 0004), so a known rule at least reads as
// itself rather than an opaque id.
export function Rules({ incidents, rules }: { incidents: Incident[]; rules: RuleSummary[] }) {
  const { t, lang } = useLanguage();

  const fired = useMemo(() => {
    const counts = new Map<string, number>();
    for (const inc of incidents) {
      counts.set(inc.rule_id, (counts.get(inc.rule_id) ?? 0) + 1);
    }
    return counts;
  }, [incidents]);

  if (rules.length > 0) {
    const sorted = [...rules].sort(
      (a, b) => (fired.get(b.id) ?? 0) - (fired.get(a.id) ?? 0) || a.id.localeCompare(b.id),
    );
    return (
      <div>
        <h1 className="page-title">{t("rules_title")}</h1>
        <p className="page-subtitle">{t("rules_catalog_title")}</p>
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
