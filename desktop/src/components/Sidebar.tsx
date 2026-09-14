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
    <nav className="sidebar">
      <div className="app-name">{t("appName")}</div>
      {ITEMS.map((item) => (
        <div
          key={item.page}
          className={`nav-item${page === item.page ? " active" : ""}`}
          onClick={() => onNavigate(item.page)}
          role="button"
          tabIndex={0}
          aria-current={page === item.page ? "page" : undefined}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault(); // Space otherwise scrolls the page, same as a native button suppresses it
              onNavigate(item.page);
            }
          }}
        >
          {t(item.key)}
        </div>
      ))}
      <div className="lang-toggle">
        <button className={lang === "ar" ? "active" : ""} onClick={() => setLang("ar")}>
          العربية
        </button>
        <button className={lang === "en" ? "active" : ""} onClick={() => setLang("en")}>
          English
        </button>
      </div>
    </nav>
  );
}
