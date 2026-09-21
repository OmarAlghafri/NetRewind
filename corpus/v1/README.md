# مجموعة اختبارات NetRewind المرجعية — الإصدار v1

> تنفّذ هذه المجموعة متطلبات المرحلة 0 من `docs/PRODUCT_RELEASE_PLAN_AR.md`: تحويل
> سيناريوهات Linux الأربعة عشر إلى مجموعة ثابتة من الأحداث والحوادث المتوقعة
> ومعرّفات الأدلة. ويضيف حالات لفجوة الرصد وتوقف مجمّع ونقص البيانات ومدخل خبيث
> وعطلين متزامنين.
>
> **جميع الأرقام وأنواع الأحداث مأخوذة من تشغيل معزول فعلي لكل سيناريو** على WSL2
> ضمن `netrewind-lab` وAlpine، باستخدام النواة
> `6.6.87.2-microsoft-standard-WSL2` مع توفر BTF. ولم تُنسخ النتائج من التوثيق.
> ويمكن إعادة إنتاجها بالخطوات الواردة في §5.

## 1. البنية

يحتوي كل مجلد `<scenario>/` على:

- `events.json` و`events.txt` — جميع الأحداث المسجّلة بصيغة JSON ونص مقروء.
- `incidents.json` و`incidents.txt` — الحوادث التي استنتجها محرّك الإسناد.
- `recorder.log` — سجل تشغيل `netrewindd` في تلك الجلسة، ويُستخدم لتشخيص أي اختلاف لاحق.

## 2. السيناريوهات الأربعة عشر

شُغّل كل سيناريو بمعزل كامل عن بقية السيناريوهات.

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

## 3. لماذا لا تنتج بعض السيناريوهات حادثة منفردة

لم ينتج السيناريوهان `link_flap` و`route_change` أي حادثة عند تشغيلهما منفردين، وهذا هو السلوك
المتوقع. تحتاج قواعد الارتباط (`rules/*.yaml`) عادة إلى نتيجة مرصودة في وقت قريب،
مثل فشل اتصال أو توقف مضيف عن الرد، حتى تبني حادثة تربط التغيّر بالعَرَض. وعند تشغيل
`link_flap` ضمن التسلسل الكامل (`docs/evidence/04-full-lab-gate.log`) مع سيناريوهات
تولد اتصالات، استنتج المحرّك `link-down-isolated-hosts` بثقة 80%. لذلك يجب أن تختبر
الاختبارات التعاقدية في المرحلة 1 الحالتين: السيناريو المنعزل والسيناريو داخل سياق.
غياب الحادثة قد يعني نقص السياق، ولا يعني بالضرورة وجود خلل في القاعدة.

## 4. الحالات الإضافية المطلوبة في §5 من خطة الإصدار

### `blind_gap` — فجوة في التسجيل

أُوقف المسجّل عمدًا (`system.stop`) لمدة 20 ثانية، وهي أطول من قيمة
`--gap-threshold 12s`، ثم أُعيد تشغيله على قاعدة البيانات نفسها. سجّل الحدث
`system.gap` المدة الصحيحة، وظهر أيضًا `system.collector_down` لأن تشغيل WSL2
لم يفعّل جميع قدرات eBPF بين عمليتي التشغيل في هذا السيناريو. ونتجت حادثتان:
`recorder-was-blind` و`collector-not-watching`، وكلتاهما بثقة 100%. يعرض
`blind_gap/incidents.txt` النص الكامل.

### `collector_down` — مجمّع متوقف

يتحقق هذا السيناريو من العبارة الواردة في §2 من `PRODUCT_RELEASE_PLAN_AR.md` بشأن
إنشاء `system.collector_down` عند تشغيل المجمّعات غير المدعومة على Windows. شُغّل
`netrewindd.exe` على Windows مباشرة، لا داخل WSL. فنتجت سبع رسائل
`system.collector_down`، واحدة لكل مجمّع مقيد بـLinux، وسبع حوادث منفصلة من قاعدة
`collector-not-watching` بثقة 100%. وبذلك تطابق السلوك الفعلي مع ما وصفته الخطة.

