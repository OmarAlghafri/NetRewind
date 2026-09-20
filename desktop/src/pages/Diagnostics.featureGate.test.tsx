import { expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { AiSessionProvider } from "../data/aiSession";
import { Diagnostics } from "./Diagnostics";
import { DEFAULT_SETTINGS } from "../data/source";
import { DEFAULT_AI_SETTINGS } from "../data/aiSettings";
import type { Record } from "../data/useRecord";

// Deliberately does NOT mock ../data/aiFeature - proves the "Local AI
// assistant" and "Notes memory" cards are hidden entirely against the
// real, currently-false constant, even with aiSettings.enabled true.

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(
    <LanguageProvider>
      <AiSessionProvider>{ui}</AiSessionProvider>
    </LanguageProvider>,
  );
}

function record(): Record {
  return {
    status: "ready",
    error: "",
    events: [],
    incidents: [],
    health: null,
    capabilities: [],
    rules: [],
    manifest: null,
    bundleSigned: false,
    bundleHasSignature: false,
    bundleNotes: null,
    refreshedAt: null,
    stale: false,
    refresh: () => {},
  };
}

it("never renders the AI or notes cards while the real feature flag is off", () => {
  english(
    <Diagnostics
      events={[]}
      incidents={[]}
      settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
      record={record()}
      aiSettings={{ ...DEFAULT_AI_SETTINGS, enabled: true }}
    />,
  );
  expect(screen.queryByText("Local AI assistant")).not.toBeInTheDocument();
  expect(screen.queryByText("Notes memory")).not.toBeInTheDocument();
});
