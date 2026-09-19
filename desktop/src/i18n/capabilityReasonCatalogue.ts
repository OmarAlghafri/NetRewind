import type { Lang } from "./translations";
import type { Capability } from "../data/types";

/**
 * Mirrors internal/registry.Snapshot's ReasonCode/ReasonParams - the other
 * half of ADR 0004 §4.5's "{code, params, technical_detail}" besides
 * agentErrorCatalogue.ts's connection errors. `reason` itself (the plain
 * English sentence, e.g. "requires linux" or "stopped") is this contract's
 * `technical_detail`: always present, always what the recorder actually
 * said, kept available even when a translated `messageFor` result is shown
 * in its place.
 */
interface Template {
  en: string;
  ar: string;
}

/**
 * Every code `internal/registry.go`'s DownCoded/UnsupportedCoded call
 * sites use must have an entry here - `capabilityReasonCatalogue.test.ts`
 * checks this list against the same literal set a Go test in
 * `internal/registry/registry_test.go` asserts `cmd/netrewindd/main.go`'s
 * call sites use. Two hand-kept lists, not a generated cross-language
 * bridge - proportionate to two stable, rarely-changing codes, the same
 * reasoning as agentErrorCatalogue.ts's own note.
 */
const TEMPLATES: Record<string, Template> = {
  requires_platform: {
    en: "This collector requires {platform}.",
    ar: "تتطلب وحدة جمع البيانات هذه نظام {platform}.",
  },
  collector_stopped: {
    en: "This collector stopped.",
    ar: "توقفت وحدة جمع البيانات هذه.",
  },
};

function interpolate(template: string, params: Record<string, string>): string {
  return template.replace(/\{(\w+)\}/g, (match, key: string) => params[key] ?? match);
}

export interface ReasonLookup {
  message: string;
  known: boolean;
}

/**
 * `known: false` (no code, or one this build does not recognise - an
 * older/newer recorder) falls back to `reason` itself, exactly the ADR's
 * "unknown or newer key falls back to the original, never blank" rule.
 */
export function reasonMessageFor(cap: Pick<Capability, "reason" | "reason_code" | "reason_params">, lang: Lang): ReasonLookup {
  const fallback = cap.reason ?? "";
  if (!cap.reason_code) return { message: fallback, known: false };
  const template = TEMPLATES[cap.reason_code];
  if (!template) return { message: fallback, known: false };
  return { message: interpolate(template[lang], cap.reason_params ?? {}), known: true };
}
