# خطة تحويل NetRewind إلى منتج سطح مكتب موثوق

> **حالة الوثيقة:** خطة تنفيذ، لا ادعاء بأن هذه الميزات موجودة الآن.  
> **المبدأ الحاكم:** لا يدّعي المنتج أنه رأى أو عرف ما لا يملك له دليلاً.
>
> **حالة التنفيذ الحيّة:** تُحدَّث باستمرار في `docs/agent-state/PROGRESS.md`
> (وما تم إنجازه فعليًا واختباره في `docs/evidence/`)، لا في هذا الملف —
> هذا المستند يبقى الخطة كما كُتبت، والتقدم الفعلي يُقاس بدليل قابل للتحقق في
> تلك الملفات، لا بتعديل النص هنا.

## 1. القرار التنفيذي

يُبنى NetRewind كمنتج محلي (local-first) من جزأين منفصلين بوضوح:

1. **وكيل التسجيل** (`netrewind-agent`) يعمل بصلاحيات محدودة، يلتقط التغيّرات ويكتب الدليل.
2. **تطبيق سطح المكتب** (`NetRewind Desktop`) يعمل كمستخدم عادي، يقرأ الدليل، يشرح الحوادث، ويضبط الوكيل. لا يملك واجهة المستخدم حق تغيير الشبكة.

يبدأ الإصدار التجاري بقدرات قراءة وتحليل متطابقة على Windows وLinux، ثم يحقق تكافؤاً موثقاً في الالتقاط الحي بقدر ما تسمح به كل منصة. لا يجوز تسمية قدرة بأنها "مدعومة" لمجرد أن التطبيق يفتح على النظام؛ تعرض الواجهة دائماً جدول القدرات الفعلية ومصدر كل دليل.

لا يبدأ الذكاء الاصطناعي قبل أن يصبح مسار الدليل، ومحرك القواعد، والواجهة، واختبارات المنصتين قابلة للتكرار. الذكاء المحلي يكون طبقة **اقتراح وشرح مقيدة بالدليل** فوق المحرك الحتمي، لا بديلاً عنه ولا مديراً ذاتياً للشبكة.

## 2. ما هو موجود الآن — نقطة البداية التي يجب عدم هدمها

المشروع الحالي نواة Go جيدة البناء وليست بعد تطبيق سطح مكتب:

| موجود ومثبت | المصدر في الشجرة | أثره في الخطة |
|---|---|---|
| غلاف أحداث versioned، دليل مرفق بكل حدث، ساعتان (wall/monotonic)، وهوية زمنية | `internal/event`, `internal/identity`, `docs/schema.md` | يبقى عقد البيانات المشترك بين المنصتين والذكاء الاصطناعي. أي تغيير له يحتاج migration وfixture متوافقاً للخلف. |
| SQLite، retention، طيّ التكرارات، أحداث لفجوات الرؤية أو الإسقاط | `internal/store`, `cmd/netrewindd` | هو سجل الدليل المحلي، وليس cache لواجهة المستخدم. |
| محرك Isnad وقواعد YAML تفرّق `causes` و`correlates` و`precedes` | `internal/correlate`, `rules/` | هو خط الأساس الحتمي الذي تُقاس عليه اقتراحات الذكاء. |
| CLI وويب قراءة فقط، محلي افتراضياً، وقوالب بلا JavaScript | `cmd/netrewind`, `internal/web` | تُستعمل كمرجع سلوكي وكمصدر بيانات أولي للواجهة الجديدة، وليست واجهة API مكتملة. |
| التقط Linux الحي: netlink، eBPF، nftables، socket capture، probes | `internal/collect/*_linux.go` | هذا هو المنتج Linux الحالي. يجب فصل الدلالة عن plumbing النظام قبل إضافة Windows. |
| حزم Linux، systemd، container، appliance، تحديث موقّع | `deploy/`, `internal/update` | لا يُستبدل مسار الثقة القائم؛ يُعمم بحذر على تطبيق سطح المكتب. |

