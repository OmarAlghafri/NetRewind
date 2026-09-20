import { useEffect, useRef, useState } from "react";
import { useLanguage } from "../../i18n/LanguageContext";
import { formatBytes } from "../../i18n/format";
import { isTauri } from "../../data/tauri";
import {
  aiModelDownloadCancel,
  aiModelDownloadStart,
  aiModelDownloadStatus,
  aiModelList,
  aiModelRemove,
  type AiModelDownloadStatus,
  type AiModelRow,
} from "../../data/aiModels";
import type { AiProfile } from "../../data/aiSettings";
import { Button } from "../Button";
import { TechnicalValue } from "../TechnicalValue";

const POLL_MS = 500;

const PROFILE_LABEL_KEY: Record<string, "settings_ai_profile_small" | "settings_ai_profile_balanced" | "settings_ai_profile_full"> = {
  small: "settings_ai_profile_small",
  balanced: "settings_ai_profile_balanced",
  full: "settings_ai_profile_full",
};

type TFn = ReturnType<typeof useLanguage>["t"];

/**
 * Always visible, regardless of AI_FEATURE_ENABLED: acquiring a model file
 * onto disk runs no model and produces no analysis, unlike the assistant
 * panel/enable-toggle this sits beside in Settings, which stay gated by
 * that compile-time flag. "The user picks a profile, presses download, it
 * starts immediately" - no separate consent step, per an explicit product
 * decision (size/license are still shown plainly on the row itself, just
 * not behind a blocking dialog).
 *
 * `selectedFileName`/`onModelReady` touch Settings.tsx's own *draft* only,
 * never `aiSettings`/`onChangeAiSettings` directly: Settings.tsx resets its
 * whole draft object whenever the committed aiSettings prop changes
 * (its own effect, for the ordinary "Save" path), so calling
 * onChangeAiSettings straight from here the moment a download finishes
 * could silently wipe an unrelated in-progress edit elsewhere on the page
 * (e.g. a half-typed endpoint). Going through the draft means a finished
 * download shows up as a pending change with the usual save bar, exactly
 * like every other field on this page - not a surprise auto-save.
 */
