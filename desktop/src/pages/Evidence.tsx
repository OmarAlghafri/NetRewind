import { useState } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import { Button } from "../components/Button";
import { TechnicalValue } from "../components/TechnicalValue";
import type { Record } from "../data/useRecord";
import type { SourceSettings } from "../data/source";
import { agentExportBundle, isTauri, pickBundleToOpen, pickBundleToSave } from "../data/tauri";
import type { InvestigationContext } from "../routing/useRoute";
import { findRule, titleFor } from "../i18n/rulesCatalogue";

type Window = "1h" | "24h" | "7d";
const WINDOW_HOURS: { [w in Window]: number } = { "1h": 1, "24h": 24, "7d": 168 };

// PRD U5: "مشاركة دليل حادثة مع طرف آخر... حادثة محددة" (sharing a
// specific incident's evidence, not just a recent window) - a default
// padding on each side of the incident's own window, adjustable rather
// than fixed, since how much surrounding context is worth including
// depends on the incident.
const DEFAULT_PADDING_MINUTES = 5;

// Export reads the live recorder's /v1/bundle and saves it where the user
// chooses; open verifies a bundle file (checksums, and the signature when a
// key is configured) and switches the app to show it. Neither touches the
// local record. In a plain browser neither can work, and the buttons say so
// instead of doing nothing.
export function Evidence({
  record,
  settings,
  context,
  onOpenBundle,
  onCloseBundle,
}: {
  record: Record;
  settings: SourceSettings;
  context: InvestigationContext;
  onOpenBundle: (path: string) => void;
  onCloseBundle: () => void;
}) {
  const { t, lang } = useLanguage();
  const [window, setWindow] = useState<Window>("24h");
  const [paddingMinutes, setPaddingMinutes] = useState(DEFAULT_PADDING_MINUTES);
  const [includeSecrets, setIncludeSecrets] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [exportMessage, setExportMessage] = useState<{ ok: boolean; text: string } | null>(null);
  const [openMessage, setOpenMessage] = useState<string | null>(null);

  const inShell = isTauri();
  const live = settings.kind === "live" && record.status === "ready";

  // Arrived here via IncidentCard's "export this incident" (Incidents.tsx
  // sets from/to to the incident's own opened_at/closed_at, selection to
  // its id) rather than through the sidebar - reachable only that way,
  // since a bare "#/evidence" carries no context.
  const incidentScope =
    context.from && context.to ? { from: context.from, to: context.to, selection: context.selection } : null;
  const scopedIncident = incidentScope?.selection
    ? record.incidents.find((i) => i.incident_id === incidentScope.selection)
    : undefined;
  const scopedTitle = scopedIncident ? titleFor(findRule(scopedIncident.rule_id, record.rules), lang, scopedIncident.title) : undefined;

  const doExport = async () => {
    setExportMessage(null);
    const stamp = new Date().toISOString().replace(/[-:]/g, "").slice(0, 15) + "Z";
    const dest = await pickBundleToSave(`netrewind-${stamp}.tar.gz`);
    if (!dest) return;
    setExporting(true);
    try {
      let since: Date, until: Date;
      if (incidentScope) {
        const padMs = Math.max(0, paddingMinutes) * 60_000;
        since = new Date(new Date(incidentScope.from).getTime() - padMs);
        until = new Date(new Date(incidentScope.to).getTime() + padMs);
      } else {
        until = new Date();
        since = new Date(until.getTime() - WINDOW_HOURS[window] * 3600 * 1000);
      }
      const query =
        `since=${encodeURIComponent(since.toISOString())}` +
        `&until=${encodeURIComponent(until.toISOString())}` +
        (includeSecrets ? "&include_secrets=true" : "");
      const result = await agentExportBundle(settings.endpoint, query, dest);
      setExportMessage({ ok: true, text: `${t("evidence_export_done")}: ${result.path} (${result.bytes} B)` });
    } catch (e) {
      setExportMessage({ ok: false, text: `${t("evidence_export_failed")}: ${e instanceof Error ? e.message : String(e)}` });
    } finally {
      setExporting(false);
    }
  };

  const doOpen = async () => {
    setOpenMessage(null);
    try {
      const path = await pickBundleToOpen();
      if (path) onOpenBundle(path);
    } catch (e) {
      setOpenMessage(`${t("evidence_open_failed")}: ${e instanceof Error ? e.message : String(e)}`);
    }
  };

  const fmt = (iso: string) => {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(lang === "ar" ? "ar-EG" : "en-US", { hour12: false });
  };

  return (
    <div>
      <h1 className="page-title">{t("evidence_title")}</h1>
      <p className="page-subtitle">{t("evidence_subtitle")}</p>

      <div className="card">
        <strong>{t("evidence_what_title")}</strong>
        <p style={{ color: "var(--text-muted)", fontSize: "0.9rem", marginTop: 8 }}>{t("evidence_what_body")}</p>
      </div>

      {settings.kind === "bundle" && record.manifest && (
        <div className="card">
          <strong>{t("evidence_opened_title")}</strong>
          <div className="capability-row">
            <span>{t("evidence_manifest_observer")}</span>
            <TechnicalValue>{record.manifest.observer_id}</TechnicalValue>
          </div>
          <div className="capability-row">
            <span>{t("evidence_manifest_window")}</span>
            <TechnicalValue>
              {fmt(record.manifest.window_from)} → {fmt(record.manifest.window_to)}
            </TechnicalValue>
          </div>
          <div className="capability-row">
            <span>{t("evidence_manifest_created")}</span>
            <TechnicalValue>{fmt(record.manifest.created_at)}</TechnicalValue>
          </div>
          <div className="capability-row">
            <span>{t("evidence_manifest_version")}</span>
            <TechnicalValue>{record.manifest.app_version}</TechnicalValue>
          </div>
          <ul style={{ marginBottom: 0 }}>
            <li>{t("evidence_checksums_ok")}</li>
            <li>
              {record.bundleSigned
                ? t("evidence_signature_verified")
                : record.bundleHasSignature
                  ? t("evidence_signature_present")
                  : t("evidence_signature_none")}
            </li>
            {record.manifest.redacted && <li>{t("evidence_redacted")}</li>}
            {record.manifest.truncated && <li>{t("evidence_truncated")}</li>}
          </ul>
          <Button variant="secondary" style={{ marginTop: 10 }} onClick={onCloseBundle}>
            {t("evidence_close_bundle")}
          </Button>
        </div>
      )}

      <div className="card">
        <strong>{incidentScope ? t("evidence_incident_scope_title") : t("evidence_export_title")}</strong>

        {incidentScope ? (
          <>
            {scopedTitle && (
              <p style={{ marginTop: 8, marginBottom: 0 }}>
                {scopedTitle} <TechnicalValue>({incidentScope.selection})</TechnicalValue>
              </p>
            )}
            <div style={{ marginTop: 8 }}>
              <TechnicalValue>
                {new Date(incidentScope.from).toLocaleString(lang === "ar" ? "ar-EG" : "en-US", { hour12: false })} →{" "}
                {new Date(incidentScope.to).toLocaleString(lang === "ar" ? "ar-EG" : "en-US", { hour12: false })}
              </TechnicalValue>
            </div>
          </>
        ) : (
          <>
            <div style={{ marginTop: 8 }}>
              <TechnicalValue>{record.events.length}</TechnicalValue> {t("evidence_events_count_label")}
            </div>
            <div>
              <TechnicalValue>{record.incidents.length}</TechnicalValue> {t("evidence_incidents_count_label")}
            </div>
          </>
        )}

        {live ? (
          <div style={{ marginTop: 12 }}>
            {incidentScope ? (
              <label className="field-label">
                {t("evidence_padding_label")}
                <input
                  type="number"
                  className="field-input"
                  min={0}
                  max={180}
                  value={paddingMinutes}
                  onChange={(e) => setPaddingMinutes(Number(e.target.value))}
                />
              </label>
            ) : (
              <label className="field-label">
                {t("evidence_window_label")}
                <select className="field-input" value={window} onChange={(e) => setWindow(e.target.value as Window)}>
                  <option value="1h">{t("evidence_window_1h")}</option>
                  <option value="24h">{t("evidence_window_24h")}</option>
                  <option value="7d">{t("evidence_window_7d")}</option>
                </select>
              </label>
            )}
            <label className="field-check">
              <input type="checkbox" checked={includeSecrets} onChange={(e) => setIncludeSecrets(e.target.checked)} />
              {t("evidence_include_secrets")}
            </label>
            <div style={{ marginTop: 10 }}>
              <Button variant="primary" onClick={doExport} disabled={exporting}>
                {exporting
                  ? t("evidence_exporting")
                  : incidentScope
                    ? t("incident_export_button")
                    : t("evidence_export_button")}
              </Button>
            </div>
            {exportMessage && (
              <p className={exportMessage.ok ? "inline-ok" : "inline-error"} role="status">
                <TechnicalValue>{exportMessage.text}</TechnicalValue>
              </p>
            )}
          </div>
        ) : (
          <p style={{ color: "var(--text-muted)", fontSize: "0.9rem", marginTop: 12 }}>{t("evidence_live_only")}</p>
        )}
        <p style={{ color: "var(--text-muted)", fontSize: "0.85rem", marginTop: 8, marginBottom: 0 }}>
          {t("evidence_export_note")}
        </p>
      </div>

      <div className="card">
        <strong>{t("evidence_import_title")}</strong>
        <p style={{ color: "var(--text-muted)", fontSize: "0.9rem", marginTop: 8 }}>{t("evidence_import_body")}</p>
        <div style={{ marginTop: 12 }}>
          {inShell ? (
            <Button variant="primary" onClick={doOpen}>
              {t("evidence_import_button")}
            </Button>
          ) : (
            <p style={{ color: "var(--text-muted)", fontSize: "0.9rem" }}>{t("settings_shell_note")}</p>
          )}
        </div>
        {openMessage && (
          <p className="inline-error" role="alert">
            <TechnicalValue>{openMessage}</TechnicalValue>
          </p>
        )}
        <p style={{ color: "var(--text-muted)", fontSize: "0.85rem", marginTop: 8, marginBottom: 0 }}>
          {t("evidence_import_note")}
        </p>
      </div>
    </div>
  );
}