التحقق في هذه المراجعة: `go test ./...` و`go vet ./...` و`GOOS=linux go vet ./...` مرّت على Windows. كما توجد أدلة تنفيذ حيّ سابقة في `docs/evidence/DEMONSTRATION.md`: سباق Go على Linux، و14/14 سيناريو حقن أعطال، وصفحات الويب وسجل المقاييس. تشغيل `go test -race` محلياً على هذه الجلسة لا يعمل لأن `CGO_ENABLED=0` في البيئة؛ هذا ليس نجاحاً زائفاً ولا فشلاً في الكود، ويجب أن يبقى اختبار السباق إلزامياً في runner يملك C toolchain كما هو في CI/Linux.

### الفجوات التي تمنع وصف المشروع الحالي بأنه برنامج Windows/Linux متكامل

- كل collectors غير Linux هي stubs تعيد `ErrUnsupported`؛ بناء `netrewindd.exe` ممكن لكن التسجيل الحي فيه سيتوقف ويكتب `system.collector_down`.
- الويب الحالي صفحة قراءة فقط بلا API موثق، ولا مصادقة لأنه local loopback فقط؛ لا يجوز فتحه للشبكة أو جعله backend لتطبيق تجاري كما هو.
- لا توجد خدمة Windows، واجهة إعداد، wizard صلاحيات، MSI/MSIX/NSIS موقّع، ولا سياسة تثبيت/إزالة/rollback خاصة بـWindows.
- العقد الحالي واسع في event schema لكنه يحتاج capability model وإصدارات API وmigration harness قبل تعدد العملاء.
- لا يوجد corpus واقعي موسوم، ولا مقياس جودة، ولا سياسة نماذج؛ إذن لا يصح وعد المستخدم بأن النموذج "يعرف سبب العطل" بعد.

## 3. نطاق المنتج والإصدارات

### 3.1 الإصداران المطلوبان

| الإصدار | ما يسلمه للمستخدم | حزمة الإصدار |
|---|---|---|
| **Windows x64** | Desktop + خدمة Agent اختيارية، استيراد حزم أدلة، تحليل محلي، صفحة capabilities، إعدادات واضحة، AI اختياري بعد اجتياز البوابة | installer موقّع (`.exe`/NSIS للمستهلك، و`.msi` للمؤسسات)، مع uninstall لا يلمس الدليل إلا باختيار مستقل وواضح |
| **Linux x86_64** | Desktop + Agent systemd المستمد من الوكيل الحالي، تحليل الأدلة الحالية والالتقاط الحي | `.deb` و`.rpm` للخدمة/التطبيق، وAppImage للعارض عند الحاجة؛ ثم Linux arm64 كامتداد مدعوم بعد اجتياز نفس الاختبارات |

لا يُضمّن ملف النموذج داخل installer: حجمه، الترخيص، وتحديثه مختلفون عن البرنامج. يُنزل صراحة بعد شاشة موافقة تعرض الاسم والإصدار والحجم والرخصة وبصمة التحقق.

### 3.2 تعريف النسخة الأولى الصادقة

**مشترك بين النظامين:** health وblind spots، incidents، timeline، البحث حسب المضيف/الوقت، استيراد/تصدير evidence bundle موقّع، إعدادات، diagnostic bundle، القواعد، وتحليل AI اختياري بعد بوابته.

**Linux:** يحتفظ أولاً بكل collectors الموجودة. الميزات المتوقفة على kernel أو privileges لا تختفي؛ تظهر disabled مع السبب والبديل.

**Windows:** يبدأ بقراءة وتحليل كاملين، ثم collector production للواجهات والعناوين والطرق وجيران IP، وسياسة Windows Firewall/WFP، والتقاط اختياري للحزم المرئية. يمكن لوظائف L4 أن تختلف في مصدرها عن eBPF؛ لا تستخدم عبارة "تكافؤ eBPF" على Windows. يعرض التطبيق لكل event `source`, `capability`, `coverage`, وسبب أي غياب.

