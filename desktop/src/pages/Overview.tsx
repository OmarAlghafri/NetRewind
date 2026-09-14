import { useMemo } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { NetRewindEvent } from "../types";

// Demo mode has no live registry.Snapshot (internal/registry) to read, since
// there is no running agent - so this deliberately does NOT claim a live
// "up/down" status. It reports what the recording actually contains, which
// is a different and honest claim: PRODUCT_RELEASE_PLAN_AR.md's own rule
// that a capability must never be implied just because the app is open.
export function Overview({ events }: { events: NetRewindEvent[] }) {
  const { t } = useLanguage();

  const sources = useMemo(() => {
    const set = new Set<string>();
    for (const e of events) set.add(e.source);
    return Array.from(set).sort();
  }, [events]);

  const gaps = useMemo(() => events.filter((e) => e.kind === "system.gap"), [events]);
  const collectorDowns = useMemo(() => events.filter((e) => e.kind === "system.collector_down"), [events]);

  return (
    <div>
      <h1 className="page-title">{t("overview_title")}</h1>
      <p className="page-subtitle">{t("overview_subtitle")}</p>

      <div className="card">
        <strong>{t("unknown_gaps_title")}</strong>
        {gaps.length === 0 && collectorDowns.length === 0 ? (
          <div style={{ color: "var(--text-muted)", marginTop: 6 }}>{t("no_gaps")}</div>
        ) : (
          <ul>
            {gaps.map((g) => (
              <li key={g.event_id}>
                <span className="ltr-field">system.gap</span> —{" "}
                <span className="ltr-field">{String(g.attrs?.gap_duration_ms ?? "?")}ms</span>
              </li>
            ))}
            {collectorDowns.map((c) => (
              <li key={c.event_id}>
                <span className="ltr-field">{String(c.attrs?.collector ?? "?")}</span>:{" "}
                {String(c.attrs?.reason ?? "")}
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="card">
        <strong>
          <span className="ltr-field">{sources.length}</span> {t("sources_in_recording")}
        </strong>
        {sources.map((s) => (
          <div className="capability-row" key={s}>
            <span className="ltr-field">{s}</span>
            <span className="status-dot status-up" title={t("seen_in_recording")} />
          </div>
        ))}
      </div>
    </div>
  );
}
