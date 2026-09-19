# Changelog

## 1.1.0 — 2026-09-19

A modernization pass on the desktop application: the shell no longer scrolls
itself away, Arabic covers the actual content an investigation reads rather
than only the surrounding chrome, the client talks to a live recorder
efficiently instead of re-fetching everything on a timer, and every page the
1.0.0 PRD named gets the workflow it was missing. No API contract broke; every
addition is additive.

### The shell stays where you put it

`.app-shell` bounded itself with `min-height: 100vh` and no independent scroll
regions, so once a page's content grew past the viewport (any incident list or
timeline longer than a screenful), the whole grid — sidebar included — scrolled
away as one unit, taking the language toggle and the lower navigation items
with it. `.app-frame` now sets an actual `block-size: 100dvh; overflow: hidden`
ceiling; the sidebar and the workspace body each get their own scroll region,
so the sidebar's footer and language toggle stay reachable regardless of how
long the current page is. The onboarding wizard had the identical defect at a
smaller scale (its longest step overflowed the minimum supported window by
15px) and is fixed the same way. Sidebar navigation is native `<button>`s in a
labelled `<nav>` now, not `<div role="button">` with hand-rolled key handling.
Proven across 5 widths (720–1920px) × 2 languages × 2 color schemes, plus the
WCAG 1.4.10 320px reflow check, with a real browser-driven Playwright suite
that did not exist before this release.

### Real Arabic, not just Arabic chrome

Every rule's title, advice, and per-clause explanation — the actual sentences
an investigation reads — were English-only in the engine, with no
localization mechanism at all; only the UI chrome around them was bilingual.
Rules now carry an optional `i18n` block (title, advice, and a translation
per matched clause, keyed by the clause's own name so a translation for a
clause that doesn't exist is rejected at load time, not silently ignored),
and all 19 shipped rules are translated. `incident.Link` gained `clause`, so
a chain link's explanation can be looked up by rule and clause even when it
fell back to a generic description. The 49-kind event vocabulary and every
rule's content are generated into the desktop bundle (`internal/event/gen`,
`internal/correlate/gen`) with a test that fails on drift from the Go source
of truth, so demo mode, bundle mode, and a live recorder all render the same
translations. A new formatting service gives times, dates, durations, and
counts a single, correct Arabic rendering (Latin numerals, proper grouping —
ten call sites had each grown their own inconsistent formatting before this).
The full §5 content-contract sweep (terminology corrections, the wizard's
privacy step, and a blanket-avoid glossary) landed as well. An automated
scanner (`desktop/tests/visual/latin-text-scan.spec.ts`) now flags any
untranslated English sentence rendered in the Arabic app that isn't a
short technical term or acronym — not "any Latin text," which Arabic
technical writing legitimately mixes in — and found real gaps this release
closes, not just the ones anticipated in advance.

### The client stops re-fetching everything on a timer

`/v1/events` and `/v1/incidents` gained keyset cursor pagination
(`cursor`/`next_cursor`/`has_more`, plus `order=asc|desc`), correctly aware
that a folded repeat updates its existing row rather than appending a new
one. `/v1/rules` and `/v1/capabilities` support conditional GET (ETag/304),
and the Tauri bridge gained real per-request cancellation, so a superseded
poll actually stops instead of merely being ignored when it finishes. The
desktop client now polls by delta instead of re-fetching a full 24h/5000-event
window every few seconds. A new `GET /v1/what-happened` endpoint gives the
GUI the same identity-aware "what happened to this host" reconstruction the
CLI has always had, expanding a host to every address it's held rather than
only the one in front of you. The desktop app also gained real URL-based
routing (`#/page?...`) in place of in-memory page state — every page is now
a deep link, and browser back/forward works.

### The investigation workflows the PRD named

- Chain links can disclose their raw evidence (matched-count, the events
  behind a clause) without leaving the incident view.
- Evidence can be exported scoped to one incident's own window, with
  adjustable padding, instead of only a fixed recent-time range.
- Diagnostics gained a one-click, secret-free support-summary copy button.
- Incidents gained search, severity/family filters, and sort — all
  reflected in the URL, so a filtered investigation can be bookmarked or
  shared — plus an optional master/detail layout.
- Settings gained a sticky save/discard bar and a guard against losing
  unsaved changes, on both in-app navigation and tab close.