## 4. البنية المستهدفة

```text
Windows APIs / Linux kernel / Npcap (اختياري) / WFP-ETW
                         │
                platform adapters  ──>  event envelope v1+
                         │                    │
                    netrewind-agent       SQLite evidence store
                         │                    │
                  local authenticated IPC / versioned API
                         │                    │
      NetRewind Desktop ─┼─ Timeline / Incident / Settings / Diagnostics
                         │
             deterministic retrieval + Isnad rules + feature extractor
                         │
                  optional local AI sidecar (no tools, no network)
```

### 4.1 إعادة تنظيم الكود قبل إضافة ميزات

1. تبقى `event`, `identity`, `store`, `incident`, `correlate` نواة platform-neutral. أضف لها tests تعاقدية لا تعتمد على OS.
2. عرّف ports دلالية: `InterfaceChanges`, `AddressChanges`, `RouteChanges`, `NeighborChanges`, `FlowObservations`, `PolicyObservations`, `WireObservations`. يترجم adapter النظام بياناته إلى observations مشتركة؛ المحلل الحتمي فقط يحولها إلى أحداث NetRewind.
3. انقل إنشاء قائمة collectors من `cmd/netrewindd/main.go` إلى registry يعلن: الاسم، المنصة، الصلاحيات، التغطية، الحالة، آخر heartbeat، وسبب التعطل. كل adapter جديد يخضع لنفس contract tests وfixtures.
4. أنشئ API محلياً versioned (`v1`) فوق IPC محلي فقط: named pipe مع ACL على Windows وUnix-domain socket مع permissions على Linux. يصدر DTOs ولا يكشف اتصال SQLite للواجهة. لا يُفتح HTTP على LAN في الإصدار الأول.
5. يبقى CLI متوافقاً ويستعمل core/API نفسه تدريجياً. يحفظ `netrewindd` alias لفترة انتقالية؛ لا تعطل automation المستخدمين بلا deprecation period.
6. أضف migration runner: backup قابل للتحقق، migration idempotent، نسخة schema، rollback/restore مجرّب، وفتح نسخة أحدث/أقدم بسلوك صريح.

### 4.2 الامتيازات والأمان

- الخدمة هي الوحيدة التي تطلب privilege؛ تطبيق desktop لا يعمل Administrator/root دائماً.
- Linux: حافظ على least capabilities الحالية وsystemd hardening. Windows: خدمة محدودة الحساب/ACL؛ elevation مرة واحدة للتثبيت أو capabilities المطلوبة فقط.
- ACL على دليل البيانات والمفاتيح، سرّ token محفوظ بـDPAPI في Windows وبمخزن أسرار/ملف 0600 في Linux. التشفير at-rest قرار منتج مستقل (أو يعتمد على BitLocker/LUKS) ولا يدّعى موجوداً قبل اختباره.
- الحزم المستوردة غير موثوقة: تحقق من zip-slip، الحجم، hashes، schema، وتاريخ المصدر. أسماء DNS وحقول الشبكة نص غير موثوق حتى لو كانت في database.
- لا AI tools، لا shell، لا وصول ملفات، لا network. يمر فقط context مبني من API. يمنع أي "طبّق الإصلاح" أو تغيير تلقائي للشبكة.

## 5. خطة التنفيذ المحكومة بالبوابات

كل مرحلة لا تنتقل إلا بعد تسليم الكود، الاختبارات، الدليل، والتوثيق المحددة فيها. لا تُستبدل البوابة بلقطة شاشة أو بكلام "يعمل عندي".

### المرحلة 0 — تثبيت المتطلبات والقياس (قبل كتابة واجهة)

