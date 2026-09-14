# NetRewind fixed corpus — v1

> يطبّق المرحلة 0 من `docs/PRODUCT_RELEASE_PLAN_AR.md`: "حوّل 14 سيناريو Linux الحالية
> إلى corpus ثابت: event fixture، incident expected، evidence IDs" بالإضافة إلى الحالات
> الإضافية المطلوبة (سجل أعمى، collector متعطل، بيانات ناقصة، حدث خبيث، عطلان متزامنان).
>
> **كل رقم وكل نوع حدث في هذا الملف حقيقي، مأخوذ من تشغيل فعلي معزول لكل سيناريو على حدة**
> (WSL2 `netrewind-lab`، Alpine، kernel `6.6.87.2-microsoft-standard-WSL2`، BTF متاح)، وليس
> منسوخًا من التوثيق. أُعيد إنتاجه بالسكربت الموصوف في §5 لأي مراجعة مستقبلية.

## 1. البنية

كل مجلد `<scenario>/` يحتوي:

- `events.json` / `events.txt` — كل الأحداث المسجَّلة، بصيغة JSON و نص قابل للقراءة.
- `incidents.json` / `incidents.txt` — كل ما استنتجه محرك الربط (Isnad).
- `recorder.log` — سجل تشغيل `netrewindd` نفسه لتلك الجلسة (مفيد لتشخيص أي تباين لاحقًا).

## 2. السيناريوهات الأربعة عشر (كل واحد مُشغَّل بمعزل تام عن البقية)

| السيناريو | ما أُنتج فعليًا (أنواع الأحداث) | قاعدة الربط المُستنتَجة |
|---|---|---|
| `link_flap` | `link.down`, `link.up`, `flow.rollup` | **لا شيء بمفرده** — انظر §3 |
| `arp_change` | `l2.arp_binding_changed`, `flow.rollup` | `address-changed-hands` |
| `gateway_hijack` | `l2.arp_binding_changed` (على البوابة، `severity=error`) | `gateway-hijack` (90%) |
| `duplicate_ip` | `l2.arp_binding_changed`, `l2.duplicate_ip` | `address-changed-hands`, `contested-address` |
| `route_change` | `l3.route_added`, `l3.route_changed` | **لا شيء بمفرده** — انظر §3 |
| `default_route_moved` | `l2.arp_binding_new`, `l3.default_route_changed` | `default-route-moved` |
| `default_route_lost` | `l3.route_removed` | `default-route-lost` |
| `service_unreachable` | `flow.handshake_fail` | `service-unreachable` |
| `normal_traffic` | `flow.rollup` فقط | لا شيء (سيناريو خطّ أساس، لا عطل) |
| `path_broke` | `l2.arp_binding_changed`, `flow.first_failure_for_pair`, `l2.neighbor_failed` | `address-changed-hands`, **`change-broke-a-path`** (السيناريو الرئيسي للمشروع) |
| `policy_broke_a_path` | `policy.rule_changed`, `flow.first_failure_for_pair` | `change-broke-a-path` |
| `rogue_dhcp` | `dhcp.offer`, `dhcp.ack`, `dhcp.server_seen` | `rogue-dhcp-server` (92%) |
| `resolver_change` | `dns.resolver_changed` | `resolver-hijacked` |
| `measured_loss` | `l2.arp_binding_new`, `l2.neighbor_failed`, `l3.icmp_unreachable`, `metric.anomaly` | `reachability-lost` (90%) |

## 3. ملاحظة صادقة: بعض السيناريوهات لا تنتج حادثة بمفردها

`link_flap` و`route_change` لم ينتجا أي حادثة (0 incidents) عند تشغيلهما **بمعزل تام** —
وهذا سلوك صحيح، ليس نقصًا. قواعد الربط (`rules/*.yaml`) تحتاج غالبًا **نتيجة** ملاحظة قريبة
زمنيًا (اتصال فشل، مضيف توقف عن الإجابة) لتبني حادثة حول التغيير كسبب. حين شُغِّل `link_flap`
ضمن التسلسل الكامل (`docs/evidence/04-full-lab-gate.log`) بجانب سيناريوهات أخرى تولّد حركة
اتصال، استُنتجت `link-down-isolated-hosts` بثقة 80%. **هذا فرق مهم يجب أن يعرفه أي مطوّر يبني
اختبار عقد (contract test) على هذا corpus في المرحلة 1:** غياب حادثة لا يعني بالضرورة عطلاً في
القاعدة؛ قد يعني غياب سياق كافٍ للربط، ويجب أن يُختبر كلا الوضعين (منعزل، وضمن سياق) صراحةً لا
افتراض أحدهما.

## 4. الحالات الإضافية المطلوبة في §5 من خطة الإصدار

### `blind_gap` — سجل أعمى

راصد أُوقف عمدًا (`system.stop`)، أُبقي الفراغ 20 ثانية (أكبر من `--gap-threshold 12s`)، ثم
أُعيد تشغيله على نفس القاعدة. النتيجة الفعلية: `system.gap` بمدة صحيحة، **بالإضافة** إلى
`system.collector_down` (لأن هذا التشغيل كان على WSL2 دون تفعيل كل قدرات eBPF بين
إعادة التشغيلتين في هذا السيناريو تحديدًا) وحادثتان منفصلتان: `recorder-was-blind` (100%) و
`collector-not-watching` (100%). راجع `blind_gap/incidents.txt` للنص الكامل.

