import { useMemo } from "react";
import { useLanguage } from "../../i18n/LanguageContext";
import { IncidentCard } from "../../components/IncidentCard";
import type { Incident } from "../../types";

// PRODUCT_RELEASE_PLAN_AR.md §5, Phase 2's last wizard step: "sample
// investigation قابل للتشغيل بلا شبكة." Reuses the exact same
// IncidentCard component the real Incidents page renders, against one real
// incident from the demo recording - not a screenshot or a description.
// The incident with the longest causal chain is picked (deterministically,
// first one found) since it is the most informative single example of
// "what happened" for a first look.
export function SampleInvestigationStep({ incidents }: { incidents: Incident[] }) {
  const { t } = useLanguage();

  const sample = useMemo<Incident | undefined>(() => {
    return incidents.reduce<Incident | undefined>((best, i) => {
      if (!best || i.chain.length > best.chain.length) return i;
      return best;
    }, undefined);
  }, [incidents]);

  return (
    <div>
      <h1 className="page-title">{t("wizard_sample_title")}</h1>
      <p className="page-subtitle">{t("wizard_sample_subtitle")}</p>
      {sample ? <IncidentCard incident={sample} /> : <div className="empty-state">{t("wizard_sample_empty")}</div>}
    </div>
  );
}
