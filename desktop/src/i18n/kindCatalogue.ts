import type { Lang } from "./translations";

/**
 * Human names for every event kind and family, in both languages, with the
 * technical code (e.g. `l2.arp_binding_changed`) meant to stay visible
 * alongside it via `TechnicalValue` - never replaced by it.
 *
 * ADR 0004: "A generated event-kind catalogue mirrors internal/event/kinds.go's
 * 49 kinds ... into a GUI-side table giving each kind a human name in both
 * languages with the technical code shown beneath - generated, not
 * hand-maintained, so a new Go kind cannot silently ship without a matching
 * GUI entry." The content here is hand-written (a translation is not
 * mechanically derivable from a Go identifier); what is generated is
 * `./generated/kinds.json`, the authoritative key set this file is checked
 * against by `kindCatalogue.test.ts` and, on the Go side, by
 * `internal/event/kind_catalogue_test.go`.
 *
 * English wording follows docs/schema.md's own "What it means" column.
 * Arabic wording reuses this project's already-reviewed rule translations
 * (rules/*.yaml `i18n.ar`) wherever the same concept appears there, so the
 * same fact reads the same way whether an operator meets it in a rule's
 * `why` or in a raw event kind: المسار (route), العنوان (address), عنوان
 * العتاد (MAC), البوابة (gateway), الواجهة (interface), المُحلِّل (DNS
 * resolver), وحدة جمع البيانات (collector - not «جامعة», see the execution
 * order's glossary). "Down" follows the same glossary's `متعطّل`.
 */
export interface KindLabel {
  en: string;
  ar: string;
}

