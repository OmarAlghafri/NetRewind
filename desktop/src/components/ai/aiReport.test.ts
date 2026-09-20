import { describe, expect, it } from "vitest";
import { buildAiReport } from "./aiReport";
import type { Incident } from "../../types";
import type { AiAnalyzeResponse } from "../../data/aiTypes";
import type { NotesAnnotation } from "../../data/notes";

const incident: Incident = {
  incident_id: "inc-1",
  opened_at: 1000,
  status: "closed",
  title: "Gateway hijacked",
  severity: "warn",
  confidence: 75,
  root_cause: { kind: "l2.arp_binding_changed", entity: "10.0.0.1", event_id: "e1", confidence: 75 },
  chain: [{ seq: 0, event_id: "e1", kind: "l2.arp_binding_changed", at: 1000, subject: "10.0.0.1", relation: "", why: "" }],
  rule_id: "gateway-hijack",
};

const response: AiAnalyzeResponse = {
  version: 1,
  verdict: "answered",
  guardrail: { refuse: false, ceiling: 75 },
  handles: [],
  output: {
    summary: "The gateway changed hands.",
    ranked_hypotheses: [{ cause: "l2.arp_binding_changed", entity: "10.0.0.1", confidence: 75 }],
    evidence_handles: ["E1"],
    counter_evidence: ["nothing counters this"],
    unknowns: ["which switch port"],
    confidence_ceiling: 75,
    next_checks: ["check the switch port table"],
  },
  validation: { ok: true, retried: false, first_attempt_valid: true, violations: [] },
  timing: { prompt_ms: 1, predicted_ms: 2, total_ms: 3 },
};

describe("buildAiReport", () => {
  it("includes the engine facts, no-notes placeholder, and model text sections", () => {
    const text = buildAiReport({ incident, response, note: null, lang: "en" });
    expect(text).toContain("Engine facts (deterministic)");
    expect(text).toContain("gateway-hijack");
    expect(text).toContain("l2.arp_binding_changed");
    expect(text).toContain("(none recorded)");
    expect(text).toContain("Local model text");
    expect(text).toContain("The gateway changed hands.");
    expect(text).toContain("check the switch port table");
  });

  it("includes the operator's note when one exists", () => {
    const note: NotesAnnotation = {
      incident_id: "inc-1",
      fingerprint: "fp",
      rule_id: "gateway-hijack",
      root_cause_kind: "l2.arp_binding_changed",
      root_cause_entity: "10.0.0.1",
      opened_at_ns: 1000,
      outcome: "confirmed",
      cause_note: "bad switch port",
      created_at_ms: 0,
      updated_at_ms: 0,
    };
    const text = buildAiReport({ incident, response, note, lang: "en" });
    expect(text).toContain("Confirmed cause");
    expect(text).toContain("bad switch port");
    expect(text).not.toContain("(none recorded)");
  });

  it("reads in Arabic when asked, with the same underlying values", () => {
    const text = buildAiReport({ incident, response, note: null, lang: "ar" });
    expect(text).toContain("وقائع محرك الارتباط");
    expect(text).toContain("gateway-hijack");
  });
});
