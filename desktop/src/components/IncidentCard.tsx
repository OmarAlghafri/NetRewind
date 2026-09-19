import type { Incident, Relation } from "../types";
import { nsToDate } from "../types";
import { useLanguage } from "../i18n/LanguageContext";
import type { Lang } from "../i18n/translations";
import { labelForKind } from "../i18n/kindCatalogue";
import { adviceFor, findRule, titleFor, whyFor } from "../i18n/rulesCatalogue";
import type { RuleSummary } from "../data/types";
import { SeverityBadge } from "./SeverityBadge";
import { TechnicalValue } from "./TechnicalValue";

// PRODUCT_RELEASE_PLAN_AR.md §3: "causes خط متصل بلون تحذير، correlates خط
// متقطع محايد، precedes منقط. لا يعتمد التمييز على اللون وحده." The line
// *style* (solid/dashed/dotted) carries the distinction; color is a second,
// non-load-bearing cue for people who can see it.
function relationLine(relation: Relation) {
  if (relation === "causes") return "relation-causes";
  if (relation === "correlates") return "relation-correlates";
  if (relation === "precedes") return "relation-precedes";
  return "relation-correlates";
}

function relationLabelKey(relation: Relation) {
  if (relation === "causes") return "relation_causes" as const;
  if (relation === "precedes") return "relation_precedes" as const;
  return "relation_correlates" as const;
}

function formatTime(ns: number, lang: string) {
  return nsToDate(ns).toLocaleTimeString(lang === "ar" ? "ar-EG" : "en-US", { hour12: false });
}

// A kind this build's catalogue does not know (an older bundle from a newer
// recorder release) shows only the raw code - showing it twice, once as a
// "name" that is just the code again, would be noise, not a fallback.
function KindName({ kind, lang }: { kind: string; lang: Lang }) {
  const { name, known } = labelForKind(kind, lang);
  if (!known) return <TechnicalValue>{kind}</TechnicalValue>;
  return (
    <>
      <strong>{name}</strong> <TechnicalValue>{kind}</TechnicalValue>
    </>
  );
}

export function IncidentCard({ incident, rules = [] }: { incident: Incident; rules?: RuleSummary[] }) {
  const { t, lang } = useLanguage();
  const rule = findRule(incident.rule_id, rules);
  const title = titleFor(rule, lang, incident.title);
  const advice = incident.advice ? adviceFor(rule, lang, incident.advice) : undefined;

  return (
    <div className="card">
      <div className="incident-title">{title}</div>
      <div className="incident-meta">
        <span className="ltr-field">{formatTime(incident.opened_at, lang)}</span>
        {"  ·  "}
        <SeverityBadge severity={incident.severity} />
        {"  ·  "}
        {t("confidence")}: <span className="ltr-field">{incident.confidence}%</span>
        {"  ·  "}
        <span className="ltr-field">{incident.rule_id}</span>
      </div>

      <div className="chain">
        {incident.chain.map((link, i) => (
          <div className="chain-link" key={link.event_id + i}>
            <div>
              <span className="ltr-field">{formatTime(link.at, lang)}</span>{"  "}
              <KindName kind={link.kind} lang={lang} />{"  "}
              <span className="ltr-field">{link.subject}</span>
            </div>
            <div style={{ fontSize: "0.88rem", color: "var(--text-muted)" }}>
              {whyFor(rule, link.clause, lang, link.why)}
            </div>
            {i < incident.chain.length - 1 && (
              <div>
                <hr className={`relation-line ${relationLine(incident.chain[i + 1]?.relation)}`} />
                <span className="relation-label">{t(relationLabelKey(incident.chain[i + 1]?.relation))}</span>
              </div>
            )}
          </div>
        ))}
      </div>

      <div className="root-cause-box">
        <strong>{t("root_cause")}:</strong>{" "}
        <KindName kind={incident.root_cause.kind} lang={lang} />
        {" "}
        (<span className="ltr-field">{incident.root_cause.entity}</span>,{" "}
        <span className="ltr-field">{incident.root_cause.confidence}%</span>)
      </div>

      {advice && (
        <div className="advice-box">
          <strong>{t("advice")}:</strong> {advice}
        </div>
      )}
    </div>
  );
}
