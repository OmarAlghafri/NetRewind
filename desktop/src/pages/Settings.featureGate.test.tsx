import { expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { Settings } from "./Settings";
import { DEFAULT_SETTINGS } from "../data/source";
import { DEFAULT_AI_SETTINGS } from "../data/aiSettings";

// Deliberately does NOT mock ../data/aiFeature - proves the "Local AI
// assistant" card is hidden entirely against the real, currently-false
// constant, so there is no way to reach aiSettings.enabled from the UI at
// all while the feature stays off.

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(<LanguageProvider>{ui}</LanguageProvider>);
}

it("never renders the local AI assistant card while the real feature flag is off", () => {
  english(
    <Settings
      settings={DEFAULT_SETTINGS}
      onChange={() => {}}
      aiSettings={DEFAULT_AI_SETTINGS}
      onChangeAiSettings={() => {}}
      onReopenWizard={() => {}}
    />,
  );
  expect(screen.queryByText("Local AI assistant")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Turn on the local AI assistant")).not.toBeInTheDocument();
});
