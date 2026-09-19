import { useEffect, useRef, useState } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import { Button } from "../components/Button";
import { TechnicalValue } from "../components/TechnicalValue";
import type { SourceKind, SourceSettings } from "../data/source";
import { agentDefaultEndpoint, agentGet, isTauri } from "../data/tauri";
import type { Health } from "../data/types";

/**
 * execution order §9 Phase 5: Settings "with a sticky save/discard bar and
 * an unsaved-changes guard". Two distinct kinds of "leaving": closing or
 * reloading the tab (native `beforeunload` - the browser's own dialog,
 * not reimplemented here) and in-app navigation via the hash router,
 * which `beforeunload` never fires for at all since the document itself
 * never unloads. The second needs its own guard: revert the hash back
 * (cancelling the navigation) unless the user confirms losing the draft -
 * `reverting` exists only so that programmatic revert does not itself
 * re-trigger the guard it is part of.
 */
function useUnsavedChangesGuard(dirty: boolean, confirmMessage: string) {
  const revertingRef = useRef(false);

  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

  useEffect(() => {
    if (!dirty) return;
    const guardedHash = window.location.hash;
    const onHashChange = () => {
      if (revertingRef.current) {
        revertingRef.current = false;
        return;
      }
      if (window.location.hash === guardedHash) return;
      if (!window.confirm(confirmMessage)) {
        revertingRef.current = true;
        window.location.hash = guardedHash;
      }
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, [dirty, confirmMessage]);
}

export function Settings({
  settings,
  onChange,
  onReopenWizard,
}: {
  settings: SourceSettings;
  onChange: (next: SourceSettings) => void;
  onReopenWizard: () => void;
}) {
  const { t, lang, setLang } = useLanguage();
  const inShell = isTauri();
  const [draft, setDraft] = useState<SourceSettings>(settings);
  const [defaultEndpoint, setDefaultEndpoint] = useState("");
  const [saved, setSaved] = useState(false);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => setDraft(settings), [settings]);
  useEffect(() => {
    if (inShell) agentDefaultEndpoint().then(setDefaultEndpoint).catch(() => setDefaultEndpoint(""));
  }, [inShell]);

  const dirty = JSON.stringify(draft) !== JSON.stringify(settings);
  useUnsavedChangesGuard(dirty, t("settings_unsaved_changes_confirm"));

  const save = () => {
    onChange(draft);
    setSaved(true);
    window.setTimeout(() => setSaved(false), 2000);
  };

  const discard = () => setDraft(settings);

  const testConnection = async () => {
    setTest(null);
    try {
      const h = await agentGet<Health>(draft.endpoint, "/v1/health");
      setTest({ ok: true, text: `${t("settings_connection_ok")}: ${h.observer_id} ${h.version}` });
    } catch (e) {
      setTest({ ok: false, text: `${t("settings_connection_failed")}: ${e instanceof Error ? e.message : String(e)}` });
    }
  };

  const sourceOption = (kind: SourceKind, label: string, enabled: boolean) => (
    <label className={`field-radio${enabled ? "" : " field-radio-disabled"}`} key={kind}>
      <input
        type="radio"
        name="source"
        value={kind}
        checked={draft.kind === kind}
        disabled={!enabled}
        onChange={() => setDraft({ ...draft, kind })}
      />
      {label}
    </label>
  );

  return (
    <div>
      <h1 className="page-title">{t("settings_title")}</h1>

      <div className="card">
        <strong>{t("settings_language")}</strong>
        <div className="lang-toggle" style={{ maxWidth: 240, marginTop: 10 }}>
          <button className={lang === "ar" ? "active" : ""} onClick={() => setLang("ar")}>
            العربية
          </button>
          <button className={lang === "en" ? "active" : ""} onClick={() => setLang("en")}>
            English
          </button>
        </div>
      </div>

      <div className="card">
        <strong>{t("settings_source_title")}</strong>
        <div style={{ marginTop: 8 }}>
          {sourceOption("demo", t("settings_source_demo"), true)}
          {sourceOption("live", t("settings_source_live"), inShell)}
          {sourceOption("bundle", t("settings_source_bundle"), inShell && draft.bundlePath !== "")}
        </div>
        {!inShell && <p className="wizard-step-note">{t("settings_shell_note")}</p>}

        {inShell && (
          <>
            <label className="field-label">
              {t("settings_endpoint_label")}
              <input
                className="field-input ltr-field"
                dir="ltr"
                value={draft.endpoint}
                placeholder={defaultEndpoint}
                onChange={(e) => setDraft({ ...draft, endpoint: e.target.value })}
              />
            </label>
            <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
              <Button variant="secondary" onClick={testConnection}>
                {t("settings_test_connection")}
              </Button>
              {test && (
                <span className={test.ok ? "inline-ok" : "inline-error"} role="status">
                  <TechnicalValue>{test.text}</TechnicalValue>
                </span>
              )}
            </div>
            <label className="field-label">
              {t("settings_refresh_label")}
              <input
                className="field-input ltr-field"
                dir="ltr"
                type="number"
                min={2}
                max={60}
                value={draft.refreshSeconds}
                onChange={(e) => setDraft({ ...draft, refreshSeconds: Number(e.target.value) })}
              />
            </label>
            <label className="field-label">
              {t("settings_public_key_label")}
              <input
                className="field-input ltr-field"
                dir="ltr"
                value={draft.publicKey}
                onChange={(e) => setDraft({ ...draft, publicKey: e.target.value })}
              />
            </label>
            <p className="wizard-step-note">{t("settings_public_key_note")}</p>
          </>
        )}

        {saved && (
          <div style={{ marginTop: 6 }}>
            <span className="inline-ok" role="status">
              {t("settings_saved")}
            </span>
          </div>
        )}
      </div>

      <div className="card">
        <strong>{t("settings_wizard_title")}</strong>
        <p style={{ color: "var(--text-muted)", fontSize: "0.88rem" }}>{t("settings_wizard_body")}</p>
        <Button variant="secondary" onClick={onReopenWizard} style={{ marginTop: 4 }}>
          {t("settings_wizard_button")}
        </Button>
      </div>

      {/* Sticky save/discard bar (execution order §9 Phase 5) - only ever
          shown once there is something to save or discard, so it never
          occupies space or asks a decision of a reader who changed
          nothing. Sticks to the bottom of .workspace-body (the app's one
          normal scroll region, ADR 0003), not the browser viewport, so it
          stays reachable inside the shell exactly like a primary action
          in WorkspaceHeader is required to (§4.1) - never scrolled away
          at the bottom of a long settings page. */}
      {dirty && (
        <div className="settings-save-bar" role="region" aria-label={t("settings_unsaved_indicator")}>
          <span className="settings-save-bar-label">{t("settings_unsaved_indicator")}</span>
          <span className="settings-save-bar-actions">
            <Button variant="secondary" onClick={discard}>
              {t("settings_discard")}
            </Button>
            <Button variant="primary" onClick={save}>
              {t("settings_save")}
            </Button>
          </span>
        </div>
      )}
    </div>
  );
}
