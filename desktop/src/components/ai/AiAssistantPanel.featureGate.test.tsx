import { expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { LanguageProvider } from "../../i18n/LanguageContext";
import { AiSessionProvider } from "../../data/aiSession";
import { DEFAULT_AI_SETTINGS, writeAiSettings } from "../../data/aiSettings";
import { AiAssistantPanel } from "./AiAssistantPanel";
import type { Incident } from "../../types";

// Deliberately does NOT mock ../../data/aiFeature (unlike
// AiAssistantPanel.test.tsx) - proves the panel renders "not available"
// against the real, currently AI_FEATURE_ENABLED = false constant, even
// when localStorage settings claim the assistant is fully configured.

const incident: Incident = {
  incident_id: "inc-1",
  opened_at: 1000,
  status: "closed",
  title: "test",
  severity: "warn",
  confidence: 75,
  root_cause: { kind: "l2.arp_binding_changed", entity: "10.0.0.1", event_id: "e1", confidence: 75 },
  chain: [],
  rule_id: "gateway-hijack",
};

it("shows 'not available' against the real feature flag, even fully configured", () => {
  writeAiSettings({ ...DEFAULT_AI_SETTINGS, enabled: true, modelFileName: "small.gguf" });
  window.localStorage.setItem("netrewind.lang", "en");
  render(
    <LanguageProvider>
      <AiSessionProvider>
        <AiAssistantPanel incident={incident} events={[]} history={[]} />
      </AiSessionProvider>
    </LanguageProvider>,
  );
  expect(screen.getByText("This feature is not available yet in this build.")).toBeInTheDocument();
  expect(screen.queryByText("Analyze this incident locally")).not.toBeInTheDocument();
});
