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
  uptime_seconds: 5, store: { path: "/x", events: 2 }, collectors: { up: 1, down: 0, unsupported: 0 },
};

/**
 * A fake recorder that actually behaves like the real cursor/ETag API:
 * tracks how many times each route was hit, returns a real next_cursor
 * that changes call to call, folds a specific event's count on the second
 * events call (the ADR 0005 scenario), and answers a matching
 * If-None-Match with a real 304.
 */
function statefulFakeRecorder() {
  const calls: { path: string; headers?: Record<string, string> }[] = [];
  let eventsCall = 0;
  let cancelledIds: string[] = [];
  const rulesETag = '"rules-v1"';
  const capsETag = '"caps-v1"';

  const invoke: Invoke = async (cmd, args) => {
    if (cmd === "agent_cancel") {
      cancelledIds.push(String(args?.requestId));
      return undefined;
    }
    if (cmd !== "agent_get") throw new Error("unexpected command " + cmd);
    const path = String(args?.path);
    const headers = args?.headers as Record<string, string> | undefined;
    calls.push({ path, headers });
    const json = (body: unknown, extraHeaders: Record<string, string> = {}) => ({
      status: 200,
      body: JSON.stringify(body),
      headers: extraHeaders,
    });

    if (path === "/v1/health") return json(health);

    if (path === "/v1/capabilities") {
      if (headers?.["If-None-Match"] === capsETag) return { status: 304, body: "", headers: {} };
      return json({ capabilities: [{ name: "c1", platform: "linux", privilege: "", coverage: [], status: "up", last_change: "", last_seen: "" }] }, { etag: capsETag });
    }
    if (path === "/v1/rules") {
      if (headers?.["If-None-Match"] === rulesETag) return { status: 304, body: "", headers: {} };
      return json({ rules: [{ id: "r1", title: "t", severity: "warn", confidence: 80, window: "1m0s", root_cause: "x", advice: "y" }] }, { etag: rulesETag });
    }
    if (path.startsWith("/v1/events")) {
      eventsCall += 1;
      if (eventsCall === 1) {
        return json({
          events: [{ event_id: "01A", kind: "link.down", source: "netlink", severity: "warn", ts_wall: 1, subject: { kind: "iface", id: "1", label: "eth0" }, count: 1 }],
          next_cursor: "CURSOR-1",
          has_more: false,
        });
      }
      // Second call: a delta with the fold ADR 0005 exists for - the same
      // event id, count grown - plus one genuinely new event.
      return json({
        events: [
          { event_id: "01A", kind: "link.down", source: "netlink", severity: "warn", ts_wall: 1, subject: { kind: "iface", id: "1", label: "eth0" }, count: 3 },
          { event_id: "01B", kind: "link.up", source: "netlink", severity: "notice", ts_wall: 2, subject: { kind: "iface", id: "1", label: "eth0" }, count: 1 },
        ],
        next_cursor: "CURSOR-2",
        has_more: false,
      });
    }
    if (path.startsWith("/v1/incidents")) return json({ incidents: [], next_cursor: "INC-CURSOR-1", has_more: false });
    return { status: 404, body: JSON.stringify({ error: { code: "not_found", message: "no such route" } }) };
  };
  return { invoke, calls: () => calls, cancelledIds: () => cancelledIds };
}

describe("useRecord live polling: cursors, conditional GET, cancellation", () => {
  afterEach(() => {
    removeShell();
    vi.useRealTimers();
  });

  it("uses since= on the first load and cursor= on the next poll, merging a folded event's new count instead of duplicating it", async () => {
    const fake = statefulFakeRecorder();
    installShell(fake.invoke);
    const { result } = renderHook(() => useRecord({ ...DEFAULT_SETTINGS, kind: "live", refreshSeconds: 2 }));

    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(result.current.events).toHaveLength(1);
    expect(result.current.events[0].count).toBe(1);
    expect(fake.calls().some((c) => c.path.startsWith("/v1/events?since="))).toBe(true);

    // Trigger the second poll directly rather than waiting on the real
    // timer - refresh() is exactly what the interval calls internally.
    result.current.refresh();
    await waitFor(() => expect(result.current.events).toHaveLength(2));

    const secondEventsCall = fake.calls().filter((c) => c.path.startsWith("/v1/events"))[1];
    expect(secondEventsCall.path).toBe("/v1/events?cursor=CURSOR-1&limit=5000");

    // The folded event (01A) is updated in place, not duplicated; the
    // genuinely new one (01B) is appended after it.
    expect(result.current.events[0].event_id).toBe("01A");
    expect(result.current.events[0].count).toBe(3);
    expect(result.current.events[1].event_id).toBe("01B");
  });

  it("sends If-None-Match on the second poll and keeps the existing rules/capabilities on a 304 instead of clearing them", async () => {
    const fake = statefulFakeRecorder();
    installShell(fake.invoke);
    const { result } = renderHook(() => useRecord({ ...DEFAULT_SETTINGS, kind: "live", refreshSeconds: 2 }));
    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(result.current.rules).toHaveLength(1);
    expect(result.current.capabilities).toHaveLength(1);

    result.current.refresh();
    await waitFor(() => expect(fake.calls().filter((c) => c.path === "/v1/rules")).toHaveLength(2));

    const secondRulesCall = fake.calls().filter((c) => c.path === "/v1/rules")[1];
    expect(secondRulesCall.headers?.["If-None-Match"]).toBe('"rules-v1"');
    // Still there - a 304 must not be treated as "empty".
    expect(result.current.rules).toHaveLength(1);
    expect(result.current.capabilities).toHaveLength(1);
  });

  it("cancels the previous poll's in-flight requests when settings change before it resolves", async () => {
    const fake = statefulFakeRecorder();
    installShell(fake.invoke);
    const { result, rerender } = renderHook(({ settings }) => useRecord(settings), {
      initialProps: { settings: { ...DEFAULT_SETTINGS, kind: "live", refreshSeconds: 60 } as typeof DEFAULT_SETTINGS & { kind: "live"; endpoint: string; refreshSeconds: number } },
    });
    await waitFor(() => expect(result.current.status).toBe("ready"));

    // Changing the endpoint starts a new generation/poll; the previous
    // generation's requests (already finished by now, in this fake) must
    // still have been offered for cancellation - the point being that
    // cancelPoll is actually invoked on supersession, not only that it
    // would be safe to call.
    rerender({ settings: { ...DEFAULT_SETTINGS, kind: "live", endpoint: "\\\\.\\pipe\\other", refreshSeconds: 60 } });
    await waitFor(() => expect(fake.cancelledIds().length).toBeGreaterThan(0));
    expect(fake.cancelledIds().some((id) => id.includes("health"))).toBe(true);
  });
});
