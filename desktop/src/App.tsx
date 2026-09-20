import { useEffect, useState } from "react";
import "./assets/fonts/fonts.css";
import "./App.css";
import { LanguageProvider, useLanguage } from "./i18n/LanguageContext";
import { AiSessionProvider } from "./data/aiSession";
import { Sidebar, type Page } from "./components/Sidebar";
import { useRoute } from "./routing/useRoute";
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
import { readAiSettings, writeAiSettings, type AiSettings } from "./data/aiSettings";
import { useRecord } from "./data/useRecord";
import { launchOptions } from "./data/tauri";

const PAGES: Page[] = ["overview", "incidents", "timeline", "host", "rules", "evidence", "diagnostics", "settings"];

function Shell() {
  const { dir, setLang, t } = useLanguage();
  // ADR (routing): #/<page>?<InvestigationContext fields>, real browser
  // history and deep links instead of local component state - the router
  // itself (desktop/src/routing/useRoute.ts) predates any page actually
  // reading InvestigationContext fields, which lands with each page's own
  // Phase 3/5 rebuild; today it carries page navigation only.
  const { route, navigate } = useRoute();
  const [onboardingComplete, setOnboardingComplete] = useState<boolean>(readOnboardingComplete);
  const [settings, setSettingsState] = useState<SourceSettings>(readSettings);
  const [aiSettings, setAiSettingsState] = useState<AiSettings>(readAiSettings);
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
      if (o.page && (PAGES as string[]).includes(o.page)) navigate(o.page as Page);
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
  const setAiSettings = (next: AiSettings) => {
    setAiSettingsState(next);
    writeAiSettings(next);
  };

  // Every page takes plain events/incidents (plus the record's metadata)
  // and does not know or care whether they came from the demo recording,
  // a live recorder over IPC, or a verified bundle file.
  const record = useRecord(settings);

  // One frame, until the launch options are known - a real shell outline
  // instead of a blank window (§4.1: "first paint is never blank"). Not a
  // fully designed loading skeleton (that component lands with the rest of
  // the component library) - just the frame shape, so the window never
  // looks broken or unresponsive for the single frame this actually takes.
  if (!launched) {
    return (
      <div className="app-frame">
        <div className="sidebar">
          <div className="sidebar-header">
            <div className="app-name">{t("appName")}</div>
          </div>
          <div className="sidebar-nav" tabIndex={0} />
          <div className="sidebar-footer" />
        </div>
        <div className="workspace">
          <div className="workspace-header" />
          <div className="workspace-body" tabIndex={0} />
        </div>
      </div>
    );
  }

  const openBundle = (path: string) => {
    if (settings.kind !== "bundle") setPreviousKind(settings.kind);
    setSettings({ ...settings, kind: "bundle", bundlePath: path });
    navigate("evidence");
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
    // ADR 0003: .app-frame bounds the whole shell to the viewport
    // (block-size: 100dvh; overflow: hidden) instead of only setting a
    // floor as the old .app-shell did. .workspace-body is the one normal
    // vertical scroll region in the main window - the sidebar scrolls
    // independently inside itself (Sidebar.tsx) and never needs to.
    <div className="app-frame">
      <Sidebar page={route.page} onNavigate={navigate} />
      <div className="workspace">
        <div className="workspace-header">
          <SourceBanner settings={settings} record={record} onSwitchToDemo={switchToDemo} />
        </div>
        {/* tabIndex=0: a keyboard-only user has no other way to scroll this
            region (axe `scrollable-region-focusable`, found while auditing
            the wizard's equivalent region - same fix applies here). */}
        <main className="workspace-body" tabIndex={0}>
          {route.page === "overview" && <Overview record={record} settings={settings} />}
          {route.page === "incidents" && (
            <Incidents
              incidents={record.incidents}
              rules={record.rules}
              events={record.events}
              context={route.context}
              navigate={navigate}
            />
          )}
          {route.page === "timeline" && <Timeline events={record.events} />}
          {route.page === "host" && <Host events={record.events} />}
          {route.page === "rules" && (
            <Rules
              incidents={record.incidents}
              rules={record.rules}
              context={route.context}
              navigate={navigate}
            />
          )}
          {route.page === "evidence" && (
            <Evidence
              record={record}
              settings={settings}
              context={route.context}
              onOpenBundle={openBundle}
              onCloseBundle={closeBundle}
            />
          )}
          {route.page === "settings" && (
            <Settings
              settings={settings}
              onChange={setSettings}
              aiSettings={aiSettings}
              onChangeAiSettings={setAiSettings}
              onReopenWizard={() => {
                writeOnboardingComplete(false);
                setOnboardingComplete(false);
              }}
            />
          )}
          {route.page === "diagnostics" && (
            <Diagnostics events={record.events} incidents={record.incidents} settings={settings} record={record} />
          )}
        </main>
      </div>
    </div>
  );
}

export default function App() {
  return (
    <LanguageProvider>
      <AiSessionProvider>
        <Shell />
      </AiSessionProvider>
    </LanguageProvider>
  );
}
