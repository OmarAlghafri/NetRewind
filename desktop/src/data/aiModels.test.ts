import { afterEach, describe, expect, it } from "vitest";
import {
  aiModelDownloadCancel,
  aiModelDownloadStart,
  aiModelDownloadStatus,
  aiModelList,
  aiModelRemove,
} from "./aiModels";
import { AiCommandError } from "./ai";

type Invoke = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;

function installShell(invoke: Invoke) {
  (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = { invoke };
}
function removeShell() {
  delete (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}
afterEach(() => removeShell());

describe("aiModelList", () => {
  it("parses the CLI's JSON stdout", async () => {
    const rows = [{ profile: "small", id: "unsloth/Qwen3.5-0.8B-GGUF", file_name: "Qwen3.5-0.8B-Q4_K_M.gguf", size_bytes: 532517120, gate_passed: false, installed: false }];
    installShell(async (cmd) => {
      if (cmd !== "ai_model_list") throw new Error("unexpected command " + cmd);
      return JSON.stringify(rows);
    });
    const result = await aiModelList();
    expect(result).toEqual(rows);
  });

  it("wraps a rejection in AiCommandError with the code split out", async () => {
    installShell(async () => {
      throw "cli_failed: netrewind: some error";
    });
    await expect(aiModelList()).rejects.toBeInstanceOf(AiCommandError);
  });
});

describe("aiModelDownloadStart / status / cancel", () => {
  it("sends the profile to ai_model_download_start", async () => {
    let seenArgs: Record<string, unknown> | undefined;
    installShell(async (cmd, args) => {
      seenArgs = args;
      if (cmd === "ai_model_download_start") return undefined;
      throw new Error("unexpected command " + cmd);
    });
    await aiModelDownloadStart("small");
    expect(seenArgs).toEqual({ profile: "small" });
  });

  it("returns the parsed status object, or null", async () => {
    installShell(async (cmd) => {
      if (cmd !== "ai_model_download_status") throw new Error("unexpected command " + cmd);
      return { profile: "small", downloaded: 100, total: 1000, done: false, cancelled: false, error: null };
    });
    const status = await aiModelDownloadStatus();
    expect(status?.downloaded).toBe(100);
  });

  it("returns null when nothing has been started", async () => {
    installShell(async () => null);
    expect(await aiModelDownloadStatus()).toBeNull();
  });

  it("calls ai_model_download_cancel with no arguments", async () => {
    let called = false;
    installShell(async (cmd) => {
      called = cmd === "ai_model_download_cancel";
      return undefined;
    });
    await aiModelDownloadCancel();
    expect(called).toBe(true);
  });
});

describe("aiModelRemove", () => {
  it("sends the profile to ai_model_remove", async () => {
    let seenArgs: Record<string, unknown> | undefined;
    installShell(async (cmd, args) => {
      seenArgs = args;
      if (cmd === "ai_model_remove") return undefined;
      throw new Error("unexpected command " + cmd);
    });
    await aiModelRemove("balanced");
    expect(seenArgs).toEqual({ profile: "balanced" });
  });
});
