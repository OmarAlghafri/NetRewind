import { useMemo } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { Record } from "../data/useRecord";
import type { SourceSettings } from "../data/source";
import { CapabilityTable } from "../components/CapabilityTable";

function formatUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  return h > 0 ? `${h}h ${m}m` : m > 0 ? `${m}m ${s}s` : `${s}s`;
}

// Health is what a live recorder says about itself, and the capability
// matrix is its registry. With a bundle, the same matrix is what the
// producing recorder recorded into the manifest. In demo mode there is no
// registry at all, so the page reports only what the recording contains -
// never an implied "up".
export function Overview({ record, settings }: { record: Record; settings: SourceSettings }) {
  const { t } = useLanguage();
  const { events, health, capabilities } = record;

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

      {health && (
        <div className="card">
          <strong>{t("health_recorder")}</strong>
          <div className="capability-row">
            <span>{t("health_version")}</span>
            <span className="ltr-field">{health.version}</span>
          </div>
          <div className="capability-row">
            <span>{t("health_observer")}</span>
            <span className="ltr-field">{health.observer_id}</span>
          </div>
          <div className="capability-row">
            <span>{t("health_uptime")}</span>
            <span className="ltr-field">{formatUptime(health.uptime_seconds)}</span>
          </div>
          <div className="capability-row">
            <span>{t("health_store_events")}</span>
            <span className="ltr-field">{health.store.error ? health.store.error : health.store.events}</span>
          </div>
          <div className="capability-row" style={{ borderBottom: "none" }}>
            <span>{t("health_store_path")}</span>
            <span className="ltr-field" style={{ wordBreak: "break-all" }}>
              {health.store.path ?? "-"}
            </span>
          </div>
        </div>
      )}

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

      {capabilities.length > 0 ? (
        <div className="card">
          <strong>{t("capabilities_title")}</strong>
          <p className="page-subtitle" style={{ marginTop: 4 }}>
            {settings.kind === "bundle" ? t("capabilities_in_bundle") : t("capabilities_subtitle")}
          </p>
          <CapabilityTable capabilities={capabilities} />
        </div>
      ) : (
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
      )}
    </div>
  );
}
