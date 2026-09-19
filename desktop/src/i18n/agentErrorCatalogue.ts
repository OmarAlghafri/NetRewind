import type { Lang } from "./translations";

/**
 * Mirrors desktop/src-tauri/src/agent.rs's `AgentError` exactly - a
 * connection/transport failure the Rust side reports as a code plus
 * parameters instead of a hard-coded English sentence.
 *
 * ADR 0004 / execution order §4.5: "Rust connection errors ... become
 * {code, params, technical_detail}; the GUI shows a translated message
 * with the raw detail available on demand." `messageFor` below is that
 * translation; `technical_detail` (the original English sentence,
 * verbatim, including whatever hyper or the OS said) is what "available
 * on demand" means - shown separately, in `TechnicalValue`, not
 * discarded.
 */
export interface AgentErrorPayload {
  code: string;
  params: Record<string, string>;
  technical_detail: string;
}

interface Template {
  en: string;
  ar: string;
}

/**
 * `{param}` is replaced from `AgentErrorPayload.params`. Every `code`
 * `agent.rs`'s `AgentError::new` constructs must have an entry here -
 * `agentErrorCatalogue.test.ts` checks this list against the same literal
 * set of codes a Rust-side test in `desktop/src-tauri/src/agent.rs`
 * extracts from that file's own source text. This is a lighter guarantee
 * than the AST-based generators for kinds.json/rules.json (a hand-
 * maintained list on each side, not a single generated source of truth) -
 * proportionate to a small, stable set of connection-error shapes defined
 * in one function, unlike the 49 kinds or 19+ rules those generators
 * track, but still real: both lists have to be edited for a new code to
 * pass either test, which is the actual protection against one language
 * changing without the other noticing.
 */
const TEMPLATES: Record<string, Template> = {
  timeout: {
    en: "The recorder at {endpoint} did not answer within {seconds}s.",
    ar: "لم يستجب المُسجِّل عند {endpoint} خلال {seconds} ثانية.",
  },
  handshake_failed: {
    en: "The connection to the recorder failed during the initial handshake.",
    ar: "فشل الاتصال بالمُسجِّل أثناء المصافحة الأولية.",
  },
  request_build_failed: {
    en: "Could not build the request to the recorder.",
    ar: "تعذّر إنشاء الطلب الموجّه إلى المُسجِّل.",
  },
  request_failed: {
    en: "The request to the recorder failed.",
    ar: "فشل الطلب الموجّه إلى المُسجِّل.",
  },
  response_read_failed: {
    en: "Reading the recorder's answer failed.",
    ar: "تعذّرت قراءة إجابة المُسجِّل.",
  },
  not_listening: {
    en: "No recorder is listening at {endpoint}. Is the netrewindd service running?",
    ar: "لا يوجد مُسجِّل يستمع عند {endpoint}. هل خدمة netrewindd تعمل؟",
  },
  access_denied_windows: {
    en: "The recorder at {endpoint} refused this user. Add this account to api.allow_users in netrewindd.yaml.",
    ar: "رفض المُسجِّل عند {endpoint} هذا المستخدم. أضِف هذا الحساب إلى api.allow_users في netrewindd.yaml.",
  },
  access_denied_unix: {
    en: "The recorder at {endpoint} refused this user. Add this user to the group named by api.group in netrewindd.yaml.",
    ar: "رفض المُسجِّل عند {endpoint} هذا المستخدم. أضِف هذا المستخدم إلى المجموعة المحددة في api.group ضمن netrewindd.yaml.",
  },
  open_failed: {
    en: "Could not open {endpoint}.",
    ar: "تعذّر فتح {endpoint}.",
  },
  pipe_busy: {
    en: "The recorder at {endpoint} stayed busy.",
    ar: "بقي المُسجِّل عند {endpoint} مشغولاً.",
  },
  invalid_path: {
    en: "This request path is not allowed.",
    ar: "مسار الطلب هذا غير مسموح به.",
  },
  cancelled: {
    en: "The request was cancelled.",
    ar: "أُلغي الطلب.",
  },
};

function interpolate(template: string, params: Record<string, string>): string {
  return template.replace(/\{(\w+)\}/g, (match, key: string) => params[key] ?? match);
}

export interface AgentErrorLookup {
  message: string;
  known: boolean;
}

/**
 * `known: false` (an unrecognised code - an older frontend build talking
 * to a newer agent.rs) falls back to `technical_detail` itself as the
 * message, tagged not-a-translation by the caller - never a crash, never
 * blank text, matching every other fallback in this ADR.
 */
export function messageFor(error: AgentErrorPayload, lang: Lang): AgentErrorLookup {
  const template = TEMPLATES[error.code];
  if (!template) return { message: error.technical_detail, known: false };
  return { message: interpolate(template[lang], error.params), known: true };
}
