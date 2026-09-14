import { beforeEach, describe, expect, it } from "vitest";
import { DEFAULT_SETTINGS, normalize, readSettings, writeSettings } from "./source";

describe("source settings", () => {
  beforeEach(() => window.localStorage.clear());

  it("round-trips through localStorage", () => {
    writeSettings({ kind: "live", endpoint: " /tmp/api.sock ", refreshSeconds: 7, publicKey: "abc", bundlePath: "" });
    expect(readSettings()).toEqual({ kind: "live", endpoint: "/tmp/api.sock", refreshSeconds: 7, publicKey: "abc", bundlePath: "" });
  });

  it("falls back to the defaults when nothing or garbage is stored", () => {
    expect(readSettings()).toEqual(DEFAULT_SETTINGS);
    window.localStorage.setItem("netrewind.source", "{not json");
    expect(readSettings()).toEqual(DEFAULT_SETTINGS);
  });

  it("rejects an unknown source kind and clamps the refresh interval", () => {
    const n = normalize({ ...DEFAULT_SETTINGS, kind: "cloud" as never, refreshSeconds: 0 });
    expect(n.kind).toBe("demo");
    expect(n.refreshSeconds).toBe(2);
    expect(normalize({ ...DEFAULT_SETTINGS, refreshSeconds: 999 }).refreshSeconds).toBe(60);
    expect(normalize({ ...DEFAULT_SETTINGS, refreshSeconds: Number.NaN }).refreshSeconds).toBe(DEFAULT_SETTINGS.refreshSeconds);
  });
});
