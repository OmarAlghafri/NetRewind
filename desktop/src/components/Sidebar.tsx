import { useLanguage } from "../i18n/LanguageContext";
import type { DictKey } from "../i18n/translations";

export type Page = "overview" | "incidents" | "timeline" | "host" | "rules" | "evidence" | "diagnostics" | "settings";

const ITEMS: { page: Page; key: DictKey }[] = [
  { page: "overview", key: "nav_overview" },
  { page: "incidents", key: "nav_incidents" },
  { page: "timeline", key: "nav_timeline" },
  { page: "host", key: "nav_host" },
  { page: "rules", key: "nav_rules" },
  { page: "evidence", key: "nav_evidence" },
  { page: "settings", key: "nav_settings" },
  { page: "diagnostics", key: "nav_diagnostics" },
];

export function Sidebar({ page, onNavigate }: { page: Page; onNavigate: (p: Page) => void }) {
  const { t, lang, setLang } = useLanguage();
  return (
    // ADR 0003: three independent grid rows instead of one flex column -
    // header and footer (.lang-toggle) are always visible; only .sidebar-nav
    // scrolls, and only if it ever has more items than fit (it does not,
    // today, but the structure no longer depends on that staying true).
    // Nav items are native <button>s now, not `<div role="button">` with
    // hand-rolled Enter/Space handling - real focus and activation
    // semantics, for free.
    <div className="sidebar">
      <div className="sidebar-header">
        <div className="app-name">{t("appName")}</div>
      </div>
      <nav className="sidebar-nav" aria-label={t("nav_landmark_label")}>
        {ITEMS.map((item) => (
          <button
            key={item.page}
            type="button"
            className={`nav-item${page === item.page ? " active" : ""}`}
            onClick={() => onNavigate(item.page)}
            aria-current={page === item.page ? "page" : undefined}
          >
            {t(item.key)}
          </button>
        ))}
      </nav>
      <div className="sidebar-footer">
        <div className="lang-toggle">
          <button className={lang === "ar" ? "active" : ""} onClick={() => setLang("ar")}>
            العربية
          </button>
          <button className={lang === "en" ? "active" : ""} onClick={() => setLang("en")}>
            English
          </button>
        </div>
      </div>
    </div>
  );
}
