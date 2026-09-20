import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import * as aiModule from "./ai";
import { AiSessionProvider, useAiSession } from "./aiSession";

type Invoke = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;

function installShell(invoke: Invoke) {
  (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = { invoke };
}

function removeShell() {
  delete (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}

afterEach(() => {
  removeShell();
  vi.useRealTimers();
});

function wrapper({ children }: { children: ReactNode }) {
  return <AiSessionProvider>{children}</AiSessionProvider>;
}

describe("AiSessionProvider outside the shell", () => {
  it("reports running:false without ever calling aiStatus at all", async () => {
    // A spy on the real aiStatus export, not just an absent
    // window.__TAURI_INTERNALS__ - aiStatus()/invoke() already fail safe
    // on their own when the shell is missing (tauri.ts's own guard), so
    // asserting only the resulting {running:false} would pass even if
    // aiSession's own isTauri() early-return were deleted and it always
    // attempted (and always failed) the call instead. This spy is what
    // actually distinguishes "skipped" from "attempted and caught".
    const spy = vi.spyOn(aiModule, "aiStatus");
    removeShell();

    const { result } = renderHook(() => useAiSession(), { wrapper });
    await waitFor(() => expect(result.current.runtimeStatus).toEqual({ running: false, port: null }));
    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });
});

describe("AiSessionProvider inside the shell", () => {
  it("polls ai_status and exposes the result", async () => {
    installShell(async (cmd) => {
      if (cmd !== "ai_status") throw new Error("unexpected command " + cmd);
      return { running: true, port: 41233 };
    });
    const { result } = renderHook(() => useAiSession(), { wrapper });
    await waitFor(() => expect(result.current.runtimeStatus).toEqual({ running: true, port: 41233 }));
  });

  it("falls back to running:false when ai_status rejects", async () => {
    installShell(async () => {
      throw "runtime_missing: no runtime staged";
    });
    const { result } = renderHook(() => useAiSession(), { wrapper });
    await waitFor(() => expect(result.current.runtimeStatus).toEqual({ running: false, port: null }));
  });

  it("refreshStatus re-polls immediately rather than waiting for the next tick", async () => {
    let running = false;
    installShell(async (cmd) => {
      if (cmd !== "ai_status") throw new Error("unexpected command " + cmd);
      return { running, port: running ? 1 : null };
    });
    const { result } = renderHook(() => useAiSession(), { wrapper });
    await waitFor(() => expect(result.current.runtimeStatus?.running).toBe(false));

    running = true;
    await act(async () => {
      result.current.refreshStatus();
      await Promise.resolve();
    });
    await waitFor(() => expect(result.current.runtimeStatus?.running).toBe(true));
  });
});

describe("AiSessionProvider's entry cache", () => {
  it("stores and retrieves an entry by incident id", async () => {
    installShell(async () => ({ running: false, port: null }));
    const { result } = renderHook(() => useAiSession(), { wrapper });
    await waitFor(() => expect(result.current.runtimeStatus).not.toBeNull());

    act(() => {
      result.current.setEntry("inc-1", { state: "result", result: { summary: "ok" } });
    });
    expect(result.current.getEntry("inc-1")).toEqual({ state: "result", result: { summary: "ok" } });
    expect(result.current.getEntry("inc-2")).toBeUndefined();
  });

  it("keeps separate incidents' entries independent - switching away and back must not lose or merge state", async () => {
    installShell(async () => ({ running: false, port: null }));
    const { result } = renderHook(() => useAiSession(), { wrapper });
    await waitFor(() => expect(result.current.runtimeStatus).not.toBeNull());

    act(() => {
      result.current.setEntry("inc-1", { state: "analyzing" });
      result.current.setEntry("inc-2", { state: "result", result: { summary: "different incident" } });
    });
    expect(result.current.getEntry("inc-1")).toEqual({ state: "analyzing" });
    expect(result.current.getEntry("inc-2")).toEqual({ state: "result", result: { summary: "different incident" } });
  });

  it("throws a clear error when used outside a provider", () => {
    // Suppress the expected React error-boundary console noise for this
    // one deliberately-erroring render.
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    function Lonely() {
      useAiSession();
      return null;
    }
    expect(() => render(<Lonely />)).toThrow(/AiSessionProvider/);
    spy.mockRestore();
  });
});