- أنشئ `docs/product/`: personas (مهندس NOC، مهندس ميداني، مسؤول نظام)، حالات استخدام، non-goals، threat model، سياسة بيانات، مصفوفة دعم OS/kernel، وخطة تراخيص.
- حدد Windows الأدنى، توزيعات Linux المدعومة، معالجات x64/arm64، RAM/disk target، اللغات (العربية والإنجليزية)، وسياسة offline.
- حوّل 14 سيناريو Linux الحالية إلى corpus ثابت: event fixture، incident expected، evidence IDs، ولقطة واجهة متوقعة. أضف حالات: سجل أعمى، collector متعطل، بيانات ناقصة، حدث خبيث في hostname، وعطلان متزامنان.
- سجل baseline للأداء (حدث/ثانية، حجم DB، زمن query، RAM) على أجهزة مرجعية معلنة؛ لا تخترع أرقام تسويق قبل القياس.

**بوابة الخروج:** PRD موقّع، capability matrix، corpus versioned، وbaseline محفوظ في `docs/evidence/baselines/<version>/`.

### المرحلة 1 — عقود النواة وAPI والـevidence bundle

- افصل adapters عن التحليل كما في §4، مع schema conformance test على Linux/Windows.
- أضف API contracts، pagination، filtering، stream لتحديثات health، ومخرجات error typed. أضف authn/authz للـIPC واختبار عميل غير مخول.
- صمم evidence bundle read-only: manifest، schema/app version، recorder capabilities، events/incidents/rules ذات الصلة، checksums، optional signature، وبدون أسرار أو DNS names ما لم يوافق المستخدم.
- نفذ import في DB مؤقت ثم atomic promote؛ لا يلمس السجل الأصلي. أضف export redacted.

**الاختبارات:** unit + property/fuzz لمدخلات bundle، API contract test، migration upgrade/rollback، injection/escaping، 100 عميل قراءة متزامن، ومعادلة CLI/API لنفس fixture.

**بوابة الخروج:** يمكن فتح evidence bundle من Linux في Windows والعكس وإظهار نفس timeline/incident ونفس تحذير blind-spot.

### المرحلة 2 — تطبيق Desktop قابل للاستخدام قبل الالتقاط الحي

- استخدم Tauri v2 مع واجهة React/TypeScript، وGo agent كخدمة/sidecar مستقلة. الاختيار يحقق حزمة أصلية خفيفة نسبياً ولا يعيد كتابة core Go؛ أدوات Tauri تدعم Windows MSI أو NSIS، وعلى Linux deb/rpm/AppImage.
- ابنِ أولاً وضع **Demo/Import** على corpus ثابت ثم local store. لا تربط الواجهة بـSQLite مباشرة.
- الصفحات المطلوبة: Overview/Health، Incidents، Timeline، Investigation، Rules، Evidence bundles، Settings، Diagnostics. أضف empty/loading/error states قبل الرسوم الجميلة.
- wizard أول تشغيل: اختيار اللغة، بيان الخصوصية، اختيار agent/import، فحص الصلاحيات والقرص، فحص capabilities، ثم sample investigation قابل للتشغيل بلا شبكة.

**بوابة الخروج:** E2E يعيد سيناريو `change-broke-a-path` على Windows وLinux، ينتج incident نفسه، ويعرض chain وevidence، ويأخذ screenshot مرجعي لكل منصة.

### المرحلة 3 — تجربة المستخدم والتصميم البصري

النهج المقترح هو **Evidence-first calm operations UI**: هادئ ومقروء أثناء العطل، لا لوحة ألوان صارخة أو رسوم لا تساعد القرار.

