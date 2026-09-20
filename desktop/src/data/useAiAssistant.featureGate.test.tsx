import { describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { AiSessionProvider } from "./aiSession";
import { DEFAULT_AI_SETTINGS, type AiSettings } from "./aiSettings";
import { useAiAssistant } from "./useAiAssistant";
import type { Incident } from "../types";

// Deliberately does NOT mock ./aiFeature (unlike useAiAssistant.test.tsx) -
// this is the one place the hook's behavior against the real, currently
// AI_FEATURE_ENABLED = false compile-time constant is proven, so a change
// to vite.config.ts's __AI_FEATURE_ENABLED__ define or aiFeature.ts's own
// re-export would be caught here even if every other test in the suite
// mocks it away.

function wrapper({ children }: { children: ReactNode }) {
  return <AiSessionProvider>{children}</AiSessionProvider>;
}

const incident: Incident = {
  incident_id: "inc-1",
  opened_at: 1000,
  status: "closed",
  title: "test",
  severity: "warn",
  confidence: 75,
  root_cause: { kind: "l2.arp_binding_changed", entity: "10.0.0.1", event_id: "e1", confidence: 75 },
  chain: [{ seq: 0, event_id: "e1", kind: "l2.arp_binding_changed", at: 1000, subject: "10.0.0.1", relation: "", why: "" }],
  rule_id: "gateway-hijack",
};

const enabledSettings: AiSettings = { ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" };

describe("useAiAssistant against the real (unmocked) feature flag", () => {
  it("reports not_available even when settings claim the assistant is fully configured", () => {
    const { result } = renderHook(() => useAiAssistant(incident, [], [], "en", enabledSettings), { wrapper });
    expect(result.current.state).toBe("not_available");
  });

  it("reports not_available regardless of settings.enabled", () => {
    const { result } = renderHook(() => useAiAssistant(incident, [], [], "en", DEFAULT_AI_SETTINGS), { wrapper });
    expect(result.current.state).toBe("not_available");
  });
});