- Rules gained a match-count date range.
- Connection failures and capability reasons are now structured
  `{code, params, technical_detail}` values with a translated message and
  the raw detail available on demand, replacing twelve hard-coded English
  strings on the Rust side alone.

### Type is no longer a fallback font

IBM Plex Sans Arabic, Alexandria, Inter, and JetBrains Mono are vendored
(15 WOFF2 files, SIL OFL 1.1) instead of being named in CSS and left to
whatever the OS happened to have installed. Two real WCAG contrast failures
in the design tokens were found and fixed (a warning color at 4.39:1, and
white-on-accent button fills as low as 2.66:1 in dark mode — a genuinely
different constraint than the same colors used as text, which is what they
were originally tuned for). Every scroll region added by this release is
keyboard-focusable, closing an axe-detected gap that a mouse-only pass would
never have found.

### Local AI evaluation continues; still nothing ships

The evaluation harness that measures a candidate model's answers gained the
fixes needed for a real benchmark to mean something: evidence is now cited
by short closed handles instead of requiring a model to reproduce a
26-character event ID verbatim (the actual reason both previously-measured
candidates failed), the confidence ceiling is enforced structurally rather
than only checked after the fact, address redaction was generalized beyond
one lab's fixed subnet, and the request shape sent to the local model server
was corrected twice — once for a structural mismatch, once for a
documented-but-non-functional field value — each found only by running the
real, downloaded model against the real, pinned server rather than by
reading its documentation more carefully. The corpus grew from 33 to 37
cases. No model ships and no AI feature is enabled in this release; the
actual benchmark run stays on hardware set aside for it.

### Also

- `desktop/src/demo/demo-incidents.json` is regenerated to carry the new
  `clause` field, via a new tool (`internal/correlate/replaydemo`) that
  replays the demo recording's raw events through the real engine rather
  than its already-folded export — fixing a real gap where a folded event's
  true occurrence count was silently understated on replay.
- A genuine, previously-unnoticed non-determinism in the correlation engine
  is fixed: an incident's `victims` list is now sorted, where it previously
  depended on Go's randomized map iteration order.

## 1.0.0 — 2026-09-14

The first release of NetRewind as a product rather than a Linux daemon with a
CLI: a recorder on Linux and on Windows, a desktop application that reads it,
and evidence bundles that carry a record between machines. What the recorder
observes on each platform is stated by the recorder itself, in a capability
report, rather than claimed by the documentation.

### The recorder serves a local API, and the desktop application reads it

`netrewindd` now serves a versioned JSON API (`/v1/health`, `/v1/capabilities`,
`/v1/events`, `/v1/incidents`, `/v1/rules`, `/v1/bundle`) over a local-only
transport: a Unix domain socket on Linux (`/run/netrewind/api.sock`, shared
with the `netrewind` group), a named pipe on Windows (`\\.\pipe\netrewind-api`,
restricted by ACL to the installing user). There is no TCP port to find; the
API is read-only by construction, and the store handle never leaves the
process.

The desktop application (`desktop/`, Tauri + React, Arabic and English) has
three sources: the demo recording it always carried, a live recorder over
that API, and an evidence bundle file. Every page — health, incidents,
timeline, investigation, rules, evidence bundles, diagnostics, settings — takes
the same events and incidents whichever source they came from. The health
page shows the recorder's own capability report: what it is watching, what it
is not, and why. `netrewind status` asks the same questions from a terminal.

### Windows is a platform, not a stub

`internal/collect/iphelper` records interfaces, addresses, routes and
neighbours on Windows through the IP Helper API and feeds the same analysis
code as the Linux netlink collectors, so a gateway hijack or a lost default
route looks the same in the record whichever kernel saw it. Interface rows are
re-read after every notification, twice, because the notification arrives
while the change is still being applied and the row read at that instant can
still show the state from before. The neighbour table is polled every two
seconds; Windows has no change notification for it, and the capability report
says so. Flows, filtering policy, DHCP/DNS on the wire and active probes have
no Windows source in this release and are reported as unsupported rather than
run as stubs.

`netrewindd service install` registers the recorder as a Windows service:
LocalSystem, automatic start, restart on failure, an event-log source, a
configuration under `%ProgramData%\NetRewind` and the installing user granted
access to the API pipe. The desktop installer for Windows bundles the recorder
and runs that registration; uninstalling unregisters the service and leaves
the record in place.