- sidebar قصير: صحة التسجيل، الحوادث، الخط الزمني، التحقيقات، الأدلة، الإعدادات. أعلى الشاشة يبين recorder/host/time range وحالة الرؤية.
- في صفحة الحادث: عنوان واضح، confidence، ما نعرفه/ما لا نعرفه، chain عمودية؛ `causes` خط متصل بلون تحذير، `correlates` خط متقطع محايد، `precedes` منقط. لا يعتمد التمييز على اللون وحده.
- في Timeline: filter chips محفوظة، تكبير زمني، ربط الحادث بأحداثه، drawer للدليل الخام، و"سبب عدم التوفر" للـcollector بدلاً من blank chart.
- الإعدادات مجموعات مفهومة: **التسجيل**، **الخصوصية والبيانات**، **التنبيهات والتصدير**، **التحديثات**، **الذكاء المحلي**، **الدعم والتشخيص**. كل إعداد يصف أثره، القيمة الافتراضية، وحساسيته؛ اختبار config قبل الحفظ.
- Arabic-first بصورة كاملة: RTL للواجهات العربية، لكن IP/MAC/ports/timestamps تبقى isolated LTR ومثبتة؛ تبديل لغة فوري، نصوص تقنية غير مترجمة عند الحاجة مع شرح عربي.
- الخطوط المضمّنة offline: `IBM Plex Sans Arabic` للواجهة العربية، `Inter` أو `IBM Plex Sans` للاتينية، و`JetBrains Mono` للزمن/العناوين/الحزم، مع fallback `Noto Sans Arabic` وsystem font. يفحص build رخصها وحجمها وcontrast.
- light/dark/system theme، WCAG 2.2 AA، keyboard navigation، focus states، reduced motion، zoom 200%، وعدم افتراض اتصال إنترنت أو mouse.

**بوابة الخروج:** review تصميم على 3 سيناريوهات (سجل سليم، عطل، سجل أعمى) باللغتين؛ axe accessibility بلا violations حرجة؛ screenshot visual-diff على عرض desktop ونافذة ضيقة.

### المرحلة 4 — Linux Desktop + Agent productization

- يغلف الوكيل Linux الحالي كخدمة رسمية مع IPC، health، logs، config migration، وإدارة lifecycle من desktop.
- حوّل كل collector موجود إلى registry الجديد دون تغيير دلالته؛ أضف contract fixtures بجانب الاختبارات الحالية.
- أعد الاختبارات الحالية: unit/race، load، eBPF source/object، lab 14 fault، GNS3، ثم QEMU appliance. لا يكفي نجاح desktop على fixture.
- ابنِ `.deb`, `.rpm`, AppImage على أقدم baseline مدعوم؛ smoke test لكل حزمة في VM نظيفة، install/upgrade/uninstall/reboot، وتأكد أن uninstall لا يحذف evidence.

**بوابة الخروج:** Linux installer نظيف ينتج recorder شغال، desktop يقرأه عبر IPC، و14/14 lab + golden E2E + upgrade/rollback ناجحة.

### المرحلة 5 — Windows: إثبات الإمكان ثم المنتج

**5A: spike معزول، لا تدمجه مبكراً.** اختبر Windows IP Helper APIs لتغير الواجهات والعناوين والطرق والجيران، WFP/ETW للسياسة وblocked connections، وNpcap فقط إن كان التقاط DHCP/DNS/ICMP ضرورياً وموافقاً قانونياً. توثيق Microsoft يؤكد وجود notifications للواجهات والـroutes والـunicast addresses، لكنه لا يجعلها مكافئة لـnetlink تلقائياً؛ اختبر الفقد، الـcallbacks، IPv4/IPv6، sleep/resume وVPN.

**5B: production adapter.** بعد spike الناجح فقط:

- `windows.netio` يقرأ seed ثم notifications، ويحولها إلى نفس observations/events التي تنتجها Linux مع evidence خام مناسب.
- `windows.wfp` يسجل تغيرات policy وblocked events بصورة low-volume افتراضياً. لا تفعل success connection auditing افتراضياً؛ Microsoft تحذر من volume عالٍ جداً. لا تبنِ `first_failure_for_pair` إلا إذا أثبت مصدر baseline آمن ومحدود.
- `windows.wire` capability اختياري مرتبط بوجود Npcap وadapter selected وسياسة خصوصية؛ بدونه يسجل سبب الغياب ولا يوهم المستخدم بأنه يرى broadcast traffic.
- صمم Windows Service installer, recovery, event log source، ACLs، cleanup وdiagnostic collector. افصل executable signing عن driver licensing/signing.

