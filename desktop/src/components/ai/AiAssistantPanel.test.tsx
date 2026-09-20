import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { LanguageProvider } from "../../i18n/LanguageContext";
import { AiSessionProvider } from "../../data/aiSession";
import { DEFAULT_AI_SETTINGS, writeAiSettings } from "../../data/aiSettings";
import { DEFAULT_SETTINGS } from "../../data/source";
import { AiAssistantPanel } from "./AiAssistantPanel";
import type { Incident, NetRewindEvent } from "../../types";

// This whole file exercises the panel once past the compile-time feature
// gate - see AiAssistantPanel.featureGate.test.tsx for the (unmocked)
// proof that it renders "not available" while the real flag stays off.
vi.mock("../../data/aiFeature", () => ({ AI_FEATURE_ENABLED: true }));

type Invoke = (cmd: string, args?: Record<string, unknown>) => Promise<unknown>;

function installShell(invoke: Invoke) {
  (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = { invoke };
}
function removeShell() {
  delete (window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}

afterEach(() => {
  removeShell();
  window.localStorage.clear();
});

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(
    <LanguageProvider>
      <AiSessionProvider>{ui}</AiSessionProvider>
    </LanguageProvider>,
  );
}

const incident: Incident = {
  incident_id: "inc-1",
  opened_at: Date.parse("2026-09-19T10:00:00Z") * 1e6,
  status: "closed",
  title: "Gateway hijacked",
  severity: "warn",
  confidence: 75,
  root_cause: { kind: "l2.arp_binding_changed", entity: "10.0.0.1", event_id: "e1", confidence: 75 },
  chain: [{ seq: 0, event_id: "e1", kind: "l2.arp_binding_changed", at: Date.parse("2026-09-19T10:00:30Z") * 1e6, subject: "10.0.0.1", relation: "", why: "" }],
  rule_id: "gateway-hijack",
};

const events: NetRewindEvent[] = [
  {
    event_id: "e1",
    schema_v: 1,
    ts_wall: Date.parse("2026-09-19T10:00:30Z") * 1e6,
    ts_mono: 0,
    observer_id: "obs",
    source: "netlink",
    kind: "l2.arp_binding_changed",
    severity: "warn",
    confidence: 75,
    subject: { kind: "host", id: "10.0.0.1", label: "10.0.0.1" },
  },
];

function analyzeResponseBody(handles: { handle: string; kind: string; ref: string }[], retrieval: Record<string, unknown>[] = []) {
  return JSON.stringify({
    version: 1,
    verdict: "answered",
    guardrail: { refuse: false, ceiling: 75 },
    handles,
    retrieval,
    output: {
      summary: "ARP binding changed on the gateway.",
      ranked_hypotheses: [{ cause: "l2.arp_binding_changed", entity: "10.0.0.1", confidence: 75 }],
      evidence_handles: handles.map((h) => h.handle),
      counter_evidence: [],
      unknowns: [],
      confidence_ceiling: 75,
      next_checks: [],
    },
    validation: { ok: true, retried: false, first_attempt_valid: true, violations: [] },
    timing: { prompt_ms: 1, predicted_ms: 2, total_ms: 3 },
  });
}

function installAnsweredShell(handles: { handle: string; kind: string; ref: string }[], retrieval: Record<string, unknown>[] = []) {
  installShell(async (cmd) => {
    if (cmd === "ai_status") return { running: true, port: 1234 };
    if (cmd === "ai_analyze") return analyzeResponseBody(handles, retrieval);
    throw new Error("unexpected command " + cmd);
  });
}

/** Same as installAnsweredShell, but also answers the /v1/notes routes the
 *  NotesSection reads/writes - agent_get for the existing annotation
 *  (404 = none yet), agent_request for the PUT/POST writes. `calls`
 *  records every agent_request invocation so a test can assert on the
 *  exact body sent. */
function installAnsweredShellWithNotes(
  handles: { handle: string; kind: string; ref: string }[],
  opts: { existingNote?: Record<string, unknown>; calls: Record<string, unknown>[] },
) {
  installShell(async (cmd, args) => {
    if (cmd === "ai_status") return { running: true, port: 1234 };
    if (cmd === "ai_analyze") return analyzeResponseBody(handles);
    if (cmd === "agent_get") {
      if (opts.existingNote) return { status: 200, body: JSON.stringify(opts.existingNote), headers: {} };
      return { status: 404, body: JSON.stringify({ error: { code: "not_found", message: "no note" } }), headers: {} };
    }
    if (cmd === "agent_request") {
      opts.calls.push(args ?? {});
      const method = args?.method as string;
      if (method === "PUT") return { status: 200, body: JSON.stringify({ incident_id: "inc-1", outcome: (JSON.parse(args?.body as string)).outcome }), headers: {} };
      return { status: 204, body: "", headers: {} };
    }
    if (cmd === "ai_report_redact") return `[redacted] ${args?.text as string}`;
    throw new Error("unexpected command " + cmd);
  });
}

describe("AiAssistantPanel: evidence handle navigation", () => {
  it("jumps to the Timeline, narrowed to the evidence window and highlighting the cited event", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([{ handle: "E1", kind: "event", ref: "e1" }]);
    const navigate = vi.fn();

    english(<AiAssistantPanel incident={incident} events={events} history={[]} navigate={navigate} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    const handleButton = await screen.findByText("E1");
    fireEvent.click(handleButton);

    expect(navigate).toHaveBeenCalledWith("timeline", {
      from: new Date(Date.parse("2026-09-19T10:00:30Z")).toISOString(),
      to: new Date(Date.parse("2026-09-19T10:00:30Z")).toISOString(),
      selection: "e1",
    });
  });

  it("does not navigate for a history or annotation handle - neither is a Timeline row", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([{ handle: "H1", kind: "history", ref: "inc-prior" }]);
    const navigate = vi.fn();

    english(<AiAssistantPanel incident={incident} events={events} history={[]} navigate={navigate} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    await waitFor(() => expect(screen.getByText("H1")).toBeInTheDocument());

    // A history handle renders as plain text, not a clickable button.
    expect(screen.getByText("H1").closest("button")).toBeNull();
    fireEvent.click(screen.getByText("H1"));
    expect(navigate).not.toHaveBeenCalled();
  });

  it("renders event handles as plain text (not buttons) when no navigate callback is given", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([{ handle: "E1", kind: "event", ref: "e1" }]);

    english(<AiAssistantPanel incident={incident} events={events} history={[]} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    await waitFor(() => expect(screen.getByText("E1")).toBeInTheDocument());
    expect(screen.getByText("E1").closest("button")).toBeNull();
  });
});

describe("AiAssistantPanel: previously-on-this-network retrieval", () => {
  const retrieval = [{ handle: "H1", incident_id: "inc-prior", rule_id: "gateway-hijack", root_cause_kind: "l2.arp_binding_changed", root_cause_entity: "10.0.0.1" }];

  it("lists a retrieved prior incident and jumps to it on click", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([], retrieval);
    const navigate = vi.fn();

    english(<AiAssistantPanel incident={incident} events={events} history={[]} navigate={navigate} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    const heading = await screen.findByText("Previously on this network", { exact: false });
    fireEvent.click(heading.parentElement!.querySelector("button")!);

    expect(navigate).toHaveBeenCalledWith("incidents", { selection: "inc-prior" });
  });

  it("omits the section entirely when there is nothing retrieved", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([]);
    english(<AiAssistantPanel incident={incident} events={events} history={[]} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    await waitFor(() => expect(screen.getByText("ARP binding changed on the gateway.")).toBeInTheDocument());
    expect(screen.queryByText("Previously on this network", { exact: false })).not.toBeInTheDocument();
  });
});

describe("AiAssistantPanel: follow-up questions", () => {
  it("asks a follow-up, shows the question and answer, and clears the input", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([]);

    english(<AiAssistantPanel incident={incident} events={events} history={[]} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    await waitFor(() => expect(screen.getByText("ARP binding changed on the gateway.")).toBeInTheDocument());

    const input = screen.getByPlaceholderText("Ask a follow-up question about this incident…") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "why did this happen twice?" } });
    fireEvent.click(screen.getByText("Ask"));

    await waitFor(() => expect(screen.getByText("why did this happen twice?")).toBeInTheDocument());
    expect(screen.getAllByText("ARP binding changed on the gateway.")).toHaveLength(2);
    expect(input.value).toBe("");
  });

  it("does not persist a follow-up thread when history opt-in is off", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf", historyOptIn: false });
    const calls: Record<string, unknown>[] = [];
    installAnsweredShellWithNotes([], { calls });

    english(<AiAssistantPanel incident={incident} events={events} history={[]} settings={{ ...DEFAULT_SETTINGS, kind: "live" }} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    await waitFor(() => expect(screen.getByText("ARP binding changed on the gateway.")).toBeInTheDocument());

    fireEvent.change(screen.getByPlaceholderText("Ask a follow-up question about this incident…"), { target: { value: "root cause?" } });
    fireEvent.click(screen.getByText("Ask"));
    await waitFor(() => expect(screen.getByText("root cause?")).toBeInTheDocument());

    expect(calls.some((c) => c.path === "/v1/notes/threads/inc-1")).toBe(false);
  });

  it("does not persist a follow-up thread for a non-live source, even with history opt-in on", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf", historyOptIn: true });
    const calls: Record<string, unknown>[] = [];
    installAnsweredShellWithNotes([], { calls });

    english(<AiAssistantPanel incident={incident} events={events} history={[]} settings={{ ...DEFAULT_SETTINGS, kind: "bundle" }} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    await waitFor(() => expect(screen.getByText("ARP binding changed on the gateway.")).toBeInTheDocument());

    fireEvent.change(screen.getByPlaceholderText("Ask a follow-up question about this incident…"), { target: { value: "root cause?" } });
    fireEvent.click(screen.getByText("Ask"));
    await waitFor(() => expect(screen.getByText("root cause?")).toBeInTheDocument());

    expect(calls.some((c) => c.path === "/v1/notes/threads/inc-1")).toBe(false);
  });

  it("persists a redacted follow-up thread turn on a live source with history opt-in on", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf", historyOptIn: true });
    const calls: Record<string, unknown>[] = [];
    installAnsweredShellWithNotes([], { calls });

    english(<AiAssistantPanel incident={incident} events={events} history={[]} settings={{ ...DEFAULT_SETTINGS, kind: "live" }} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    await waitFor(() => expect(screen.getByText("ARP binding changed on the gateway.")).toBeInTheDocument());

    fireEvent.change(screen.getByPlaceholderText("Ask a follow-up question about this incident…"), { target: { value: "root cause?" } });
    fireEvent.click(screen.getByText("Ask"));
    await waitFor(() => expect(screen.getByText("root cause?")).toBeInTheDocument());

    await waitFor(() => expect(calls.some((c) => c.path === "/v1/notes/threads/inc-1")).toBe(true));
    const threadCall = calls.find((c) => c.path === "/v1/notes/threads/inc-1");
    const body = JSON.parse(threadCall?.body as string);
    expect(body.question_redacted).toBe("[redacted] root cause?");
    expect(body.summary_redacted).toBe("[redacted] ARP binding changed on the gateway.");
    expect(typeof body.answer_id).toBe("string");
  });
});