### Evidence bundles are usable from end to end

`netrewind bundle export` writes a window of the record as a checksummed
archive; `bundle inspect` verifies one without importing it; `bundle import`
turns one into a separate store. The desktop application exports from a live
recorder and opens any bundle after verifying every member's checksum and, when
a public key is configured, its ed25519 signature. A bundle is never merged
into the local record.

### Packaging

The `.deb` and `.rpm` were installed, upgraded, removed and purged on real
systemd hosts, and three defects that only show up there were fixed: the
service was stopped in the wrong maintainer script and left running after
removal; an upgrade left the previous binary running; and neither package
declared its dependency on `nft`. Both packages create the `netrewind` group
and recommend `nftables`. Linux desktop bundles (`.deb`, `.rpm`, AppImage)
and Windows installers (NSIS, MSI) are built by `make desktop`.

### Local AI stays off

Two candidate models were benchmarked in both languages against the
evaluation corpus. Neither meets the release gate; the runner's two
measurement defects (non-reproducible greedy runs, no answer-language
instruction) are fixed and the results recorded. No model ships, and no AI
feature is enabled in this release.

### Also

- Platform defaults on Windows moved from the temporary directory to
  `%ProgramData%\NetRewind`.
- Analyzers stamp events with the platform source they came through
  (`netlink` or `iphelper`).
- Collectors a build cannot run are reported as `unsupported`, distinct from
  `down`.
- `deploy/build-rpm.sh` builds the `aarch64` package on an `x86_64` host
  (`rpmbuild --target`; the spec no longer pins `BuildArch`).
- A frontend test suite (Vitest) and Rust unit tests for the desktop shell run
  in CI alongside the Go suite.

## 0.9.1 — 2026-08-29

A hardening release. Nothing here changes what the recorder is for; all of it
came out of trying to break the recorder deliberately, and all of it is about
the same thing — the ways the record could stop being trustworthy without
anybody being told.

### The burst these rules exist for is no longer the burst that stopped the recorder

Correlation re-walked every event in its window as a candidate opening on every
arrival. The cost of one event grew with the window and the cost of a burst
grew with its cube: two thousand flaps took thirteen seconds of CPU and five
thousand took thirty-four, on the goroutine that also makes events durable. A
flapping port is the shape `port-flapping` was written to recognise, and it was
the shape that made the recorder fall behind the network — the queue backs up,
netlink overruns, and the record ends up with a hole in exactly the period
somebody will later need.

Each opening now remembers what it is waiting for, so an arrival that cannot
change the answer skips the walk, and openings waiting on a kind that has not
arrived are not visited at all. The same burst is now linear in its size:
eight thousand events take 34 ms rather than minutes. The window is also
bounded by count as well as by time, because a time bound is not a bound when
events arrive faster than the window empties — and what it had to drop is
counted in `netrewind_correlation_dropped_total`, on the principle that
correlation's blind spots belong in the same place as the recorder's.

### Retention now bounds the disk, which is what it always said it did

`retention` pruned the events. The conclusions and the identity bindings grew
without limit: `PruneIncidents` existed, was tested, and was called by nothing,
and nothing pruned identity at all. So a recorder on a segment with any churn
filled its disk however retention was configured — and the deployment that
suffers most is the appliance, whose whole premise is being plugged in and
forgotten. Measured before the fix: a prune that removed all twenty thousand
events left twenty thousand identity bindings and a thousand incidents behind.

Incidents are now kept four times as long as the events under them, which is
what the runbook always claimed. Identity is pruned on two rules that are
provable rather than approximate: a superseded binding goes once it ended
before the oldest event kept, and a binding still in force goes only when no
retained event names its host — because a binding in force carries when it was
made, not when it was last seen, and pruning those by age would delete exactly
the machines that have been on the network longest.

### The terminal no longer executes what the network wrote

The Linux kernel accepts an escape character in an interface name. A DNS name
is whatever a machine on the watched segment looked up. Printed unchanged,
`ESC [ 2 J` clears the operator's screen and a carriage return overwrites the
line before it — so the machine being investigated got to decide what the
investigator read. Everything the CLI prints now renders those bytes as `\xNN`
instead of passing them to the terminal. The store still keeps exactly what
arrived; this is the last step before bytes become something a terminal acts
on, and nothing else.

