import { useLanguage } from "../i18n/LanguageContext";
import type { DictKey } from "../i18n/translations";

const SEVERITY_KEYS: Record<string, DictKey> = {
  info: "severity_info",
  notice: "severity_notice",
  warn: "severity_warn",
  error: "severity_error",
};

export function SeverityBadge({ severity }: { severity: string }) {
  const { t } = useLanguage();
  const key = SEVERITY_KEYS[severity];
  return <span className={`severity-badge sev-${severity}`}>{key ? t(key) : severity}</span>;
}
