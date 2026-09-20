import type { Lang } from "./translations";

/**
 * Mirrors desktop/src-tauri/src/ai/codes.rs's `ALL` exactly - every error
 * code the local-AI subsystem's Tauri commands (ai_status, ai_runtime_start,
 * ai_runtime_stop, ai_analyze) can fail with, translated the same way
 * agentErrorCatalogue.ts translates agent.rs's connection errors: a closed
 * code plus optional params, never a hard-coded English sentence baked into
 * the Rust side.
 */
export interface AiErrorPayload {
  code: string;
  message: string;
}

interface Template {
  en: string;
  ar: string;
}

/**
 * Every code in ai::codes::ALL must have an entry here -
 * aiStatusCatalogue.test.ts checks this list against the same literal set
 * of 27 codes a Rust-side test in codes.rs pins by count, the same
 * hand-kept-in-sync guarantee agentErrorCatalogue.ts already relies on.
 */
const TEMPLATES: Record<string, Template> = {
  runtime_missing: {
    en: "The local AI runtime is not installed.",
    ar: "وقت تشغيل الذكاء الاصطناعي المحلي غير مثبَّت.",
  },
  runtime_hash_mismatch: {
    en: "The local AI runtime does not match its expected checksum and cannot be trusted.",
    ar: "وقت تشغيل الذكاء الاصطناعي المحلي لا يطابق المجموع الاختباري المتوقع، ولا يمكن الوثوق به.",
  },
  runtime_not_executable: {
    en: "The local AI runtime is not executable on this system.",
    ar: "وقت تشغيل الذكاء الاصطناعي المحلي غير قابل للتشغيل على هذا النظام.",
  },
  runtime_incompatible_glibc: {
    en: "This system's C library is too old for the local AI runtime.",
    ar: "مكتبة C في هذا النظام قديمة جدًا بالنسبة لوقت تشغيل الذكاء الاصطناعي المحلي.",
  },
  runtime_spawn_failed: {
    en: "The local AI runtime could not be started.",
    ar: "تعذّر تشغيل وقت تشغيل الذكاء الاصطناعي المحلي.",
  },
  runtime_port_unavailable: {
    en: "No local port was available for the AI runtime.",
    ar: "لا يوجد منفذ محلي متاح لوقت تشغيل الذكاء الاصطناعي.",
  },
  runtime_not_ready: {
    en: "The local AI runtime did not become ready in time.",
    ar: "لم يصبح وقت تشغيل الذكاء الاصطناعي المحلي جاهزًا في الوقت المحدد.",
  },
  runtime_exited: {
    en: "The local AI runtime stopped unexpectedly.",
    ar: "توقف وقت تشغيل الذكاء الاصطناعي المحلي بشكل غير متوقع.",
  },
  model_missing: {
    en: "The selected model is not downloaded.",
    ar: "النموذج المحدد غير مُنزَّل.",
  },
  model_hash_mismatch: {
    en: "The model file does not match its expected checksum and cannot be trusted.",
    ar: "ملف النموذج لا يطابق المجموع الاختباري المتوقع، ولا يمكن الوثوق به.",
  },
  model_quarantined: {
    en: "This model file failed verification and was quarantined rather than used.",
    ar: "فشل التحقق من ملف النموذج هذا، فتم عزله بدلاً من استخدامه.",
  },
  manifest_fetch_failed: {
    en: "The model catalogue could not be fetched.",
    ar: "تعذّر جلب فهرس النماذج.",
  },
  manifest_signature_invalid: {
    en: "The model catalogue's signature is not valid and was refused.",
    ar: "توقيع فهرس النماذج غير صالح، وتم رفضه.",
  },
  manifest_model_not_found: {
    en: "That model profile is not in the catalogue.",
    ar: "فئة النموذج هذه غير موجودة في الفهرس.",
  },
  download_failed: {
    en: "The model download failed.",
    ar: "فشل تنزيل النموذج.",
  },
  download_cancelled: {
    en: "The model download was cancelled.",
    ar: "أُلغي تنزيل النموذج.",
  },
  preflight_ram: {
    en: "This device does not have enough free memory for this model.",
    ar: "لا تتوفر لدى هذا الجهاز ذاكرة كافية لهذا النموذج.",
  },
  preflight_disk: {
    en: "This device does not have enough free disk space for this model.",
    ar: "لا تتوفر لدى هذا الجهاز مساحة تخزين كافية لهذا النموذج.",
  },
  cli_missing: {
    en: "The local analysis component is not installed with this application.",
    ar: "عنصر التحليل المحلي غير مثبَّت مع هذا التطبيق.",
  },
  cli_version_mismatch: {
    en: "The local analysis component does not match this version of the application.",
    ar: "عنصر التحليل المحلي لا يطابق هذا الإصدار من التطبيق.",
  },
  cli_failed: {
    en: "The local analysis component failed to run.",
    ar: "فشل تشغيل عنصر التحليل المحلي.",
  },
  analysis_timeout: {
    en: "The local model did not answer in time.",
    ar: "لم يجب النموذج المحلي في الوقت المحدد.",
  },
  analysis_cancelled: {
    en: "The analysis was cancelled.",
    ar: "أُلغي التحليل.",
  },
  analysis_invalid: {
    en: "The local model's answer did not pass validation.",
    ar: "لم تجتز إجابة النموذج المحلي عملية التحقق.",
  },
  notes_need_recorder: {
    en: "Notes require the recorder to be reachable.",
    ar: "تتطلب الملاحظات إمكانية الوصول إلى المُسجِّل.",
  },
  notes_write_failed: {
    en: "Saving this note failed.",
    ar: "فشل حفظ هذه الملاحظة.",
  },
  feature_disabled: {
    en: "This feature is not available yet in this build.",
    ar: "هذه الميزة غير متاحة بعد في هذا الإصدار.",
  },
};

export interface AiErrorLookup {
  message: string;
  known: boolean;
}

/**
 * `known: false` (an unrecognised code - an older frontend build talking to
 * a newer Rust/Go build) falls back to the payload's own `message` field
 * (whatever the Rust or Go side already produced), tagged not-a-translation
 * - never a crash, never blank text, matching agentErrorCatalogue's own
 * fallback.
 */
export function messageFor(error: AiErrorPayload, lang: Lang): AiErrorLookup {
  const template = TEMPLATES[error.code];
  if (!template) return { message: error.message, known: false };
  return { message: template[lang], known: true };
}