### A release can no longer be cut with a standard library that has holes in it

`go.mod` names a toolchain rather than raising the `go` line, so the tree keeps
building on a distro that pins `GOTOOLCHAIN=local` with an older Go. The cost
was that on exactly those machines the directive had no effect: the build
succeeded and quietly linked the older standard library. `govulncheck` reports
nine vulnerabilities against such a build and none against one built with the
toolchain `go.mod` asks for — and one of the nine is in `html/template`, which
is what renders network-supplied strings into the web interface. Building that
way is still fine; `make release` and `make image` now refuse to.

### CI cannot overwrite a signed release

It could, and the way it would have failed is the reason this is worth a
heading. A release is cut from the machine holding the signing key, so the
release on GitHub already carries a signed `SHA256SUMS` by the time a tag's CI
run finishes — and `action-gh-release` replaces assets of the same name. CI's
unsigned checksum file would have taken the place of the signed one, leaving
`SHA256SUMS.sig` a signature over a file no longer in the release.

`sha256sum -c` would still have passed, because CI's sums match CI's tarballs.
So the release would have looked correct to anyone checking it by hand, while
every recorder configured with `update.public_key` refused it and reported only
that no matching asset was found. The job now keeps its build as a workflow
artefact and publishes nothing.

> **Note.** 0.9.0 was written up here but never tagged or published. This is
> the first release since 0.8.0, and it contains everything both entries
> describe.

### Fixes

- **`install.sh` edited a configuration somebody had tuned.** The rewrite that
  points `db:` and `rules:` at the host's paths ran on every install, including
  an upgrade — so an operator who had moved the store to a bigger disk got it
  moved back, the recorder opened an empty one at the default path, and months
  of timeline read as though the network had never done anything.
- **`install.sh` never created the directory it installed into.** `/usr/local/bin`
  exists on most systems, which is why this went unnoticed; where it does not,
  the install stopped part way through.
- **`CONFDIR` was accepted and then ignored.** The recorder reads
  `/etc/netrewind/netrewindd.yaml` unless told otherwise, so a non-default
  `CONFDIR` installed a file nothing would read, and the check meant to catch an
  unusable configuration checked a different one. The unit and the check are now
  told where the configuration is.
- **The store outage that logs four lines a second.** A refused write was
  reported on every flush for the duration of the outage, which buried the one
  line that says what went wrong — and on a machine where the log and the store
  share a filesystem, made a full disk fuller.
- **An OTLP endpoint's credentials were written to the log.** A collector behind
  basic auth is reached as `https://user:password@host`, and the endpoint was
  logged verbatim at every start.
- **The container image shipped its configuration world-readable and
  executable.** That file is the documented place for `update.token` and for an
  OTLP `Authorization` header, and the image's own comments suggest running it
  as `--user 10001`, which could read them.
- **The security test for the web interface tested nothing.** It sent the
  payload as `?host=`, which no handler reads; the search field is `?q=`. The
  escaping was in fact sound — html/template was doing its job — but the test
  proving it was not looking at the page.
- **The runbook did not mention every metric it should be watched through.** It
  is now checked against the registry rather than against memory.

### Hardening

- The web interface declares a content security policy that forbids script
  outright, along with `nosniff`, `DENY` framing and `no-referrer`. The pages
  have no JavaScript, load nothing from anywhere else and submit only to
  themselves, so the strictest policy that can be written is also the one that
  describes them exactly.
- The `nft` binary is found at an absolute path before `PATH` is consulted. A
  process running as root with `CAP_BPF` that can be configured to replace its
  own binary should not let an inherited `PATH` decide what it executes.
- Rule text carried as evidence is bounded by length as well as by line count.
  An nftables rule with a large set inline is one line of arbitrary size.
- Fuzz targets for the three parsers that read bytes off the wire. 18.3 million
  executions found no crash; they are in the tree so the next change is checked
  the same way.

## 0.9.0 — 2026-08-27

The release the recorder can install for itself, and the first one an operator
can prove came from its author rather than from whoever holds the repository.

### The recorder keeps itself current

It asks its own release feed once a day, records `system.update_available` when
a newer version exists, and — only if told to — installs it. Checking and
installing are separate settings because they are separate decisions: knowing a
fix exists costs one HTTPS request and is nearly always wanted, while letting a
machine on your network rewrite its own binary is a change of trust. Installing
is off by default.