**اختبارات Windows:** VMs نظيفة لكل إصدار مدعوم، IPv4/IPv6، Wi-Fi/Ethernet/VPN، sleep/resume، reboot/upgrade، user بلا Admin، تلف DB، packet-loss/backpressure، adapter disabled، firewall change، واستيراد Linux evidence. تجرى تغييرات الشبكة فقط داخل VM معزولة ثم تستعاد snapshot.

**بوابة الخروج:** capability report صحيح في جميع الحالات، لا silent loss، وتنجح semantic fixtures المشتركة. أي feature لا يبلغ عتبة الإثبات يبقى `Experimental` أو Disabled ولا يدخل ادعاء GA.

### المرحلة 6 — الذكاء المحلي (بعد المنتج الحتمي)

#### الاختيار المبدئي

اختيار البداية المعقول هو **Qwen3-4B Instruct، quantized GGUF `Q4_K_M`، عبر llama.cpp**؛ ليس لأنه "أفضل نموذج" نظرياً، بل لأنه أفضل فرضية أولى قابلة للقياس هنا:

- Qwen3-4B مرخص Apache-2.0، حجمه 4B، يدعم 100+ لغة، ويملك 32K context أصلياً؛ كما يتيح `thinking` للمراجعة العميقة و`non-thinking` للاستجابة الأسرع.
- GGUF/llama.cpp مناسب لـCPU: quantization من 1.5 إلى 8 bit، binary/API محلي، ويدعم Windows وLinux؛ لا يفترض وجود GPU. نثبت version/build/quantization ولا ننزل "latest" بلا رقم.
- profile متوازن: Q4_K_M مع context محدود افتراضياً (4K–8K) وthread count بعد benchmark. profile منخفض الموارد: Qwen3-1.7B أو نموذج يجتاز benchmark، لا 4B قسراً على جهاز لا يملك RAM كافية. profile اختياري للأجهزة الأقوى: 8B فقط بعد قياس واضح.
- **Gemma 3 4B** مرشح مقارنة، لا بديل مقرر: Google تضع 4B لأجهزة desktop/small servers وتدعم عائلة متعددة اللغات. يمر عبر الاختبار نفسه وفحص الرخصة/تنزيلها قبل أن يظهر للمستخدم.

صيغة الملفات والحجم المتاح وRAM لا يحددان الجودة وحدهما. لا يصح اختيار Phi/Gemma/Qwen اعتماداً على benchmark عام أو على اسم مشهور؛ corpus الشبكي ثنائي اللغة هو الحكم. لا يشحن model weights حتى ينجح إصدار pinned في evaluation وينتهي review الترخيص وprovenance.

#### تصميم AI الآمن

1. **Feature/Retrieval حتمي:** يستدعي API incident chain، الأحداث القريبة، collector capabilities/gaps، القواعد المطابقة، وإحصاءات بسيطة. يبدأ بـSQLite FTS والـrule retrieval؛ embedding model مرحلة لاحقة منفصلة.
2. **Prompt صغير ومغلق:** يمرر facts مع event IDs، حقل `unknowns`، وقواعد: لا تخترع حدثاً، لا تقول "سبب مؤكد" فوق confidence الدليل، لا تقترح تغييراً تنفيذياً، ارفض الاستنتاج في gap/collector-down.
3. **Output مقيد grammar/JSON schema:** `summary`, `ranked_hypotheses[]`, `evidence_event_ids[]`, `counter_evidence[]`, `unknowns[]`, `confidence_ceiling`, `next_checks[]`. يتحقق backend من وجود IDs وسقف الثقة قبل العرض؛ أي JSON خاطئ يعاد مرة واحدة أو تظهر رسالة فشل آمنة.
4. **واجهة صريحة:** badge "AI hypothesis — not evidence"، links لكل ID، زر "compare with deterministic incident"، نسخ report، وإيقاف/حذف model/data. لا يرسل أي byte خارج الجهاز.
5. **مدير النموذج:** signed manifest يستضيفه المشروع، URL pinned، SHA-256 + detached signature، TLS، resume آمن، قياس disk/RAM قبل التنزيل، pause/cancel/delete، health self-test، ومجلد quarantine للملف الفاسد. لا ينزل GGUF عشوائياً من مصدر community في production.