export const kindLabels: Record<string, KindLabel> = {
  // link.* - layer 1
  "link.up": { en: "Link up", ar: "الوصلة متصلة" },
  "link.down": { en: "Link down", ar: "الوصلة منقطعة" },
  "link.flap": { en: "Link flapping", ar: "تذبذب الوصلة" },
  "link.mtu_changed": { en: "MTU changed", ar: "تغيّر MTU" },
  "link.error_rate_high": { en: "High error rate", ar: "ارتفاع معدل الأخطاء" },

  // l2.* - layer 2
  "l2.arp_binding_new": { en: "New address binding", ar: "ارتباط عنوان جديد" },
  "l2.arp_binding_changed": { en: "Address binding changed", ar: "تغيّر ارتباط العنوان" },
  "l2.mac_moved": { en: "Hardware address moved", ar: "انتقال عنوان العتاد" },
  "l2.duplicate_ip": { en: "Duplicate address", ar: "تعارض العناوين" },
  "l2.neighbor_failed": { en: "Neighbor stopped answering", ar: "توقف الجار عن الإجابة" },
  "l2.lldp_neighbor_changed": { en: "LLDP neighbor changed", ar: "تغيّر جار LLDP" },
  "l2.vlan_seen": { en: "VLAN seen", ar: "رصد VLAN" },

  // l3.* - layer 3
  "l3.route_added": { en: "Route added", ar: "إضافة مسار" },
  "l3.route_removed": { en: "Route removed", ar: "إزالة مسار" },
  "l3.route_changed": { en: "Route changed", ar: "تغيّر مسار" },
  "l3.default_route_changed": { en: "Default route changed", ar: "تغيّر المسار الافتراضي" },
  "l3.addr_added": { en: "Address added", ar: "إضافة عنوان" },
  "l3.addr_removed": { en: "Address removed", ar: "إزالة عنوان" },
  "l3.icmp_unreachable": { en: "Destination stopped answering", ar: "توقفت الوجهة عن الإجابة" },
  "l3.mtu_blackhole": { en: "MTU black hole", ar: "الحفرة السوداء لـ MTU" },

  // flow.* - layer 4
  "flow.reset": { en: "Connection reset", ar: "إعادة ضبط الاتصال" },
  "flow.timeout_no_close": { en: "Connection timed out", ar: "انتهاء مهلة الاتصال" },
  "flow.retransmit_spike": { en: "Retransmit spike", ar: "ارتفاع إعادة الإرسال" },
  "flow.handshake_fail": { en: "Handshake failed", ar: "فشل المصافحة" },
  "flow.first_failure_for_pair": { en: "First failure between hosts", ar: "أول فشل بين جهازين" },
  "flow.rollup": { en: "Connection activity summary", ar: "ملخص نشاط الاتصالات" },

  // dhcp.*
  "dhcp.offer": { en: "DHCP offer", ar: "عرض DHCP" },
  "dhcp.ack": { en: "Lease granted", ar: "منح عقد DHCP" },
  "dhcp.nak": { en: "Lease refused", ar: "رفض عقد DHCP" },
  "dhcp.server_seen": { en: "New DHCP server seen", ar: "رصد خادم DHCP جديد" },
  "dhcp.lease_changed": { en: "Lease changed", ar: "تغيّر عقد DHCP" },

  // dns.*
  "dns.query_fail": { en: "DNS query failed", ar: "فشل استعلام DNS" },
  "dns.resolver_changed": { en: "Resolver changed", ar: "تغيّر المُحلِّل" },
  "dns.latency_spike": { en: "DNS latency spike", ar: "ارتفاع كمون DNS" },

  // policy.*
  "policy.drop_burst": { en: "Burst of dropped packets", ar: "دفعة من الرزم المُسقَطة" },
  "policy.rule_changed": { en: "Filtering rules changed", ar: "تغيّرت قواعد الترشيح" },

  // metric.*
  "metric.anomaly": { en: "Metric anomaly", ar: "شذوذ في المقياس" },

  // change.*
  "change.config_applied": { en: "Configuration applied", ar: "تطبيق إعداد" },
  "change.device_reboot": { en: "Device rebooted", ar: "إعادة تشغيل الجهاز" },
  "change.admin_action": { en: "Administrative action", ar: "إجراء إداري" },

  // system.*
  "system.gap": { en: "Recording gap", ar: "فراغ في التسجيل" },
  "system.drop": { en: "Events dropped", ar: "فقدان أحداث" },
  "system.clock_step": { en: "Clock jumped", ar: "قفزة في الساعة" },
  "system.start": { en: "Recorder started", ar: "بدء تشغيل المُسجِّل" },
  "system.stop": { en: "Recorder stopped", ar: "إيقاف المُسجِّل" },
  "system.collector_down": { en: "Collector down", ar: "تعطّل وحدة جمع بيانات" },
  "system.update_available": { en: "Update available", ar: "يتوفر تحديث" },
  "system.updated": { en: "Recorder updated", ar: "تحديث المُسجِّل" },
  "system.update_failed": { en: "Update failed", ar: "فشل التحديث" },
};

export const familyLabels: Record<string, KindLabel> = {
  link: { en: "Link", ar: "الوصلة" },
  l2: { en: "Layer 2", ar: "الطبقة الثانية" },
  l3: { en: "Layer 3", ar: "الطبقة الثالثة" },
  flow: { en: "Connections", ar: "الاتصالات" },
  dhcp: { en: "DHCP", ar: "DHCP" },
  dns: { en: "DNS", ar: "DNS" },
  policy: { en: "Filtering policy", ar: "سياسة الترشيح" },
  metric: { en: "Metrics", ar: "المقاييس" },
  change: { en: "Changes", ar: "التغييرات" },
  system: { en: "System", ar: "النظام" },
};

/**
 * A lookup result always has a name to render - `known: false` is the ADR's
 * "unknown or newer localization key falls back to the English original"
 * path (a kind this GUI build predates), which a caller renders visibly
 * flagged rather than crashing or going blank. It never happens for a kind
 * this build actually knows, which `kindCatalogue.test.ts` proves by
 * checking every generated kind against this table.
 */
export interface KindLookup {
  name: string;
  known: boolean;
}

export function labelForKind(kind: string, lang: Lang): KindLookup {
  const entry = kindLabels[kind];
  return entry ? { name: entry[lang], known: true } : { name: kind, known: false };
}

export function labelForFamily(family: string, lang: Lang): KindLookup {
  const entry = familyLabels[family];
  return entry ? { name: entry[lang], known: true } : { name: family, known: false };
}