Everything about the path is built on the assumption that this is a supply
chain and the thing being replaced is a witness:

- HTTPS only, and a plaintext asset URL is refused outright
- every download checked against the `SHA256SUMS` published with the release
- an optional ed25519 signature over that file, and a release **without** a
  valid one is refused whenever a public key is configured, because an optional
  check that can be skipped is decoration
- the new binary is run before anything is replaced, and the old one is kept
- rules that are new in the release are added; rules that were edited locally
  are left alone
- the swap is recorded as `system.updated`, so the gap either side of the
  restart has an explanation beside it

### Releases are signed with a key that never touches CI

Checksums prove a download arrived as the server sent it. They do not prove who
built it — anyone who can publish a release can publish checksums for it. `make
signing-key` creates an ed25519 key that lives outside the repository and
outside CI, and `make release` signs the checksum file with it and then
**verifies the signature using the same code the recorder uses to check one**,
because signing without verifying is how a release goes out that every updater
refuses.

### Fixes

- **`make release` would have named its assets something no updater could
  find.** `VERSION` came from `git describe --tags`, which returns `v0.9.0` at a
  tag, and the updater asks for `netrewind-0.9.0-...`. The 0.8.0 release
  happened to be built with the version passed by hand, so the bug stayed
  invisible, waiting for the first release built the obvious way. The leading
  `v` is now stripped in one place, and a test compares the Makefile's naming
  against the updater's expectation.
- **The appliance image named itself after a commit.** It worked out its own
  version instead of being told one, so the 0.8.0 image reported `0fb51d7` — a
  hash from an untagged tree, matching no release, and not a version any
  updater can parse. An appliance is the deployment nobody looks at, so an
  image that silently declines every update for the rest of its life is the
  worst place for this to happen. `make image` now passes the release version,
  and a test pins that it does.
- **`--version` was gated behind the configuration being valid**, so the same
  binary printed its version from a directory that happened to contain a
  `rules/` and exited 2 from one that did not. The updater runs exactly that
  flag on a downloaded build to decide whether it works before replacing
  anything, inheriting the daemon's working directory — so whether a recorder
  could update itself depended on where it had been started from, and the
  refusal blamed the new build rather than a rules path. A flag that exists to
  prove a binary runs cannot fail for reasons of its own.
- **The eBPF object is checked against its source, not against a rebuild.**
  Comparing bytes against a fresh build only ever asked whether the runner had
  the same clang.
- **The reserved-kind count in the schema document is now checked.** The prose
  said nine while the table marked seven, which teaches a reader to go and
  count the rows themselves.
- **The operating runbook was missing two of the metrics it tells you to alert
  on.** `netrewind_store_writable` and `netrewind_otlp_dropped_total` had been
  stranded above the title rather than in the table — a document about not
  missing things, quietly missing things.
- **The rule-writing guide contradicted the engine.** It said the validator
  refuses a rule that opens with an optional clause, which is precisely how a
  rule asks what changed *before* a failure; what the validator actually
  refuses is a rule in which every clause is optional.

### Verified

Every published 0.8.0 asset was downloaded, checked against its published
checksums, and then actually run: the fault-injection lab was re-run against the
release binaries rather than a development build and reconstructed all fourteen
faults with correlation naming the cause in eight of them; `install.sh` was
exercised clean, over an existing installation, and on the way out, confirming
it neither reverts an edited rule nor deletes a store; the arm64 build was run
under emulation, where it recorded correctly and — the point of the exercise —
admitted the two collectors that could not start rather than appearing healthy;
and the appliance image was booted in QEMU, where it came up recording and
reported a 31-hour blind spot it had genuinely had.

## 0.8.0 — 2026-08-26

The first version that does everything the project set out to do: record state
changes, tie them to machines across address changes, notice when a working path
stops working, and say what changed just before it.

### The record

- **Layer 1** — interface state through netlink, distinguishing an
  administratively shut port from a dropped carrier. Every other tool reports
  both as "interface down"; they have different causes and different fixes.
- **Layer 2** — the neighbour table. An ARP binding moving under a running
  network is an error rather than a warning when the address is the default
  gateway. Repeated flip-flopping is reported as a contested address.
