import { useEffect, useMemo, useRef } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { NetRewindEvent } from "../types";
import { nsToDate } from "../types";
import { SeverityBadge } from "../components/SeverityBadge";
import { TechnicalValue } from "../components/TechnicalValue";
import { formatTime } from "../i18n/format";
import type { InvestigationContext } from "../routing/useRoute";

/**
 * Same absolute from/to convention as Rules.tsx's own incidentsInRange -
 * unset means no filter (today's exact behaviour, so every existing
 * caller that never sets context.from/to keeps seeing every event).
 */
export function eventsInRange(events: NetRewindEvent[], context: InvestigationContext): NetRewindEvent[] {
  const fromMs = context.from ? Date.parse(context.from) : undefined;
  const toMs = context.to ? Date.parse(context.to) : undefined;
  if ((fromMs === undefined || Number.isNaN(fromMs)) && (toMs === undefined || Number.isNaN(toMs))) {
    return events;
  }
  return events.filter((e) => {
    const wallMs = e.ts_wall / 1e6;
    if (fromMs !== undefined && !Number.isNaN(fromMs) && wallMs < fromMs) return false;
    if (toMs !== undefined && !Number.isNaN(toMs) && wallMs > toMs) return false;
    return true;
  });
}

/**
 * `context` is how a clicked local-AI evidence handle (AiAssistantPanel)
 * lands here: `{from, to}` narrow the row list to that analysis's own
 * evidence window, `selection` (an event id) highlights and scrolls to
 * the specific cited event - the same InvestigationContext shape Rules.tsx
 * and Incidents.tsx already read, applied to this page for the first time.
 */
export function Timeline({ events, context = {} }: { events: NetRewindEvent[]; context?: InvestigationContext }) {
  const { t, lang } = useLanguage();
  const filtered = useMemo(() => eventsInRange(events, context), [events, context]);
  const sorted = useMemo(() => [...filtered].sort((a, b) => a.ts_wall - b.ts_wall), [filtered]);
  const selectedRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (context.selection) selectedRef.current?.scrollIntoView?.({ block: "center" });
  }, [context.selection]);

  return (
    <div>
      <h1 className="page-title">{t("timeline_title")}</h1>
      <p className="page-subtitle">{t("timeline_subtitle")}</p>
      <div className="card">
        {sorted.length === 0 ? (
          <div className="empty-state">{t("incidents_empty")}</div>
        ) : (
          sorted.map((e) => {
            const isSelected = e.event_id === context.selection;
            return (
              <div
                className={`timeline-row${isSelected ? " timeline-row-selected" : ""}`}
                key={e.event_id}
                ref={isSelected ? selectedRef : undefined}
              >
                <TechnicalValue className="timeline-time" dateTime={nsToDate(e.ts_wall).toISOString()}>
                  {formatTime(nsToDate(e.ts_wall), lang)}
                </TechnicalValue>
                <SeverityBadge severity={e.severity} />
                <span className="ltr-field" style={{ fontWeight: 600 }}>
                  {e.kind}
                </span>
                <span className="ltr-field" style={{ color: "var(--text-muted)" }}>
                  {e.subject?.label}
                </span>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
