import { useCallback, useEffect, useState } from "react";
import type { Page } from "../components/Sidebar";

/**
 * The shared shape of "what is this investigation about right now" -
 * execution order §4.2. Every field is optional because not every page
 * uses every field, and because a bare "#/incidents" with no context at
 * all is a completely valid route (today, every page - none of them read
 * from this yet; that lands with each page's own Phase 3/5 rebuild, which
 * is what actually gives these fields something to filter). This type is
 * the contract those rebuilds are written against, so it exists before
 * they land rather than growing ad hoc with the first page that needs it.
 */
export interface InvestigationContext {
  from?: string;
  to?: string;
  entity?: string;
  severities?: string[];
  families?: string[];
  query?: string;
  sort?: string;
  selection?: string;
}

export interface Route {
  page: Page;
  context: InvestigationContext;
}

const ARRAY_KEYS = new Set<keyof InvestigationContext>(["severities", "families"]);
const DEFAULT_PAGE: Page = "overview";

function isPage(v: string): v is Page {
  return (
    v === "overview" ||
    v === "incidents" ||
    v === "timeline" ||
    v === "host" ||
    v === "rules" ||
    v === "evidence" ||
    v === "diagnostics" ||
    v === "settings"
  );
}

/** "#/incidents?entity=10.0.0.5&severities=warn,error" -> {page, context}.
 *  An empty, missing, or unrecognized hash is Overview with no context -
 *  never an error state a user could see, since a bad or stale link
 *  should still open the app instead of failing to. */
export function parseRoute(hash: string): Route {
  const raw = hash.replace(/^#\/?/, "");
  const qIndex = raw.indexOf("?");
  const pagePart = qIndex === -1 ? raw : raw.slice(0, qIndex);
  const page = isPage(pagePart) ? pagePart : DEFAULT_PAGE;

  const context: InvestigationContext = {};
  if (qIndex !== -1) {
    const params = new URLSearchParams(raw.slice(qIndex + 1));
    for (const [key, value] of params) {
      if (!value) continue;
      if (ARRAY_KEYS.has(key as keyof InvestigationContext)) {
        (context as Record<string, string[]>)[key] = value.split(",").filter(Boolean);
      } else {
        (context as Record<string, string>)[key] = value;
      }
    }
  }
  return { page, context };
}

export function buildHash(route: Route): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(route.context)) {
    if (value == null) continue;
    if (Array.isArray(value)) {
      if (value.length > 0) params.set(key, value.join(","));
    } else if (value !== "") {
      params.set(key, String(value));
    }
  }
  const qs = params.toString();
  return `#/${route.page}${qs ? `?${qs}` : ""}`;
}

/**
 * The whole router: reads the current route from `location.hash`, updates
 * on back/forward/manual hash edits (`hashchange`), and `navigate` writes
 * a new one - hash-only, no server, no history-API path handling, which
 * is deliberately as small as this project's routing needs actually are
 * today (a Tauri webview loading a single local bundle, not a hosted
 * multi-route site).
 */
export function useRoute(): { route: Route; navigate: (page: Page, context?: InvestigationContext) => void } {
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.hash));

  useEffect(() => {
    const onHashChange = () => setRoute(parseRoute(window.location.hash));
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  const navigate = useCallback((page: Page, context: InvestigationContext = {}) => {
    const next = { page, context };
    // Setting .hash itself (not replaceState) is what fires 'hashchange'
    // and populates real browser back/forward history - both wanted here.
    window.location.hash = buildHash(next);
  }, []);

  return { route, navigate };
}
