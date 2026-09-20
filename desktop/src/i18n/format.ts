import type { Lang } from "./translations";

/**
 * execution order §4.6: "One shared module, used everywhere instead of
 * the five independent toLocaleTimeString call sites." By the time this
 * was written there were actually ten, across eight files, all repeating
 * the identical `lang === "ar" ? "ar-EG" : "en-US"` ternary - and all of
 * them producing Arabic-Indic digits (٢٢:٢٠:٢٢) for a plain "ar-EG"
 * locale, inconsistent with every other number in the app (confidence,
 * counts, IDs), which are always Latin digits via `TechnicalValue`/
 * `ltr-field`. The "-u-nu-latn" Unicode locale extension forces Latin
 * numerals from the same `Intl` call, closing that inconsistency at the
 * source instead of at each render site.
 */
const LOCALE: Record<Lang, string> = { ar: "ar-EG-u-nu-latn", en: "en-US" };

export function formatTime(date: Date, lang: Lang): string {
  return date.toLocaleTimeString(LOCALE[lang], { hour12: false });
}

export function formatDateTime(date: Date, lang: Lang): string {
  return date.toLocaleString(LOCALE[lang], { hour12: false });
}

export function formatCount(n: number, lang: Lang): string {
  return n.toLocaleString(LOCALE[lang]);
}

/** "2.7 GB", base-1000 (matching cmd/netrewind/ai_model.go's own
 *  formatBytes - what a model catalogue download size is quoted in
 *  everywhere else in this project, and how every model host/browser
 *  already reports a download size, unlike a filesystem's base-1024). */
export function formatBytes(n: number, lang: Lang): string {
  const units = ["B", "kB", "MB", "GB", "TB", "PB"];
  if (n < 1000) return `${formatCount(n, lang)} ${units[0]}`;
  let div = 1000;
  let exp = 0;
  for (let v = n / 1000; v >= 1000; v /= 1000) {
    div *= 1000;
    exp++;
  }
  return `${(n / div).toLocaleString(LOCALE[lang], { minimumFractionDigits: 1, maximumFractionDigits: 1 })} ${units[exp + 1]}`;
}

const DURATION_UNITS: Record<Lang, { h: [string, string]; m: [string, string]; s: [string, string] }> = {
  // [compact suffix, detailed word] - detailed word is used bare (no
  // dual/plural inflection beyond this), matching how this project
  // already accepts loose number/noun agreement elsewhere (rules_fired_count,
  // sources_in_recording) rather than a full Arabic numeral-noun grammar
  // engine for a duration display.
  ar: { h: ["س", "ساعات"], m: ["د", "دقيقة"], s: ["ث", "ثانية"] },
  en: { h: ["h", "hours"], m: ["m", "minutes"], s: ["s", "seconds"] },
};

/**
 * `style: "compact"` -> «4 س 49 د» / "4h 49m" (inline, next to a label).
 * `style: "detailed"` -> «4 ساعات و49 دقيقة» / "4 hours and 49 minutes"
 * (a standalone sentence). Both drop a zero-valued larger unit rather
 * than show "0 س" - the smallest non-zero unit stands alone (`formatDuration(45, ...)`
 * -> «45 ث», never «0 د 45 ث»).
 */
export function formatDuration(totalSeconds: number, lang: Lang, style: "compact" | "detailed" = "compact"): string {
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.floor((totalSeconds % 3600) / 60);
  const s = Math.floor(totalSeconds % 60);
  const u = DURATION_UNITS[lang];

  const parts: [number, [string, string]][] =
    h > 0 ? [[h, u.h], [m, u.m]] : m > 0 ? [[m, u.m], [s, u.s]] : [[s, u.s]];

  if (style === "compact") {
    return parts.map(([n, [suffix]]) => `${formatCount(n, lang)}${suffix}`).join(" ");
  }
  const words = parts.map(([n, [, word]]) => `${formatCount(n, lang)} ${word}`);
  // Arabic "و" (and) is a prefix glued to the next word with no space of
  // its own - "4 ساعات و49 دقيقة", not "4 ساعات و 49 دقيقة".
  return lang === "ar" ? words.join(" و") : words.join(" and ");
}