#### بوابة اختيار النموذج

أنشئ `ai/eval/` قبل واجهة AI: 14 سيناريو lab + حوادث منزوعة الهوية + حالات سلبية/ملتبسة + صياغات عربية وإنجليزية + prompt-injection داخل DNS/hostname. افصل train/dev/test ولا تضف hidden test للمطالبة نفسها.

لكل مرشح/quantization/platform سجّل: الإصدار، hash، الرخصة، hardware، RAM peak، tokens/sec، p50/p95، حرارة/استهلاك إن أمكن، JSON-validity، citation precision، Top-1/Top-3 cause coverage، false-causality، نجاح refusal عند gap، والنص العربي.

**معايير منع الإصدار (تثبت قيمها النهائية بعد baseline):** JSON صالح 100%، event IDs صحيحة 100%، لا ادعاء سببي عند gap أو collector-down، لا أدوات/شبكة/أوامر، وعدم تجاوز budget RAM/latency المعلن. لا يخرج candidate للـbeta ما لم يحقق عتبة Top-3 والـcitation المتفق عليها على test مجمد ويجتاز red-team. نتيجة سيئة تعني إبقاء AI off، لا تخفيض العتبة بصمت.

### المرحلة 7 — إصلاح المنتج للإصدار وbeta

- CI matrix: Windows + Ubuntu؛ Go unit/race/vet/fuzz، API contracts، frontend type/lint/unit، Playwright E2E، accessibility، visual diffs، installers smoke VMs، dependency/vulnerability/SBOM، secret scan، وlicense scan.
- nightly: Linux 14-fault lab، load/stress، GNS3، appliance boot، Windows isolated-network suite، DB migration matrix، AI benchmark matrix. كل فشل يحفظ logs وDB وscreenshots وsystem facts كـartifacts.
- supply chain: deterministic version metadata، SBOM SPDX/CycloneDX، hashes، provenance، signing key خارج CI قدر الإمكان، Windows code signing، توقيع Linux repository/package، وverify-before-replace/rollback لكلا agent وdesktop.
- beta مغلق: telemetry محلي opt-in ومزال الهوية، export diagnostic bundle بعد موافقة، crash reports بلا events/raw IPs افتراضياً، channel stable/beta، feature flags محلية لا تخفي نقص coverage.
- قبل GA: مراجعة أمنية مستقلة، مراجعة AGPL-3.0 وحقوق العلامة ونصوص third-party (Go/Rust/Node/Npcap/fonts/models)، privacy notice، EULA إن لزم، سياسة دعم، وقائمة known limitations ظاهرة.

## 6. بروتوكول إلزامي لأي وكيل/مطور

لا يبدأ أي task بكود عشوائي. كل task يجب أن يمر بهذا التسلسل ويوثق مخرجاته في PR:

