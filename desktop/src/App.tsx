import { useEffect, useState } from "react";
import "./App.css";
import { LanguageProvider, useLanguage } from "./i18n/LanguageContext";
import { Sidebar, type Page } from "./components/Sidebar";
import { Overview } from "./pages/Overview";
import { Incidents } from "./pages/Incidents";
import { Timeline } from "./pages/Timeline";
import { Host } from "./pages/Host";
import { Rules } from "./pages/Rules";
import { Evidence } from "./pages/Evidence";
import { Settings } from "./pages/Settings";
import { Diagnostics } from "./pages/Diagnostics";
import { loadDemoEvents, loadDemoIncidents } from "./demo/loadDemoData";
import { Wizard } from "./onboarding/Wizard";
import { readOnboardingComplete, writeOnboardingComplete } from "./onboarding/onboardingStorage";

function Shell() {
  const { t, dir } = useLanguage();
  const [page, setPage] = useState<Page>("overview");
  const [onboardingComplete, setOnboardingComplete] = useState<boolean>(readOnboardingComplete);

  useEffect(() => {
    document.documentElement.dir = dir;
    document.documentElement.lang = dir === "rtl" ? "ar" : "en";
  }, [dir]);

  // Demo mode is the only data source wired up so far
  // (PRODUCT_RELEASE_PLAN_AR.md §5, Phase 2). A live source read through
  // internal/api/v1 over internal/ipc replaces this call, not the pages
  // themselves - every page below takes plain events/incidents props and
  // does not know or care where they came from.
  const events = loadDemoEvents();
  const incidents = loadDemoIncidents();

  // First-run wizard (PRODUCT_RELEASE_PLAN_AR.md §5, Phase 2: "wizard أول
  // تشغيل"): shown before the normal eight-page shell until finished or
  // explicitly skipped (localStorage remembers completion, same guarded
  // pattern as i18n/LanguageContext.tsx), and re-openable later from
  // Settings.
  if (!onboardingComplete) {
    return (
      <Wizard
        events={events}
        incidents={incidents}
        onFinish={() => {
          writeOnboardingComplete(true);
          setOnboardingComplete(true);
        }}
      />
    );
  }

  return (
    <div className="app-shell">
      <Sidebar page={page} onNavigate={setPage} />
      <main className="main">
        <div className="demo-banner">{t("demo_banner")}</div>
        {page === "overview" && <Overview events={events} />}
        {page === "incidents" && <Incidents incidents={incidents} />}
        {page === "timeline" && <Timeline events={events} />}
        {page === "host" && <Host events={events} />}
        {page === "rules" && <Rules incidents={incidents} />}
        {page === "evidence" && <Evidence events={events} incidents={incidents} />}
        {page === "settings" && (
          <Settings
            onReopenWizard={() => {
              writeOnboardingComplete(false);
              setOnboardingComplete(false);
            }}
          />
        )}
        {page === "diagnostics" && <Diagnostics events={events} incidents={incidents} />}
      </main>
    </div>
  );
}

export default function App() {
  return (
    <LanguageProvider>
      <Shell />
    </LanguageProvider>
  );
}
