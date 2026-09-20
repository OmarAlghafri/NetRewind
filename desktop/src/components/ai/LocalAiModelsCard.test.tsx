import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { LanguageProvider } from "../../i18n/LanguageContext";
import { LocalAiModelsCard } from "./LocalAiModelsCard";

type Invoke = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;

function installShell(invoke: Invoke) {
  (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = { invoke };
}
function removeShell() {
  delete (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}
afterEach(() => removeShell());

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(<LanguageProvider>{ui}</LanguageProvider>);
}

const THREE_ROWS = [
  { profile: "small", id: "unsloth/Qwen3.5-0.8B-GGUF", file_name: "Qwen3.5-0.8B-Q4_K_M.gguf", size_bytes: 532517120, gate_passed: false, installed: false },
  { profile: "balanced", id: "unsloth/Qwen3.5-2B-GGUF", file_name: "Qwen3.5-2B-Q4_K_M.gguf", size_bytes: 1280835840, gate_passed: false, installed: false },
  { profile: "full", id: "unsloth/Qwen3.5-4B-GGUF", file_name: "Qwen3.5-4B-Q4_K_M.gguf", size_bytes: 2740937888, gate_passed: false, installed: false },
];

describe("LocalAiModelsCard outside the shell", () => {
  it("shows the shell-required note rather than a broken list", () => {
    english(<LocalAiModelsCard selectedFileName="" onModelReady={() => {}} />);
    expect(screen.getByText("The live recorder and bundle sources are available in the desktop application only.")).toBeInTheDocument();
  });
});

describe("LocalAiModelsCard: listing", () => {
  it("lists every profile with its size and a download button", async () => {
    installShell(async (cmd) => {
      if (cmd === "ai_model_list") return JSON.stringify(THREE_ROWS);
      throw new Error("unexpected command " + cmd);
    });
    english(<LocalAiModelsCard selectedFileName="" onModelReady={() => {}} />);
    expect(await screen.findByText("unsloth/Qwen3.5-0.8B-GGUF")).toBeInTheDocument();
    expect(screen.getByText("unsloth/Qwen3.5-2B-GGUF")).toBeInTheDocument();
    expect(screen.getByText("unsloth/Qwen3.5-4B-GGUF")).toBeInTheDocument();
    expect(screen.getAllByText("Download")).toHaveLength(3);
  });

  it("marks the currently-selected model's row", async () => {
    installShell(async (cmd) => {
      if (cmd === "ai_model_list") return JSON.stringify(THREE_ROWS);
      throw new Error("unexpected command " + cmd);
    });
    english(<LocalAiModelsCard selectedFileName="Qwen3.5-2B-Q4_K_M.gguf" onModelReady={() => {}} />);
    await screen.findByText("unsloth/Qwen3.5-2B-GGUF");
    expect(screen.getByText("Currently selected")).toBeInTheDocument();
  });

  it("shows Remove instead of Download for an installed profile", async () => {
    installShell(async (cmd) => {
      if (cmd === "ai_model_list") return JSON.stringify([{ ...THREE_ROWS[0], installed: true }, THREE_ROWS[1], THREE_ROWS[2]]);
      throw new Error("unexpected command " + cmd);
    });
    english(<LocalAiModelsCard selectedFileName="" onModelReady={() => {}} />);
    await screen.findByText("unsloth/Qwen3.5-0.8B-GGUF");
    expect(screen.getAllByText("Download")).toHaveLength(2);
    expect(screen.getByText("Remove")).toBeInTheDocument();
  });

  it("shows a load-failure message rather than crashing", async () => {
    installShell(async () => {
      throw "cli_failed: boom";
    });
    english(<LocalAiModelsCard selectedFileName="" onModelReady={() => {}} />);
    expect(await screen.findByText("Could not read the model list")).toBeInTheDocument();
  });
});

describe("LocalAiModelsCard: download flow", () => {
  it("starts a download immediately on click, shows progress, and calls onModelReady on completion", async () => {
    let statusCalls = 0;
    installShell(async (cmd) => {
      if (cmd === "ai_model_list") return JSON.stringify(THREE_ROWS);
      if (cmd === "ai_model_download_start") return undefined;
      if (cmd === "ai_model_download_status") {
        statusCalls++;
        if (statusCalls === 1) return { profile: "small", downloaded: 100, total: 532517120, done: false, cancelled: false, error: null };
        return { profile: "small", downloaded: 532517120, total: 532517120, done: true, cancelled: false, error: null };
      }
      throw new Error("unexpected command " + cmd);
    });
    const onModelReady = vi.fn();
    english(<LocalAiModelsCard selectedFileName="" onModelReady={onModelReady} />);
    await screen.findByText("unsloth/Qwen3.5-0.8B-GGUF");

    fireEvent.click(screen.getAllByText("Download")[0]);
    expect(await screen.findByText("Cancel download")).toBeInTheDocument();
    await screen.findByText(/Downloading: /);

    await waitFor(() => expect(onModelReady).toHaveBeenCalledWith("Qwen3.5-0.8B-Q4_K_M.gguf", "small"));
  });

  it("disables downloading a second profile while one is already in flight", async () => {
    installShell(async (cmd) => {
      if (cmd === "ai_model_list") return JSON.stringify(THREE_ROWS);
      if (cmd === "ai_model_download_start") return undefined;
      if (cmd === "ai_model_download_status") return { profile: "small", downloaded: 0, total: 0, done: false, cancelled: false, error: null };
      throw new Error("unexpected command " + cmd);
    });
    english(<LocalAiModelsCard selectedFileName="" onModelReady={() => {}} />);
    await screen.findByText("unsloth/Qwen3.5-0.8B-GGUF");

    fireEvent.click(screen.getAllByText("Download")[0]);
    await screen.findByText("Cancel download");
    const remainingDownloadButtons = screen.getAllByText("Download");
    expect(remainingDownloadButtons).toHaveLength(2);
    for (const button of remainingDownloadButtons) {
      expect(button.closest("button")).toBeDisabled();
    }
  });

  it("cancels the in-flight download and refreshes the list", async () => {
    let cancelled = false;
    installShell(async (cmd) => {
      if (cmd === "ai_model_list") return JSON.stringify(THREE_ROWS);
      if (cmd === "ai_model_download_start") return undefined;
      if (cmd === "ai_model_download_cancel") {
        cancelled = true;
        return undefined;
      }
      if (cmd === "ai_model_download_status") {
        return cancelled
          ? { profile: "small", downloaded: 10, total: 100, done: false, cancelled: true, error: null }
          : { profile: "small", downloaded: 10, total: 100, done: false, cancelled: false, error: null };
      }
      throw new Error("unexpected command " + cmd);
    });
    english(<LocalAiModelsCard selectedFileName="" onModelReady={() => {}} />);
    await screen.findByText("unsloth/Qwen3.5-0.8B-GGUF");

    fireEvent.click(screen.getAllByText("Download")[0]);
    fireEvent.click(await screen.findByText("Cancel download"));

    await waitFor(() => expect(screen.getAllByText("Download")).toHaveLength(3));
  });

  it("shows an error and does not call onModelReady when the download fails", async () => {
    installShell(async (cmd) => {
      if (cmd === "ai_model_list") return JSON.stringify(THREE_ROWS);
      if (cmd === "ai_model_download_start") return undefined;
      if (cmd === "ai_model_download_status") return { profile: "small", downloaded: 0, total: 0, done: false, cancelled: false, error: "disk full" };
      throw new Error("unexpected command " + cmd);
    });
    const onModelReady = vi.fn();
    english(<LocalAiModelsCard selectedFileName="" onModelReady={onModelReady} />);
    await screen.findByText("unsloth/Qwen3.5-0.8B-GGUF");

    fireEvent.click(screen.getAllByText("Download")[0]);
    expect(await screen.findByText("disk full")).toBeInTheDocument();
    expect(onModelReady).not.toHaveBeenCalled();
  });
});

describe("LocalAiModelsCard: remove", () => {
  it("removes a downloaded profile and refreshes the list", async () => {
    let removed = false;
    installShell(async (cmd) => {
      if (cmd === "ai_model_list") {
        return JSON.stringify(removed ? THREE_ROWS : [{ ...THREE_ROWS[0], installed: true }, THREE_ROWS[1], THREE_ROWS[2]]);
      }
      if (cmd === "ai_model_remove") {
        removed = true;
        return undefined;
      }
      throw new Error("unexpected command " + cmd);
    });
    english(<LocalAiModelsCard selectedFileName="" onModelReady={() => {}} />);
    await screen.findByText("Remove");

    fireEvent.click(screen.getByText("Remove"));
    await waitFor(() => expect(screen.getAllByText("Download")).toHaveLength(3));
  });
});
