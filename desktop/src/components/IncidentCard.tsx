import { Fragment } from "react";
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

// PRD U3: "التحقق من هل كان هذا هجوماً أم فشلاً عادياً... مع أدلة خام
// قابلة للفتح" (verifying attack vs. ordinary failure, with raw evidence
// that can be opened) - internal/incident.Link.Evidence (map[string]any,
// always at least {describe}, sometimes {matched_count, last_event_id}
// when a clause needed more than one match - internal/correlate/engine.go
// build()'s only two write sites) existed on the wire and the TS type but
// was never rendered anywhere before this. Kept as raw/technical content
// (TechnicalValue on every value, not narrative prose) rather than
// something to translate: this is forensic evidence, deliberately shown
// as what the engine actually recorded, not a written account of it -
// translating individual events' event.Describe() sentences is a
// separate, much larger undertaking (internal/event/describe.go's ~40
// per-kind templates) this disclosure does not attempt.
const EVIDENCE_LABELS: Record<string, "evidence_describe_label" | "evidence_matched_count_label" | "evidence_last_event_label"> = {
  describe: "evidence_describe_label",
  matched_count: "evidence_matched_count_label",
  last_event_id: "evidence_last_event_label",
};

function EvidenceDisclosure({ evidence }: { evidence?: Record<string, unknown> }) {
  const { t } = useLanguage();
  const entries = Object.entries(evidence ?? {});
  if (entries.length === 0) return null;
  return (
    <details className="evidence-disclosure">
      <summary>{t("evidence_raw_title")}</summary>
      <dl className="evidence-disclosure-list">
        {entries.map(([key, value]) => (
          <Fragment key={key}>
            <dt>{EVIDENCE_LABELS[key] ? t(EVIDENCE_LABELS[key]) : <TechnicalValue>{key}</TechnicalValue>}</dt>
            <dd>
              <TechnicalValue>{String(value)}</TechnicalValue>
            </dd>
          </Fragment>
        ))}
      </dl>
    </details>
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
            <EvidenceDisclosure evidence={link.evidence} />
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