describe("AiAssistantPanel: notes and feedback", () => {
  it("reports notes as unavailable when no live source is given", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([]);
    english(<AiAssistantPanel incident={incident} events={events} history={[]} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    expect(await screen.findByText("Notes are only available while connected to a live recorder.")).toBeInTheDocument();
  });

  it("reports notes as unavailable for a demo/bundle source", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShell([]);
    english(<AiAssistantPanel incident={incident} events={events} history={[]} settings={{ ...DEFAULT_SETTINGS, kind: "demo" }} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    expect(await screen.findByText("Notes are only available while connected to a live recorder.")).toBeInTheDocument();
  });

  it("saves an outcome note against a live source, with the incident's own rule/cause fields", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    const calls: Record<string, unknown>[] = [];
    installAnsweredShellWithNotes([], { calls });

    english(<AiAssistantPanel incident={incident} events={events} history={[]} settings={{ ...DEFAULT_SETTINGS, kind: "live" }} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    fireEvent.click(await screen.findByText("Confirmed cause"));
    fireEvent.click(screen.getByText("Save note"));

    await waitFor(() => expect(screen.getByText("Saved")).toBeInTheDocument());
    const putCall = calls.find((c) => c.method === "PUT");
    expect(putCall?.path).toBe("/v1/notes/incidents/inc-1");
    expect(JSON.parse(putCall?.body as string)).toMatchObject({
      rule_id: "gateway-hijack",
      root_cause_kind: "l2.arp_binding_changed",
      root_cause_entity: "10.0.0.1",
      outcome: "confirmed",
    });
  });

  it("pre-fills the form from an existing note", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    installAnsweredShellWithNotes([], {
      calls: [],
      existingNote: { incident_id: "inc-1", outcome: "unresolved", cause_note: "still investigating" },
    });

    english(<AiAssistantPanel incident={incident} events={events} history={[]} settings={{ ...DEFAULT_SETTINGS, kind: "live" }} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    await waitFor(() => expect(screen.getByText("Unresolved")).toBeInTheDocument());
    expect(await screen.findByDisplayValue("still investigating")).toBeInTheDocument();
    expect((screen.getByRole("radio", { name: "Unresolved" }) as HTMLInputElement).checked).toBe(true);
  });

  it("sends helpful/not-helpful feedback tied to this answer", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf", profile: "balanced" });
    const calls: Record<string, unknown>[] = [];
    installAnsweredShellWithNotes([], { calls });

    english(<AiAssistantPanel incident={incident} events={events} history={[]} settings={{ ...DEFAULT_SETTINGS, kind: "live" }} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    fireEvent.click(await screen.findByText("Helpful"));

    await waitFor(() => expect(screen.getByText("Thanks, your feedback was recorded locally.")).toBeInTheDocument());
    const feedbackCall = calls.find((c) => c.path === "/v1/notes/feedback");
    const body = JSON.parse(feedbackCall?.body as string);
    expect(body).toMatchObject({ incident_id: "inc-1", profile: "balanced", model_id: "small.gguf", helpful: true });
    expect(typeof body.answer_id).toBe("string");
    expect(body.answer_id.length).toBeGreaterThan(0);
  });
});

