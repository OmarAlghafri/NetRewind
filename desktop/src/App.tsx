import { useEffect, useState } from "react";
import "./App.css";
import { LanguageProvider, useLanguage } from "./i18n/LanguageContext";
import { Sidebar, type Page } from "./components/Sidebar";
import { SourceBanner } from "./components/SourceBanner";
import { Overview } from "./pages/Overview";
import { Incidents } from "./pages/Incidents";
import { Timeline } from "./pages/Timeline";
import { Host } from "./pages/Host";
import { Rules } from "./pages/Rules";
import { Evidence } from "./pages/Evidence";
import { Settings } from "./pages/Settings";
import { Diagnostics } from "./pages/Diagnostics";
import { Wizard } from "./onboarding/Wizard";
import { readOnboardingComplete, writeOnboardingComplete } from "./onboarding/onboardingStorage";
import { readSettings, writeSettings, type SourceSettings } from "./data/source";
import { useRecord } from "./data/useRecord";
import { launchOptions } from "./data/tauri";

const PAGES: Page[] = ["overview", "incidents", "timeline", "host", "rules", "evidence", "diagnostics", "settings"];

function Shell() {
  const { dir, setLang } = useLanguage();
  const [page, setPage] = useState<Page>("overview");
  const [onboardingComplete, setOnboardingComplete] = useState<boolean>(readOnboardingComplete);
  const [settings, setSettingsState] = useState<SourceSettings>(readSettings);
  const [launched, setLaunched] = useState(false);

  // Command-line options (desktop shell only) override the saved settings
  // for this run: a shortcut or another program can open the viewer on a
  // specific source, page or language without changing what the user chose.
  useEffect(() => {
    let cancelled = false;
    launchOptions().then((o) => {
      if (cancelled) return;
      if (o.source || o.endpoint !== null || o.bundle) {
        setSettingsState((s) => ({
          ...s,
          kind: o.source ?? s.kind,
          endpoint: o.endpoint ?? s.endpoint,
          bundlePath: o.bundle ?? s.bundlePath,
        }));
      }
      if (o.page && (PAGES as string[]).includes(o.page)) setPage(o.page as Page);
      if (o.lang) setLang(o.lang);
      if (o.no_wizard) setOnboardingComplete(true);
      setLaunched(true);
    });
    return () => {
      cancelled = true;
    };
    // runs once: the options do not change while the app is open
  }, []);
  // The source in use before a bundle was opened, so closing the bundle
  // returns to it rather than to a hard-coded default.
  const [previousKind, setPreviousKind] = useState<SourceSettings["kind"]>("demo");

  useEffect(() => {
    document.documentElement.dir = dir;
    document.documentElement.lang = dir === "rtl" ? "ar" : "en";
  }, [dir]);

  const setSettings = (next: SourceSettings) => {
    setSettingsState(next);
    writeSettings(next);
  };

  // Every page takes plain events/incidents (plus the record's metadata)
  // and does not know or care whether they came from the demo recording,
  // a live recorder over IPC, or a verified bundle file.
  const record = useRecord(settings);

  if (!launched) return null; // one frame, until the launch options are known

  const openBundle = (path: string) => {
    if (settings.kind !== "bundle") setPreviousKind(settings.kind);
    setSettings({ ...settings, kind: "bundle", bundlePath: path });
    setPage("evidence");
  };
  const closeBundle = () => setSettings({ ...settings, kind: previousKind === "bundle" ? "demo" : previousKind });
  const switchToDemo = () => setSettings({ ...settings, kind: "demo" });

  if (!onboardingComplete) {
    return (
      <Wizard
        settings={settings}
        onChangeSettings={setSettings}
        record={record}
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
        <SourceBanner settings={settings} record={record} onSwitchToDemo={switchToDemo} />
        {page === "overview" && <Overview record={record} settings={settings} />}
        {page === "incidents" && <Incidents incidents={record.incidents} />}
        {page === "timeline" && <Timeline events={record.events} />}
        {page === "host" && <Host events={record.events} />}
        {page === "rules" && <Rules incidents={record.incidents} rules={record.rules} />}
        {page === "evidence" && (
          <Evidence record={record} settings={settings} onOpenBundle={openBundle} onCloseBundle={closeBundle} />
        )}
        {page === "settings" && (
          <Settings
            settings={settings}
            onChange={setSettings}
            onReopenWizard={() => {
              writeOnboardingComplete(false);
              setOnboardingComplete(false);
            }}
          />
        )}
        {page === "diagnostics" && (
          <Diagnostics events={record.events} incidents={record.incidents} settings={settings} record={record} />
        )}
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