1. **استكشاف:** اقرأ architecture/schema/rules/tests ذات الصلة، حدّد المالكين والتأثير، واكتب requirement قابل للاختبار وnon-goals ومخاطر المنصة.
2. **تصميم قبل التنفيذ:** ADR قصير: بدائل، سبب الاختيار، contracts، migration، privacy/security، telemetry، failure mode وrollback. التغيير الدلالي في event يحتاج fixture وschema decision.
3. **اختبار أولاً أو بالتوازي:** unit للمنطق، contract للـadapter، negative/adversarial، integration مع store/API، E2E لمسار المستخدم. أضف reproduction minimal لكل bug.
4. **تنفيذ صغير قابل للمراجعة:** لا refactor واسع مع feature جديدة، لا credentials، لا broad privileges، ولا paths/commands تتغير في الإنتاج بلا confirmation وتصميم.
5. **إثبات:** سجل command مع version/OS، نتيجة الاختبارات، logs عند الفشل، database أو bundle محدود، screenshot/فيديو قصير للحالة النهائية في Windows وLinux، ولقطة accessibility عند تغيير UI. سمِّ الملفات مثل `docs/evidence/<release>/<os>/<scenario>/` واربطها بالـcommit وchecksum.
6. **توثيق وتسليم:** user help، setting defaults، permissions، limitations، troubleshooting، release notes، migration/rollback. لا يدمج task إن كانت الوثائق تقول شيئاً لا يثبته الاختبار.

### Definition of Done للإصدار

- كل capability في marketing/docs تربط باختبار، OS، وصلاحية مطلوبة؛ ما لا يملك اختباراً يسمى experimental أو غير مدعوم.
- لا incident/AI answer يخفي gap أو source مفقود؛ واجهة المستخدم تضعه قبل الاستنتاج.
- Windows وLinux يجتازان corpus المشترك ويظهران report قابل للمقارنة؛ الفروقات موثقة في capability matrix.
- installer install/upgrade/repair/uninstall وrollback مجربة في VM نظيفة، ولا تحذف data أو config دون اختيار صريح.
- جميع artifacts موقعة/متحققة، SBOM متاح، والحزم تمسح أمنياً.
- دليل المستخدم العربي والإنجليزي، screenshots حقيقية من النسختين، وknown limitations مكتوبة قبل GA.

## 7. ترتيب العمل العملي التالي

1. اعتماد هذه الوثيقة بعد حسم OS minimum، license/business model، وسياسة بيانات العملاء.
2. إنشاء backlog للمرحلة 0 فقط، وإنتاج capability matrix/corpus/baseline؛ **لا تبدأ AI أو Windows capture قبلها**.
3. تنفيذ المرحلة 1 ثم Desktop import/demo (2–3) لتقديم قيمة Windows وLinux سريعاً من دون ادعاء capture غير موجود.
4. إبقاء Linux regression gate فعالاً أثناء productization، ثم تنفيذ Windows spike وفصله عن GA إلى أن يثبت.
5. جمع corpus واقعي من beta بموافقة ومن دون أسرار، ثم تشغيل benchmark Qwen/Gemma/quantizations، واختيار model pinned بنتيجة منشورة.
6. تشغيل beta بمصفوفة دعم صادقة، ثم GA فقط بعد بوابات المرحلة 7.

## 8. مراجع قرار التقنية

- [Qwen3-4B model card](https://huggingface.co/Qwen/Qwen3-4B): Apache-2.0، 4B، لغات متعددة، والتحويل بين reasoning/non-reasoning.
- [llama.cpp](https://github.com/ggml-org/llama.cpp): تشغيل محلي CPU وتنسيق GGUF والـquantization وواجهة server محلية.
- [Microsoft NetIO API](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/) و[NotifyRouteChange2](https://learn.microsoft.com/en-us/windows-hardware/drivers/network/notifyroutechange2): أساس spike لتغيرات الشبكة في Windows.
- [WFP auditing](https://learn.microsoft.com/en-us/windows/win32/fwp/auditing-and-logging): حدود ومصادر policy/blocked connections في Windows.
- [Tauri distribution](https://v2.tauri.app/distribute/): خيارات bundling لـWindows وLinux.