- **Layer 3** — routing and addressing. A change is reported only when the route
  that actually *wins* for a destination changes, so a standby path being
  installed is not mistaken for traffic moving. `NLM_F_REPLACE` is matched by
  metric, which is how the kernel identifies a route and how routing daemons
  install recomputed paths.
- **Layer 4** — TCP connection outcomes, from an eBPF program on the
  `sock/inet_sock_set_state` tracepoint. `flow.first_failure_for_pair` is the
  strongest single signal in the system: two machines that were connecting
  successfully can no longer complete a handshake.
- **Filtering** — nftables ruleset changes, each rule qualified by its table and
  chain. Comparing bare rule text would call a rule moved between chains "no
  change", which is exactly the sort of edit that breaks a network quietly.
- **Addressing and naming** — DHCP, DNS and ICMP metadata from a raw socket,
  with a kernel-side filter so the recorder is not woken for every frame on the
  segment. A second DHCP server answering is an error, because from that moment
  clients take whichever configuration replies first. A client changing
  resolver is reported for the same reason. **No payloads are ever stored**, and
  query names are off by default: there are networks where recording what
  people looked up is not permitted, and that is not this program's decision to
  make.
- **Reachability** — ICMP probes against the default gateway, or configured
  targets, reporting loss and latency as departures from a measured baseline
  rather than against a fixed threshold. It reports once when a path degrades
  and once when it recovers, not every round.

### Identity

A temporal identity table, so asking about an address as it was last Tuesday
returns the machine that held it *then*, and does not drag in whichever machine
holds it today. An address turning up behind different hardware creates a
different host rather than joining the existing one — merging them is how an
identity table quietly conflates an attacker with its victim.

### Correlation — the Isnad engine

Nineteen rules, as YAML files rather than code. Every link in a chain names the
event it rests on and states whether it *caused* the next one, merely
*correlated* with it, or only *preceded* it. Optional clauses placed before a
rule's first required clause are searched **backwards** in time, nearest first,
which is what lets a rule ask the question the project exists for: the symptom
has arrived, so what changed just before it?

An incident can never be more certain than the observations under it.

### Honesty

The record has to be able to say what it does not know. There are three
distinct ways to miss something and they are separate kinds because they need
separate answers.

- `system.gap` — the recorder was not running, and for exactly how long,
  measured against a heartbeat written every ten seconds.
- `system.drop` — the recorder was running and could not keep up. Every source
  that can lose data reports through it: an eBPF ring buffer that filled, a
  netlink socket the kernel overran, and a packet socket whose buffer
  overflowed. Each says which source and how much.
- `system.collector_down` — the recorder was running, the timeline is unbroken,
  and one source stopped feeding it. This is the dangerous one, because unlike
  a gap there is no interruption to notice: a kernel without BTF means no
  connection collector, and the record would otherwise show no connection
  failures on a host where connections were never being watched. The reason is
  attached, and a rule states plainly what cannot be concluded from the quiet.
- Events the store itself refuses — a full disk, most often — are counted while
  it is broken and admitted in the first write that succeeds. The account
  cannot be written to the thing that is broken, so it waits.
- All of these are exported as Prometheus counters, and all start at zero
  rather than appearing on first failure — a series that springs into existence
  when something breaks cannot be alerted on before it. `netrewind_store_writable`
  is the exception that has to be a gauge: it is the one signal that still works
  when the store is what broke.
- A folded event contributes its occurrence count to metrics, not one, so they
  do not under-report exactly when things are worst.

### Configuration

The recorder is configured from `/etc/netrewind/netrewindd.yaml`, with flags
overriding the file and the file overriding the defaults. Two decisions follow
from the same principle as the rest of the project:

- An unknown key is an error. A misspelled setting that silently does nothing
  is the quietest failure a configuration file has.
- A configuration that would leave the recorder useless is refused rather than
  accepted. `retention: 0s` would have the hourly prune delete the entire
  record, including the incident being investigated. Every problem is reported
  at once, and `--check-config` runs the same checks and exits — which is what
  the systemd unit does before starting.

### Reading it back

`events`, `timeline`, `what-happened`, `incidents`, `rules`, and `serve` — a
server-rendered web interface with no JavaScript, embedded in the same static
binary, read-only, with the blind-spot banner on every page.

Reading is read-only in SQLite's own terms, not by convention: the store is
evidence and the tool that displays it cannot alter it. Nor can it invent it —
querying a path with no store there says so instead of creating an empty one
and reporting "no events in this window", which was indistinguishable from a
quiet network. A misspelled `--family` is refused for the same reason.