describe("AiAssistantPanel: copy report", () => {
  it("copies the redacted report - never the raw pre-redaction text", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    const writeText = vi.fn(async (_text: string) => {});
    Object.assign(navigator, { clipboard: { writeText } });
    installAnsweredShellWithNotes([], { calls: [] });

    english(<AiAssistantPanel incident={incident} events={events} history={[]} settings={{ ...DEFAULT_SETTINGS, kind: "live" }} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    fireEvent.click(await screen.findByText("Copy report"));

    await waitFor(() => expect(screen.getByText("Report copied")).toBeInTheDocument());
    expect(writeText).toHaveBeenCalledTimes(1);
    const copied = writeText.mock.calls[0][0];
    expect(copied.startsWith("[redacted] ")).toBe(true);
    expect(copied).toContain("gateway-hijack");
  });

  it("shows a failure message rather than crashing when the clipboard API rejects", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    Object.assign(navigator, { clipboard: { writeText: vi.fn(async () => Promise.reject(new Error("denied"))) } });
    installAnsweredShell([]);

    english(<AiAssistantPanel incident={incident} events={events} history={[]} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    fireEvent.click(await screen.findByText("Copy report"));

    expect(await screen.findByText("Could not copy the report")).toBeInTheDocument();
  });

  it("composes the report without a note when there is no live source, rather than failing", async () => {
    writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
    const writeText = vi.fn(async (_text: string) => {});
    Object.assign(navigator, { clipboard: { writeText } });
    installShell(async (cmd, args) => {
      if (cmd === "ai_status") return { running: true, port: 1234 };
      if (cmd === "ai_analyze") return analyzeResponseBody([]);
      if (cmd === "ai_report_redact") return args?.text as string;
      throw new Error("unexpected command " + cmd);
    });

    english(<AiAssistantPanel incident={incident} events={events} history={[]} />);
    fireEvent.click(await screen.findByText("Analyze this incident locally"));
    fireEvent.click(await screen.findByText("Copy report"));

    await waitFor(() => expect(screen.getByText("Report copied")).toBeInTheDocument());
    expect(writeText.mock.calls[0][0]).toContain("(none recorded)");
  });
});
