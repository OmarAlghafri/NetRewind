import { useMemo } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { Incident } from "../types";

// The correlation engine (internal/correlate) loads its full rule catalog
// from rules/*.yaml, but that catalog lives on whatever machine runs the
// recorder - there is no live connection here to read it from (same
// constraint as Overview.tsx's capability list). What this page CAN say
// honestly is which rule_id values actually fired in the incidents already
// loaded, and how often - real information, just narrower than "all rules".
export function Rules({ incidents }: { incidents: Incident[] }) {
  const { t } = useLanguage();

  const fired = useMemo(() => {
    const counts = new Map<string, number>();
    for (const inc of incidents) {
      counts.set(inc.rule_id, (counts.get(inc.rule_id) ?? 0) + 1);
    }
    return Array.from(counts.entries()).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  }, [incidents]);

  return (
    <div>
      <h1 className="page-title">{t("rules_title")}</h1>
      <p className="page-subtitle">{t("rules_subtitle")}</p>

      <div className="advice-box">{t("rules_note")}</div>

      <div className="card" style={{ marginTop: 14 }}>
        {fired.length === 0 ? (
          <div className="empty-state">{t("rules_empty")}</div>
        ) : (
          fired.map(([ruleId, count]) => (
            <div className="capability-row" key={ruleId}>
              <span className="ltr-field">{ruleId}</span>
              <span>
                <span className="ltr-field">{count}</span> {t("rules_fired_count")}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
