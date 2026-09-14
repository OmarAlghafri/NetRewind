import { useLanguage } from "../i18n/LanguageContext";
import type { Capability } from "../data/types";

// The capability matrix the product plan requires instead of a bare
// "supported" claim: every collector the recorder knows about, whether it
// is watching right now, and - when it is not - the reason in the
// recorder's own words. "Unsupported" is a distinct state from "down": one
// is this platform, the other is a failure.
export function CapabilityTable({ capabilities }: { capabilities: Capability[] }) {
  const { t } = useLanguage();
  const status = (c: Capability) => {
    switch (c.status) {
      case "up":
        return { cls: "status-up", label: t("capability_up") };
      case "down":
        return { cls: "status-down", label: t("capability_down") };
      case "unsupported":
        return { cls: "status-unknown", label: t("capability_unsupported") };
      default:
        return { cls: "status-unknown", label: t("capability_unknown") };
    }
  };
  // What is watching comes first, then what failed, then what this
  // platform cannot run at all: the reader wants the working set before the
  // reasons for the rest.
  const rank: { [s in Capability["status"]]: number } = { up: 0, down: 1, unknown: 2, unsupported: 3 };
  const ordered = [...capabilities].sort((a, b) => rank[a.status] - rank[b.status] || a.name.localeCompare(b.name));
  return (
    <div>
      {ordered.map((c) => {
        const s = status(c);
        return (
          <div className="capability-row capability-row-detail" key={c.name}>
            <div className="capability-main">
              <span className="ltr-field capability-name">{c.name}</span>
              <span className="capability-status">
                <span className={`status-dot ${s.cls}`} />
                {s.label}
                {c.reason && c.status !== "unsupported" && (
                  <span className="ltr-field capability-reason"> — {c.reason}</span>
                )}
              </span>
            </div>
            <div className="capability-detail">
              <span>
                {t("capability_platform")}: <span className="ltr-field">{c.platform}</span>
              </span>
              <span>
                {t("capability_needs")}: <span className="ltr-field">{c.privilege}</span>
              </span>
              <span>
                {t("capability_covers")}: <span className="ltr-field">{c.coverage.join(", ")}</span>
              </span>
            </div>
          </div>
        );
      })}
    </div>
  );
}
