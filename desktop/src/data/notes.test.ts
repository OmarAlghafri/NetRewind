import { afterEach, describe, expect, it } from "vitest";
import {
  deleteNotesAnnotation,
  forgetAllNotes,
  getNotesAnnotation,
  getNotesSimilar,
  getNotesStats,
  postNotesFeedback,
  putNotesAnnotation,
} from "./notes";

type Invoke = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;

function installShell(invoke: Invoke) {
  (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = { invoke };
}

function removeShell() {
  delete (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}

afterEach(() => {
  removeShell();
});

describe("getNotesAnnotation", () => {
  it("returns the parsed annotation on 200", async () => {
    installShell(async (cmd, args) => {
      if (cmd !== "agent_get") throw new Error("unexpected command " + cmd);
      expect(args?.path).toBe("/v1/notes/incidents/inc-1");
      return { status: 200, body: JSON.stringify({ incident_id: "inc-1", outcome: "confirmed" }), headers: {} };
    });
    const a = await getNotesAnnotation("", "inc-1");
    expect(a?.outcome).toBe("confirmed");
  });

  it("returns null instead of throwing on a 404 (no note yet)", async () => {
    installShell(async () => ({
      status: 404,
      body: JSON.stringify({ error: { code: "not_found", message: "no note for this incident" } }),
      headers: {},
    }));
    const a = await getNotesAnnotation("", "inc-never-annotated");
    expect(a).toBeNull();
  });

  it("still throws for a non-404 error", async () => {
    installShell(async () => ({
      status: 500,
      body: JSON.stringify({ error: { code: "notes_read_failed", message: "boom" } }),
      headers: {},
    }));
    await expect(getNotesAnnotation("", "inc-1")).rejects.toMatchObject({ code: "notes_read_failed" });
  });

  it("URL-encodes the incident id", async () => {
    let seenPath: string | undefined;
    installShell(async (_cmd, args) => {
      seenPath = args?.path as string;
      return { status: 200, body: JSON.stringify({ incident_id: "a/b" }), headers: {} };
    });
    await getNotesAnnotation("", "a/b c");
    expect(seenPath).toBe("/v1/notes/incidents/a%2Fb%20c");
  });
});

describe("putNotesAnnotation", () => {
  it("sends the annotation fields as the PUT body", async () => {
    let seenArgs: Record<string, unknown> | undefined;
    installShell(async (cmd, args) => {
      seenArgs = args;
      if (cmd !== "agent_request") throw new Error("unexpected command " + cmd);
      return { status: 200, body: JSON.stringify({ incident_id: "inc-1", outcome: "confirmed" }), headers: {} };
    });
    await putNotesAnnotation("", "inc-1", {
      ruleId: "gateway-hijack",
      rootCauseKind: "l2.arp_binding_changed",
      rootCauseEntity: "10.99.0.1",
      openedAtNs: 123,
      outcome: "confirmed",
      causeNote: "bad switch port",
    });
    expect(seenArgs?.method).toBe("PUT");
    expect(seenArgs?.path).toBe("/v1/notes/incidents/inc-1");
    expect(JSON.parse(seenArgs?.body as string)).toMatchObject({
      rule_id: "gateway-hijack",
      root_cause_kind: "l2.arp_binding_changed",
      outcome: "confirmed",
      cause_note: "bad switch port",
    });
  });
});

describe("deleteNotesAnnotation / forgetAllNotes", () => {
  it("sends DELETE to the per-incident path", async () => {
    let seenArgs: Record<string, unknown> | undefined;
    installShell(async (_cmd, args) => {
      seenArgs = args;
      return { status: 204, body: "", headers: {} };
    });
    await deleteNotesAnnotation("", "inc-1");
    expect(seenArgs?.method).toBe("DELETE");
    expect(seenArgs?.path).toBe("/v1/notes/incidents/inc-1");
  });

  it("sends DELETE to the collection path for forget-all", async () => {
    let seenArgs: Record<string, unknown> | undefined;
    installShell(async (_cmd, args) => {
      seenArgs = args;
      return { status: 204, body: "", headers: {} };
    });
    await forgetAllNotes("");
    expect(seenArgs?.path).toBe("/v1/notes");
  });
});

describe("getNotesSimilar", () => {
  it("builds the query string from the given params", async () => {
    let seenPath: string | undefined;
    installShell(async (_cmd, args) => {
      seenPath = args?.path as string;
      return { status: 200, body: "[]", headers: {} };
    });
    await getNotesSimilar("", { ruleId: "gateway-hijack", rootCauseKind: "l2.arp_binding_changed", entity: "10.99.0.1", exclude: "inc-1", limit: 3 });
    const url = new URL("http://x" + seenPath);
    expect(url.searchParams.get("rule_id")).toBe("gateway-hijack");
    expect(url.searchParams.get("root_cause_kind")).toBe("l2.arp_binding_changed");
    expect(url.searchParams.get("entity")).toBe("10.99.0.1");
    expect(url.searchParams.get("exclude")).toBe("inc-1");
    expect(url.searchParams.get("limit")).toBe("3");
  });

  it("omits optional params entirely when not given", async () => {
    let seenPath: string | undefined;
    installShell(async (_cmd, args) => {
      seenPath = args?.path as string;
      return { status: 200, body: "[]", headers: {} };
    });
    await getNotesSimilar("", { ruleId: "r", rootCauseKind: "k" });
    expect(seenPath).not.toContain("entity=");
    expect(seenPath).not.toContain("limit=");
  });
});

describe("postNotesFeedback", () => {
  it("sends the feedback fields as the POST body", async () => {
    let seenArgs: Record<string, unknown> | undefined;
    installShell(async (_cmd, args) => {
      seenArgs = args;
      return { status: 204, body: "", headers: {} };
    });
    await postNotesFeedback("", { answerId: "a1", incidentId: "inc-1", profile: "balanced", modelId: "qwen3-4b", helpful: true });
    expect(seenArgs?.path).toBe("/v1/notes/feedback");
    expect(JSON.parse(seenArgs?.body as string)).toMatchObject({ answer_id: "a1", incident_id: "inc-1", helpful: true });
  });
});

describe("getNotesStats", () => {
  it("returns the counts and byte size", async () => {
    installShell(async (cmd, args) => {
      if (cmd !== "agent_get") throw new Error("unexpected command " + cmd);
      expect(args?.path).toBe("/v1/notes/stats");
      return { status: 200, body: JSON.stringify({ annotations: 2, feedback: 1, threads: 0, bytes: 4096 }), headers: {} };
    });
    const stats = await getNotesStats("");
    expect(stats).toEqual({ annotations: 2, feedback: 1, threads: 0, bytes: 4096 });
  });
});
