import { useState } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { Record } from "../data/useRecord";
import type { SourceSettings } from "../data/source";
import { agentExportBundle, isTauri, pickBundleToOpen, pickBundleToSave } from "../data/tauri";

type Window = "1h" | "24h" | "7d";
const WINDOW_HOURS: { [w in Window]: number } = { "1h": 1, "24h": 24, "7d": 168 };

// Export reads the live recorder's /v1/bundle and saves it where the user
// chooses; open verifies a bundle file (checksums, and the signature when a
// key is configured) and switches the app to show it. Neither touches the
// local record. In a plain browser neither can work, and the buttons say so
// instead of doing nothing.
export function Evidence({
  record,
  settings,
  onOpenBundle,
  onCloseBundle,
}: {
  record: Record;
  settings: SourceSettings;
  onOpenBundle: (path: string) => void;
  onCloseBundle: () => void;
}) {
  const { t, lang } = useLanguage();
  const [window, setWindow] = useState<Window>("24h");
  const [includeSecrets, setIncludeSecrets] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [exportMessage, setExportMessage] = useState<{ ok: boolean; text: string } | null>(null);
  const [openMessage, setOpenMessage] = useState<string | null>(null);

  const inShell = isTauri();
  const live = settings.kind === "live" && record.status === "ready";

  const doExport = async () => {
    setExportMessage(null);
    const stamp = new Date().toISOString().replace(/[-:]/g, "").slice(0, 15) + "Z";
    const dest = await pickBundleToSave(`netrewind-${stamp}.tar.gz`);
    if (!dest) return;
    setExporting(true);
    try {
      const until = new Date();
      const since = new Date(until.getTime() - WINDOW_HOURS[window] * 3600 * 1000);
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
            <span className="ltr-field">{record.manifest.observer_id}</span>
          </div>
          <div className="capability-row">
            <span>{t("evidence_manifest_window")}</span>
            <span className="ltr-field">
              {fmt(record.manifest.window_from)} → {fmt(record.manifest.window_to)}
            </span>
          </div>
          <div className="capability-row">
            <span>{t("evidence_manifest_created")}</span>
            <span className="ltr-field">{fmt(record.manifest.created_at)}</span>
          </div>
          <div className="capability-row">
            <span>{t("evidence_manifest_version")}</span>
            <span className="ltr-field">{record.manifest.app_version}</span>
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
          <button className="wizard-btn" style={{ marginTop: 10 }} onClick={onCloseBundle}>
            {t("evidence_close_bundle")}
          </button>
        </div>
      )}

      <div className="card">
        <strong>{t("evidence_export_title")}</strong>
        <div style={{ marginTop: 8 }}>
          <span className="ltr-field">{record.events.length}</span> {t("evidence_events_count_label")}
        </div>
        <div>
          <span className="ltr-field">{record.incidents.length}</span> {t("evidence_incidents_count_label")}
        </div>
        {live ? (
          <div style={{ marginTop: 12 }}>
            <label className="field-label">
              {t("evidence_window_label")}
              <select className="field-input" value={window} onChange={(e) => setWindow(e.target.value as Window)}>
                <option value="1h">{t("evidence_window_1h")}</option>
                <option value="24h">{t("evidence_window_24h")}</option>
                <option value="7d">{t("evidence_window_7d")}</option>
              </select>
            </label>
            <label className="field-check">
              <input type="checkbox" checked={includeSecrets} onChange={(e) => setIncludeSecrets(e.target.checked)} />
              {t("evidence_include_secrets")}
            </label>
            <div style={{ marginTop: 10 }}>
              <button className="wizard-btn wizard-btn-primary" onClick={doExport} disabled={exporting}>
                {exporting ? t("evidence_exporting") : t("evidence_export_button")}
              </button>
            </div>
            {exportMessage && (
              <p className={exportMessage.ok ? "inline-ok" : "inline-error"} role="status">
                <span className="ltr-field">{exportMessage.text}</span>
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
            <button className="wizard-btn wizard-btn-primary" onClick={doOpen}>
              {t("evidence_import_button")}
            </button>
          ) : (
            <p style={{ color: "var(--text-muted)", fontSize: "0.9rem" }}>{t("settings_shell_note")}</p>
          )}
        </div>
        {openMessage && (
          <p className="inline-error" role="alert">
            <span className="ltr-field">{openMessage}</span>
          </p>
        )}
        <p style={{ color: "var(--text-muted)", fontSize: "0.85rem", marginTop: 8, marginBottom: 0 }}>
          {t("evidence_import_note")}
        </p>
      </div>
    </div>
  );
}