### `collector_down` — collector متعطل

**تحقّق مباشر لادعاء الخطة نفسها** (§2 من `PRODUCT_RELEASE_PLAN_AR.md`: "بناء
`netrewindd.exe` ممكن لكن التسجيل الحي فيه سيتوقف ويكتب `system.collector_down`"). شُغِّل
`netrewindd.exe` فعليًا على Windows (لا WSL) — النتيجة: 7 رسائل `system.collector_down`
واحدة لكل collector مقيّد بـLinux (`netlink.link/neigh/route/addr`, `wire`, `ebpf.flow`,
`policy.nftables`, `probe.icmp`)، و**7 حوادث منفصلة** بقاعدة `collector-not-watching` بثقة
100% لكل واحد. الادعاء في الخطة **صحيح ومطابق تمامًا لما شوهد فعليًا**.

### `malicious_dns_name` — حدث خبيث في hostname/DNS name

هذا البند احتاج تحقيقًا أعمق من مجرد تشغيل سيناريو مختبر، والنتيجة أدق من التوقّع الأولي:

1. **الحدث `dns.resolver_changed` (الذي ينتجه `resolver_change` وهذا الاختبار) لا يحمل اسم
   الاستعلام إطلاقًا** — تحقّقنا من هذا في `internal/collect/wire/dns.go`: فقط
   `dns.query_fail` و`dns.latency_spike` يستدعيان `attachName()`، وحتى هما لا يفعلان ذلك إلا
   إذا فُعِّل `--record-dns-names` (معطَّل افتراضيًا، خصوصية أولاً). لذلك محاولة حقن
   `<script>alert(1)</script>.lab` كاسم DNS عبر `resolver_change` (الملفات في هذا المجلد)
   لم تصل أصلاً لأي حقل مخزَّن — وهذه نتيجة إيجابية حقيقية، لا نقصًا في الاختبار.
2. **الوصول الفعلي لمسار الكود الذي يخزّن الاسم** (`dns.query_fail`/`latency_spike`) يحتاج
   رسالة DNS من نوع **استجابة** (rcode فاشل)، وأداة المختبر الحالية `lab/inject/main.go`
   (`nrinject dns`) **تبني استعلامات فقط** (`buildDNSQuery`)، لا استجابات — فجوة أداة حقيقية
   يجدر سدّها في عمل المرحلة 1 على أدوات المختبر، لا شيء ندّعي تجاوزه هنا.
3. **الاختبار الصحيح لهذا السطح موجود فعلاً، وأقوى مما كنا سنبنيه يدويًا:**
   `internal/web/escaping_test.go` (`TestHostileStringsFromTheWireAreEscaped`,
   `TestHostileQueryParametersAreEscaped`) يُدخل نفس النوع من الحمولات (`<script>`,
   `" onmouseover=`, إلخ) مباشرة كـ`Attrs`/`Subject.Label` ويتحقق من عدم ظهورها غير مُهرَّبة
   في أي من صفحات الويب الثلاث. شُغِّل فعليًا في هذه الجلسة — **كل الاختبارات نجحت** (راجع
   `malicious_dns_name/escaping-tests.txt`). هذا هو الدليل الفعلي لهذا البند، لا الملفات
   الأخرى في هذا المجلد التي توثّق المحاولة الأولى ولماذا لم تكن الطريق الصحيح.

### `simultaneous_faults` — عطلان متزامنان

`gateway_hijack` و`route_change` أُطلقا في نفس اللحظة تقريبًا (بلا فارق زمني متعمَّد) داخل
نفس الجلسة. النتيجة: كلا التغييرين سُجِّلا بشكل صحيح ومنفصل (`l2.arp_binding_changed` على
البوابة، `l3.route_added`+`l3.route_changed` على الشبكة الفرعية)، واستُنتجت حادثة
`gateway-hijack` بلا تداخل أو خلط بين السببين — دليل أن محرك الربط لا يفترض عطلاً واحدًا في
كل نافذة زمنية.

## 5. إعادة الإنتاج

السكربتات المستخدمة فعليًا لتوليد هذا الإصدار من الـcorpus محفوظة كمرجع (وليست جزءًا من شجرة
المصدر الدائمة؛ ستُنقل لأداة CLI حقيقية ضمن المرحلة 1 §4.1 بند "contract tests"):
`lab/inject.sh setup`، ثم تشغيل `netrewindd` يدويًا داخل `ip netns exec nrlab`، ثم
`lab/inject.sh run <scenario>` واحدًا تلو الآخر مع تفكيك كامل للمختبر (`teardown`) بين كل
تشغيل وآخر لضمان العزل. `collector_down` وحده شُغِّل خارج WSL، على Windows مباشرة، بلا مختبر
شبكي إطلاقًا (هذا بالضبط ما يختبره).

## 6. الإصدار

`v1` — 2026-09-13. أي تغيير في `rules/*.yaml` أو `internal/event/kinds.go` يُبطل هذا
الإصدار ويتطلب `v2` جديدًا، لا تعديلًا صامتًا على الملفات هنا (مبدأ "corpus versioned" في
خطة الإصدار §5 المرحلة 0).
