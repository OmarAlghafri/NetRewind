import type { CSSProperties } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { Incident, NetRewindEvent } from "../types";

// internal/bundle/bundle.go: an evidence bundle is a signed, read-only
// export (manifest + events + incidents + SHA256SUMS, optional signature)
// meant to be handed to someone else or opened by a second NetRewind copy.
// Nothing in the desktop app is wired to that package yet - no local store
// to export from, no import path to promote a bundle into. Faking an
// upload/export control that quietly does nothing on click is exactly the
// kind of overclaim this project's docs (PRD.md, PRODUCT_RELEASE_PLAN_AR.md
// §3) rule out, so both actions below are real <button disabled> elements:
// they cannot be clicked at all, and say so.
export function Evidence({ events, incidents }: { events: NetRewindEvent[]; incidents: Incident[] }) {
  const { t } = useLanguage();

  const disabledButtonStyle: CSSProperties = {
    padding: "8px 14px",
    border: "1px solid var(--border)",
    borderRadius: 6,
    background: "var(--bg-elevated)",
    color: "var(--text-muted)",
    cursor: "not-allowed",
    fontSize: "0.9rem",
  };

  return (
    <div>
      <h1 className="page-title">{t("evidence_title")}</h1>
      <p className="page-subtitle">{t("evidence_subtitle")}</p>

      <div className="card">
        <strong>{t("evidence_what_title")}</strong>
        <p style={{ color: "var(--text-muted)", fontSize: "0.9rem", marginTop: 8 }}>{t("evidence_what_body")}</p>
      </div>

      <div className="card">
        <strong>{t("evidence_export_title")}</strong>
        <div style={{ marginTop: 8 }}>
          <span className="ltr-field">{events.length}</span> {t("evidence_events_count_label")}
        </div>
        <div>
          <span className="ltr-field">{incidents.length}</span> {t("evidence_incidents_count_label")}
        </div>
        <div style={{ marginTop: 12 }}>
          <button disabled style={disabledButtonStyle}>
            {t("evidence_export_button")}
          </button>
        </div>
        <p style={{ color: "var(--text-muted)", fontSize: "0.85rem", marginTop: 8, marginBottom: 0 }}>
          {t("evidence_export_note")}
        </p>
      </div>

      <div className="card">
        <strong>{t("evidence_import_title")}</strong>
        <p style={{ color: "var(--text-muted)", fontSize: "0.9rem", marginTop: 8 }}>{t("evidence_import_body")}</p>
        <div style={{ marginTop: 12 }}>
          <button disabled style={disabledButtonStyle}>
            {t("evidence_import_button")}
          </button>
        </div>
        <p style={{ color: "var(--text-muted)", fontSize: "0.85rem", marginTop: 8, marginBottom: 0 }}>
          {t("evidence_import_note")}
        </p>
      </div>
    </div>
  );
}
