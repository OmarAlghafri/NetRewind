import type { Incident, Relation } from "../types";
import { nsToDate } from "../types";
import { useLanguage } from "../i18n/LanguageContext";
import { SeverityBadge } from "./SeverityBadge";

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

export function IncidentCard({ incident }: { incident: Incident }) {
  const { t, lang } = useLanguage();
  return (
    <div className="card">
      <div className="incident-title">{incident.title}</div>
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
              <strong className="ltr-field">{link.kind}</strong>{"  "}
              <span className="ltr-field">{link.subject}</span>
            </div>
            <div style={{ fontSize: "0.88rem", color: "var(--text-muted)" }}>{link.why}</div>
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
        <span className="ltr-field">{incident.root_cause.kind}</span>
        {" "}
        (<span className="ltr-field">{incident.root_cause.entity}</span>,{" "}
        <span className="ltr-field">{incident.root_cause.confidence}%</span>)
      </div>

      {incident.advice && (
        <div className="advice-box">
          <strong>{t("advice")}:</strong> {incident.advice}
        </div>
      )}
    </div>
  );
}
