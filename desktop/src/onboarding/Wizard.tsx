import { useState } from "react";
import { useLanguage } from "../i18n/LanguageContext";
import type { SourceSettings } from "../data/source";
import type { Record } from "../data/useRecord";
import { LanguageStep } from "./steps/LanguageStep";
import { PrivacyStep } from "./steps/PrivacyStep";
import { AgentChoiceStep } from "./steps/AgentChoiceStep";
import { PermissionsStep } from "./steps/PermissionsStep";
import { CapabilityStep } from "./steps/CapabilityStep";
import { SampleInvestigationStep } from "./steps/SampleInvestigationStep";

// PRODUCT_RELEASE_PLAN_AR.md §5, Phase 2: "wizard أول تشغيل: اختيار اللغة،
// بيان الخصوصية، اختيار agent/import، فحص الصلاحيات والقرص، فحص
// capabilities، ثم sample investigation قابل للتشغيل بلا شبكة." - the six
// steps below, in that exact order. Shown by App.tsx before the normal
// eight-page shell until finished or explicitly skipped (both call
// onFinish - skipping is a real, honest choice, not a hidden default).
const STEPS = ["language", "privacy", "agent", "permissions", "capability", "sample"] as const;
type StepId = (typeof STEPS)[number];

export function Wizard({
  settings,
  onChangeSettings,
  record,
  onFinish,
}: {
  settings: SourceSettings;
  onChangeSettings: (next: SourceSettings) => void;
  record: Record;
  onFinish: () => void;
}) {
  const { t } = useLanguage();
  const [index, setIndex] = useState(0);
  const stepId: StepId = STEPS[index];
  const isLast = index === STEPS.length - 1;

  const next = () => setIndex((i) => Math.min(i + 1, STEPS.length - 1));
  const back = () => setIndex((i) => Math.max(i - 1, 0));

  return (
    <main className="wizard-overlay">
      <div className="wizard-card">
        <div className="wizard-progress">
          {STEPS.map((s, i) => (
            <span key={s} className={`wizard-dot${i === index ? " active" : ""}${i < index ? " done" : ""}`} />
          ))}
        </div>

        {/* The only region that scrolls inside the card: pins wizard-progress
            above and wizard-actions (back/next/skip) below at any window
            height, instead of requiring a scroll of the whole card to reach
            them - the same containment pattern as the app shell (ADR 0003),
            applied here because the privacy step's body text alone
            overflows a 720x480 window without it. */}
        <div className="wizard-step-body">
          {stepId === "language" && <LanguageStep />}
          {stepId === "privacy" && <PrivacyStep />}
          {stepId === "agent" && <AgentChoiceStep settings={settings} onChange={onChangeSettings} />}
          {stepId === "permissions" && <PermissionsStep settings={settings} record={record} />}
          {stepId === "capability" && <CapabilityStep record={record} />}
          {stepId === "sample" && <SampleInvestigationStep incidents={record.incidents} />}
        </div>

        <div className="wizard-actions">
          <button className="wizard-btn wizard-btn-ghost" onClick={onFinish}>
            {t("wizard_skip")}
          </button>
          <div className="wizard-nav-buttons">
            {index > 0 && (
              <button className="wizard-btn" onClick={back}>
                {t("wizard_back")}
              </button>
            )}
            {!isLast && (
              <button className="wizard-btn wizard-btn-primary" onClick={next}>
                {t("wizard_next")}
              </button>
            )}
            {isLast && (
              <button className="wizard-btn wizard-btn-primary" onClick={onFinish}>
                {t("wizard_finish")}
              </button>
            )}
          </div>
        </div>
      </div>
    </main>
  );
}