### `malicious_dns_name` — اسم مضيف أو اسم DNS خبيث

تطلب هذا البند فحصًا يتجاوز تشغيل سيناريو المختبر، وكانت النتيجة كما يلي:

1. **الحدث `dns.resolver_changed` (الذي ينتجه `resolver_change` وهذا الاختبار) لا يحمل اسم
   الاستعلام إطلاقًا** — تحقّقنا من هذا في `internal/collect/wire/dns.go`: فقط
   `dns.query_fail` و`dns.latency_spike` يستدعيان `attachName()`، وحتى هما لا يفعلان ذلك إلا
   إذا فُعِّل `--record-dns-names`، وهو معطّل افتراضيًا حمايةً للخصوصية. لذلك لم تصل محاولة حقن
   `<script>alert(1)</script>.lab` كاسم DNS عبر `resolver_change` إلى أي حقل مخزّن
   (راجع الملفات في هذا المجلد). ويؤكد ذلك أن هذا المسار لا يخزن الاسم أصلًا.
2. **الوصول الفعلي لمسار الكود الذي يخزّن الاسم** (`dns.query_fail`/`latency_spike`) يحتاج
   رسالة DNS من نوع **استجابة** (rcode فاشل)، وأداة المختبر الحالية `lab/inject/main.go`
   (`nrinject dns`) **تبني استعلامات فقط** (`buildDNSQuery`)، لا استجابات. وهذا قصور
   فعلي في أداة المختبر ينبغي معالجته في المرحلة 1.
3. **يوجد بالفعل اختبار مباشر لهذا المسار:**
   `internal/web/escaping_test.go` (`TestHostileStringsFromTheWireAreEscaped`,
   `TestHostileQueryParametersAreEscaped`) يُدخل نفس النوع من الحمولات (`<script>`,
   `" onmouseover=`, إلخ) مباشرة كـ`Attrs`/`Subject.Label` ويتحقق من عدم ظهورها غير مُهرَّبة
   في أي من صفحات الويب الثلاث. نجحت جميع الاختبارات في هذه الجلسة؛ راجع
   `malicious_dns_name/escaping-tests.txt`. وهذا هو الدليل المعتمد لهذا البند، بينما
   توثق الملفات الأخرى المحاولة الأولى وسبب عدم وصولها إلى مسار التخزين المقصود.

### `simultaneous_faults` — عطلان متزامنان

شُغّل `gateway_hijack` و`route_change` في الوقت نفسه تقريبًا داخل جلسة واحدة، من
دون إضافة فارق زمني مقصود. سجّل النظام التغيّرين بصورة صحيحة ومنفصلة، واستنتج
حادثة `gateway-hijack` من دون خلطها بتغيّر المسار. يثبت ذلك أن محرّك الارتباط لا
يفترض وجود عطل واحد فقط داخل النافذة الزمنية.

## 5. إعادة الإنتاج

حُفظت البرامج النصية المستخدمة لتوليد هذا الإصدار من مجموعة الاختبارات كمرجع مؤقت.
وليست جزءًا دائمًا من شجرة المصدر؛ إذ ستُنقل إلى أداة سطر الأوامر في المرحلة 1، §4.1،
ضمن الاختبارات التعاقدية:
`lab/inject.sh setup`، ثم تشغيل `netrewindd` يدويًا داخل `ip netns exec nrlab`، ثم
`lab/inject.sh run <scenario>` واحدًا تلو الآخر، مع تفكيك المختبر بالكامل (`teardown`) بين كل
تشغيل وآخر لضمان العزل. أما `collector_down` فشُغّل مباشرة على Windows خارج WSL
ومن دون مختبر شبكي، لأن هذا هو السلوك الذي يختبره.

## 6. الإصدار

`v1` — 2026-09-13. يتطلب أي تغيير في `rules/*.yaml` أو
`internal/event/kinds.go` إصدار `v2` جديدًا. ولا يجوز تعديل هذه الملفات بصمت،
اتساقًا مع سياسة إصدار مجموعة الاختبارات في المرحلة 0 من خطة الإصدار §5.
