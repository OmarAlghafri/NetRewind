import { afterEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { useRecord } from "./useRecord";
import { DEFAULT_SETTINGS } from "./source";

type Invoke = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;

function installShell(invoke: Invoke) {
  (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = { invoke };
}

function removeShell() {
  delete (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}

const health = {
  status: "ok", version: "1.0.0", schema_version: 1, api_version: 1, observer_id: "obs",
  uptime_seconds: 5, store: { path: "/var/lib/netrewind/events.db", events: 2 },
  collectors: { up: 4, down: 0, unsupported: 8 },
};

/** A fake recorder answering the API paths the hook asks for. */
function fakeRecorder(): { invoke: Invoke; calls: string[] } {
  const calls: string[] = [];
  const invoke: Invoke = async (cmd, args) => {
    if (cmd !== "agent_get") throw new Error("unexpected command " + cmd);
    const path = String(args?.path);
    calls.push(path);
    const json = (body: unknown) => ({ status: 200, body: JSON.stringify(body) });
    if (path === "/v1/health") return json(health);
    if (path === "/v1/capabilities") return json({ capabilities: [{ name: "iphelper.link", platform: "windows", privilege: "none", coverage: ["link.*"], status: "up", last_change: "", last_seen: "" }] });
    if (path.startsWith("/v1/events")) return json({ events: [{ event_id: "01A", kind: "link.down", source: "iphelper", severity: "warn", ts_wall: 1, subject: { kind: "iface", id: "1", label: "eth0" } }] });
    if (path.startsWith("/v1/incidents")) return json({ incidents: [] });
    if (path === "/v1/rules") return json({ rules: [{ id: "gateway-hijack", title: "t", severity: "error", confidence: 90, window: "2m0s", root_cause: "x", advice: "y" }] });
    return { status: 404, body: JSON.stringify({ error: { code: "not_found", message: "no such route" } }) };
  };
  return { invoke, calls };
}

describe("useRecord", () => {
  afterEach(() => removeShell());

  it("serves the demo recording synchronously without the shell", async () => {
    const { result } = renderHook(() => useRecord(DEFAULT_SETTINGS));
    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(result.current.events.length).toBeGreaterThan(0);
    expect(result.current.incidents.length).toBeGreaterThan(0);
    expect(result.current.health).toBeNull();
  });

  it("refuses the live source outside the shell with a distinct error", async () => {
    const { result } = renderHook(() => useRecord({ ...DEFAULT_SETTINGS, kind: "live" }));
    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error).toBe("shell_required");
  });

  it("fetches health, capabilities, events, incidents and rules from a live recorder", async () => {
    const fake = fakeRecorder();
    installShell(fake.invoke);
    const { result } = renderHook(() => useRecord({ ...DEFAULT_SETTINGS, kind: "live", refreshSeconds: 60 }));
    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(result.current.health?.observer_id).toBe("obs");
    expect(result.current.capabilities.map((c) => c.name)).toEqual(["iphelper.link"]);
    expect(result.current.events[0].kind).toBe("link.down");
    expect(result.current.rules[0].id).toBe("gateway-hijack");
    expect(fake.calls).toContain("/v1/health");
    expect(fake.calls.some((p) => p.startsWith("/v1/events?since="))).toBe(true);
    expect(result.current.refreshedAt).not.toBeNull();
  });

  it("reports the recorder's own failure message when it cannot be reached", async () => {
    installShell(async () => {
      throw new Error("no recorder is listening at \\\\.\\pipe\\netrewind-api (is the netrewindd service running?)");
    });
    const { result } = renderHook(() => useRecord({ ...DEFAULT_SETTINGS, kind: "live", refreshSeconds: 60 }));
    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error).toContain("no recorder is listening");
  });

  it("surfaces an API error status as its message", async () => {
    installShell(async () => ({ status: 500, body: JSON.stringify({ error: { code: "query_failed", message: "database is locked" } }) }));
    const { result } = renderHook(() => useRecord({ ...DEFAULT_SETTINGS, kind: "live", refreshSeconds: 60 }));
    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error).toBe("database is locked");
  });

  it("opens a bundle through the shell and exposes its manifest", async () => {
    const open = vi.fn(async (cmd: string, args?: Record<string, unknown>) => {
      expect(cmd).toBe("bundle_open");
      expect(args).toEqual({ path: "C:/x/b.tar.gz", publicKey: null });
      return {
        manifest: { format_version: 1, schema_version: 1, app_version: "1.0.0", observer_id: "obs", created_at: "", window_from: "", window_to: "", event_count: 1, incident_count: 0, truncated: false, redacted: true, capabilities: [] },
        events: [{ event_id: "01B", kind: "link.up" }],
        incidents: [],
        signed: false,
        has_signature: false,
      };
    });
    installShell(open as Invoke);
    const { result } = renderHook(() => useRecord({ ...DEFAULT_SETTINGS, kind: "bundle", bundlePath: "C:/x/b.tar.gz" }));
    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(result.current.manifest?.observer_id).toBe("obs");
    expect(result.current.events[0].kind).toBe("link.up");
    expect(open).toHaveBeenCalledTimes(1);
  });

  it("reports a bundle source with no file chosen", async () => {
    installShell(async () => ({}));
    const { result } = renderHook(() => useRecord({ ...DEFAULT_SETTINGS, kind: "bundle" }));
    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error).toBe("no_bundle");
  });
});
