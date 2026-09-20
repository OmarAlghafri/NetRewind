import { beforeEach, describe, expect, it } from "vitest";
import { DEFAULT_AI_SETTINGS, normalizeAiSettings, readAiSettings, writeAiSettings } from "./aiSettings";

describe("ai settings", () => {
  beforeEach(() => window.localStorage.clear());

  it("round-trips through localStorage", () => {
    writeAiSettings({ enabled: true, profile: "full", modelFileName: " small.gguf ", threads: 4, historyOptIn: true, debugSavePrompts: false });
    expect(readAiSettings()).toEqual({ enabled: true, profile: "full", modelFileName: "small.gguf", threads: 4, historyOptIn: true, debugSavePrompts: false });
  });

  it("falls back to the defaults when nothing or garbage is stored", () => {
    expect(readAiSettings()).toEqual(DEFAULT_AI_SETTINGS);
    window.localStorage.setItem("netrewind.ai", "{not json");
    expect(readAiSettings()).toEqual(DEFAULT_AI_SETTINGS);
  });

  it("defaults to disabled - the feature must never turn itself on for an existing installation", () => {
    expect(DEFAULT_AI_SETTINGS.enabled).toBe(false);
  });

  it("rejects an unknown profile and clamps the thread count", () => {
    const n = normalizeAiSettings({ ...DEFAULT_AI_SETTINGS, profile: "huge" as never, threads: -5 });
    expect(n.profile).toBe("balanced");
    expect(n.threads).toBe(0);
    expect(normalizeAiSettings({ ...DEFAULT_AI_SETTINGS, threads: 999 }).threads).toBe(64);
    expect(normalizeAiSettings({ ...DEFAULT_AI_SETTINGS, threads: Number.NaN }).threads).toBe(DEFAULT_AI_SETTINGS.threads);
  });
});
