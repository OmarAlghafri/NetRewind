// Typed wrappers over the model-manager Tauri commands (desktop/src-tauri/
// src/lib.rs: ai_model_list, ai_model_download_start/status/cancel,
// ai_model_remove) - in the same style as ai.ts's own wrappers, reusing its
// toAiCommandError since these commands reject with the identical
// "<code>: <detail>" string shape.

import { invoke } from "./tauri";
import { toAiCommandError } from "./ai";

/** One entry of `netrewind ai model list -o json`'s output - the embedded
 *  catalogue (internal/aimodel.LoadEmbeddedManifest) plus whether it is
 *  already downloaded. */
export interface AiModelRow {
  profile: string;
  id: string;
  file_name: string;
  size_bytes: number;
  gate_passed: boolean;
  installed: boolean;
}

/** desktop-tauri's ai::cli::ModelDownloadStatus. */
export interface AiModelDownloadStatus {
  profile: string;
  downloaded: number;
  total: number;
  done: boolean;
  cancelled: boolean;
  error: string | null;
}

/** Every profile the embedded catalogue offers, and whether each is
 *  already downloaded - never gated by the AI_FEATURE_ENABLED panel
 *  visibility check, so this can be shown regardless of whether the
 *  assistant itself is turned on. */
export async function aiModelList(): Promise<AiModelRow[]> {
  try {
    const stdout = await invoke<string>("ai_model_list");
    return JSON.parse(stdout) as AiModelRow[];
  } catch (e) {
    throw toAiCommandError(e);
  }
}

/** Starts downloading one profile in the background; returns once the
 *  download has *started*, not once it has finished - poll
 *  aiModelDownloadStatus for progress. Rejects if another download is
 *  already in flight. */
export async function aiModelDownloadStart(profile: string): Promise<void> {
  try {
    await invoke<void>("ai_model_download_start", { profile });
  } catch (e) {
    throw toAiCommandError(e);
  }
}

/** The in-flight (or just-finished) download's status, or `null` if
 *  nothing has been started this session. */
export async function aiModelDownloadStatus(): Promise<AiModelDownloadStatus | null> {
  try {
    return await invoke<AiModelDownloadStatus | null>("ai_model_download_status");
  } catch (e) {
    throw toAiCommandError(e);
  }
}

/** Kills the in-flight download, if any - a no-op otherwise. The Go
 *  side's own `.partial` resume means a later download for the same
 *  profile picks up where this left off. */
export async function aiModelDownloadCancel(): Promise<void> {
  try {
    await invoke<void>("ai_model_download_cancel");
  } catch (e) {
    throw toAiCommandError(e);
  }
}

/** Deletes a downloaded profile's file and metadata. */
export async function aiModelRemove(profile: string): Promise<void> {
  try {
    await invoke<void>("ai_model_remove", { profile });
  } catch (e) {
    throw toAiCommandError(e);
  }
}