### Sending the record elsewhere

Events and incidents can be copied to an OpenTelemetry collector as log
records, so network change lands in whatever pipeline a team already runs. OTLP
over HTTP with JSON bodies, encoded by hand: there is no OpenTelemetry SDK here
for the same reason there is no Prometheus client library, and what goes on the
wire is checked against the specification by tests rather than by trusting a
dependency to be right. Verified against a real collector, not a stub.

The body of each record is the same sentence the CLI prints, so a dashboard and
a terminal cannot disagree about what an event means. An incident carries its
whole chain, including which links claim a cause and which claim only
co-occurrence - losing that distinction in the export would undo the one
discipline the engine is built on.

It is a copy, and the store remains the record. Exporting never blocks
recording, a collector that is down costs nothing but the copy, and what it
missed is counted in `netrewind_otlp_dropped_total` - deliberately a different
metric from the one that means the record itself is incomplete.

### Delivery

Static binaries for `linux/amd64` and `linux/arm64` with no runtime
dependencies, an installer that refuses to overwrite locally edited rules or a
tuned configuration and never deletes the event store, a systemd unit granting
only four capabilities, and a container image. On a Linux host without systemd
the installer says so and explains what to do, rather than failing on a missing
directory.

The store sustains around ten thousand events a second on modest Linux
hardware, twice what the plan called for, and answers queries during the burst.
Repeats fold, so ten thousand transitions of one flapping port become a handful
of rows carrying an occurrence count rather than ten thousand rows.

### Keeping itself current

The recorder checks its own releases once a day and records
`system.update_available` when a newer one exists. Installing it is a separate
setting and is off by default: this output is meant to be evidence, and letting
a machine on your network rewrite its own binary is a change of trust rather
than a convenience.

When it is turned on, the download is checked against the `SHA256SUMS` published
with the release and the new recorder is **run** before anything is replaced -
the check that stands between an update and an appliance that has quietly
stopped recording. The previous binary is kept, edited rules are never
overwritten, and the swap is recorded as `system.updated` so the restart either
side of it is explained rather than being unexplained silence.

An ed25519 public key can be configured, and then a release whose checksums are
not signed by it is refused. Checksums alone prove the file arrived as GitHub
served it; they do not prove who built it, and that difference is worth being
plain about.

### The appliance

A bootable disk image that comes up recording: write it to a USB stick, plug a
spare machine into the segment, and there is nothing to install and nothing to
configure before there is a record. Alpine, syslinux, OpenRC, 178 MB
compressed. On first boot it grows the filesystem to whatever disk it was
written to, takes an observer id from its own hardware address, and brings
every interface up on DHCP - because an appliance is plugged into somebody
else's segment, and needing an address assigned first is asking for work during
the incident that caused it to be plugged in.

There is no sshd. The console is the only way in, which is deliberate for a box
whose job is to watch a network it has no reason to trust.

### Proven by

A synthetic lab that builds a topology in network namespaces, injects fourteen
faults — a flapping port, a hijacked gateway, a contested address, a re-pointed
route, a vanished default, a service that will not answer, a working path broken
by an ARP change and another broken by a filtering rule, a rogue DHCP server, a
redirected resolver, and measured packet loss — and checks both that every one
can be found in the record afterwards and that correlation named the cause. It
runs in CI, alongside a load job that verifies the store absorbs five thousand
events a second and a govulncheck job. 235 tests.

Beyond the lab, each of these was run rather than assumed: the release tarball
unpacked and installed onto a clean host; the container image built, started,
recorded, and served its web interface; a fresh clone built, vetted and tested
with an empty cache; both eBPF programs confirmed loaded and JIT-compiled in
the kernel with bpftool; a recorder stopped and restarted to confirm the gap is
measured and reported; and the whole history scanned for real addresses,
secrets and personal paths before any of it becomes public.

### Not done

- conntrack. The `sock/inet_sock_set_state` tracepoint sees sockets on *this*
  host, so a recorder placed at the gateway watches its own connections and not
  the ones it merely forwards between other machines. Connection observation is
  therefore host-local today. conntrack is what would extend it to forwarded
  traffic, and to UDP, which has no sockets to watch.
- LLDP topology, and SNMP for switch state
- Multiple recorders on one network