export function LocalAiModelsCard({
  selectedFileName,
  onModelReady,
}: {
  selectedFileName: string;
  onModelReady: (fileName: string, profile: AiProfile) => void;
}) {
  const { t, lang } = useLanguage();
  const [rows, setRows] = useState<AiModelRow[] | "loading" | "error">("loading");
  const [activeProfile, setActiveProfile] = useState<string | null>(null);
  const [progress, setProgress] = useState<AiModelDownloadStatus | null>(null);
  const [rowError, setRowError] = useState<{ profile: string; message: string } | null>(null);

  // Read fresh inside the polling effect without making it a dependency
  // (and therefore restarting the poll) every time Settings.tsx re-renders
  // and hands down a new onModelReady reference - the exact stale-closure
  // hazard useAiAssistant.ts's own comment documents, solved here with a
  // ref instead since (unlike there) the *effect* must not re-run just
  // because this callback's identity changes.
  const onModelReadyRef = useRef(onModelReady);
  onModelReadyRef.current = onModelReady;

  const refresh = () => {
    if (!isTauri()) {
      setRows("error");
      return;
    }
    aiModelList()
      .then(setRows)
      .catch(() => setRows("error"));
  };

  useEffect(refresh, []);

  useEffect(() => {
    if (!activeProfile) return;
    const profile = activeProfile;
    let cancelled = false;
    let timer: number | undefined;

    const finish = async (status: AiModelDownloadStatus | null) => {
      if (status?.done) {
        try {
          const freshRows = await aiModelList();
          const row = freshRows.find((r) => r.profile === profile);
          if (row) {
            onModelReadyRef.current(row.file_name, profile as AiProfile);
          }
        } catch {
          // The download itself succeeded; failing to also auto-select it
          // just leaves modelFileName as it was - not worth surfacing as
          // an error on top of a successful download.
        }
      }
      if (status?.error) setRowError({ profile, message: status.error });
      setActiveProfile(null);
      refresh();
    };

    const poll = () => {
      aiModelDownloadStatus()
        .then((status) => {
          if (cancelled) return;
          setProgress(status);
          if (!status || status.done || status.cancelled || status.error) {
            void finish(status);
            return;
          }
          timer = window.setTimeout(poll, POLL_MS);
        })
        .catch(() => {
          if (!cancelled) setActiveProfile(null);
        });
    };
    poll();

    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [activeProfile]);

  const download = async (profile: string) => {
    setRowError(null);
    try {
      await aiModelDownloadStart(profile);
      setProgress(null);
      setActiveProfile(profile);
    } catch (e) {
      setRowError({ profile, message: e instanceof Error ? e.message : String(e) });
    }
  };

  const cancel = () => void aiModelDownloadCancel();

  const remove = async (profile: string) => {
    setRowError(null);
    try {
      await aiModelRemove(profile);
      refresh();
    } catch (e) {
      setRowError({ profile, message: e instanceof Error ? e.message : String(e) });
    }
  };

  return (
    <div className="card">
      <strong>{t("settings_ai_models_title")}</strong>
      <p style={{ color: "var(--text-muted)", fontSize: "0.88rem" }}>{t("settings_ai_models_body")}</p>
      {!isTauri() && <p className="wizard-step-note">{t("settings_shell_note")}</p>}
      {isTauri() && rows === "loading" && <div className="empty-state">{t("status_loading")}</div>}
      {isTauri() && rows === "error" && <div className="inline-error">{t("settings_ai_models_load_failed")}</div>}
      {isTauri() &&
        Array.isArray(rows) &&
        rows.map((row) => (
          <ModelRow
            key={row.profile}
            row={row}
            active={activeProfile === row.profile}
            progress={activeProfile === row.profile ? progress : null}
            error={rowError?.profile === row.profile ? rowError.message : null}
            downloadingAnother={activeProfile !== null && activeProfile !== row.profile}
            isSelected={selectedFileName === row.file_name}
            onDownload={() => void download(row.profile)}
            onCancel={cancel}
            onRemove={() => void remove(row.profile)}
            lang={lang}
            t={t}
          />
        ))}
    </div>
  );
}

function ModelRow({
  row,
  active,
  progress,
  error,
  downloadingAnother,
  isSelected,
  onDownload,
  onCancel,
  onRemove,
  lang,
  t,
}: {
  row: AiModelRow;
  active: boolean;
  progress: AiModelDownloadStatus | null;
  error: string | null;
  downloadingAnother: boolean;
  isSelected: boolean;
  onDownload: () => void;
  onCancel: () => void;
  onRemove: () => void;
  lang: "ar" | "en";
  t: TFn;
}) {
  const profileLabel = PROFILE_LABEL_KEY[row.profile];
  return (
    <div className="capability-row" style={{ flexDirection: "column", alignItems: "stretch", gap: 6 }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <span>
          {profileLabel ? t(profileLabel) : row.profile} — <TechnicalValue>{formatBytes(row.size_bytes, lang)}</TechnicalValue>
          {isSelected && <span className="inline-ok" style={{ marginInlineStart: 8 }}>{t("settings_ai_models_selected")}</span>}
        </span>
        <span style={{ display: "flex", gap: 8 }}>
          {active ? (
            <Button variant="ghost" onClick={onCancel}>
              {t("settings_ai_models_cancel_button")}
            </Button>
          ) : row.installed ? (
            <Button variant="secondary" onClick={onRemove}>
              {t("settings_ai_models_remove_button")}
            </Button>
          ) : (
            <Button variant="primary" onClick={onDownload} disabled={downloadingAnother}>
              {t("settings_ai_models_download_button")}
            </Button>
          )}
        </span>
      </div>
      <p className="wizard-step-note" style={{ margin: 0 }}>
        <TechnicalValue>{row.id}</TechnicalValue>
      </p>
      {active && progress && (
        <p className="ai-panel-hint" style={{ margin: 0 }} role="status">
          {progress.total > 0
            ? t("settings_ai_models_progress").replace("{done}", formatBytes(progress.downloaded, lang)).replace("{total}", formatBytes(progress.total, lang))
            : t("settings_ai_models_starting")}
        </p>
      )}
      {error && (
        <p className="inline-error" style={{ margin: 0 }} role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
