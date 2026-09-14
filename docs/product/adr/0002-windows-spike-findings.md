# ADR 0002 — Windows IP Helper spike: findings, not yet a decision to build

**Status:** Spike complete, isolated (`spikes/windows-ip-helper/`), not integrated.
**Date:** 2026-09-13.

## Context

`PRODUCT_RELEASE_PLAN_AR.md` §5, Phase 5A, calls for exactly one thing before
any Windows collector code is written against `internal/ports` or
`internal/collect`: **"spike معزول، لا تدمجه مبكراً"** - an isolated spike,
do not integrate it early. The plan's own words on what such a spike is for:
Microsoft's docs confirm notifications exist for interfaces, routes, and
unicast addresses, "لكنه لا يجعلها مكافئة لـnetlink تلقائياً" - but that does
not automatically make them equivalent to netlink; the spike has to test
loss, callbacks, IPv4/IPv6, sleep/resume, and VPN, on a real machine, not
just cite the API reference.

This ADR reports what actually happened running that spike on this real
Windows 11 machine (the development machine, the same one running this
session), not what the Microsoft documentation promises in the abstract. The
code lives at `spikes/windows-ip-helper/`, its own Go module, and touches
nothing under `internal/`, `cmd/`, or the root `go.mod`/`go.sum` - another
engineer was concurrently modifying `internal/collect/netlink` and
`internal/analyze` in this same repo during this work, and this spike has no
import path that reaches either.

### Safety boundary honoured

This machine's real network adapter is the operator's actual working
internet connection. The spike **registers for notifications and observes
passively only**. It never calls `netsh`, never calls any IP Helper
"Set"/"Create"/"Delete" function, and - deliberately, see Decision below -
never even added or removed a loopback-only address to force a guaranteed
test notification, even though that specific technique was offered as an
acceptable, optional option. The product plan's own Phase 5B testing section
reserves *active* network-change testing for an isolated VM with a
snapshot to restore ("تجرى تغييرات الشبكة فقط داخل VM معزولة ثم تستعاد
snapshot") - not the bare development machine this spike ran on. Skipping
the active test here is therefore consistent with the plan's own intended
division of labour between 5A (passive, bare machine, cheap) and 5B (active,
isolated VM, safe-by-construction), not just an extra-cautious one-off
choice for this spike.

## What was built

`spikes/windows-ip-helper/` - its own Go module
(`github.com/OmarAlghafri/netrewind/spikes/windows-ip-helper`), depending on
`golang.org/x/sys v0.47.0`, the exact version already pinned by the root
module's `go.mod`. One file, `main.go` (`//go:build windows`):

- Seeds and prints, once at startup: every interface (`GetIfTable2Ex`), every
  unicast address (`GetUnicastIpAddressTable`), every route
  (`GetIpForwardTable2`) - the Windows equivalent of the "seed" step
  `internal/collect/netlink.LinkCollector.seed`/`RouteCollector.Run` does
  with `nl.LinkList()`/`nl.RouteList()`.
- Registers for change notifications on all three: `NotifyIpInterfaceChange`,
  `NotifyRouteChange2`, `NotifyUnicastIpAddressChange`, each with
  `initialNotification: true`.
- Logs every callback invocation with a real wall-clock timestamp and
  whatever the callback's row argument contains, for a bounded window
  (`-duration`, run at both 25s and 60s), then cancels all three
  registrations (`CancelMibChangeNotify2`) and prints a summary.

Raw output of both runs is kept next to the code, not committed as a
markdown claim: `spikes/windows-ip-helper/run1-observation-25s.txt` and
`run2-observation-60s.txt` (`*.log` is repo-gitignored, hence the `.txt`
names - these are the actual run transcripts, unedited except for the
renames).

### A real finding before any notification code: the binding already exists

The task brief raised the possibility of needing raw `iphlpapi.dll` syscalls
via `syscall.NewLazyDLL` if `golang.org/x/sys/windows` didn't expose these
APIs. It does. Checking the actual vendored source
(`$(go env GOMODCACHE)/golang.org/x/sys@v0.47.0/windows/syscall_windows.go`
and `zsyscall_windows.go`) shows `NotifyIpInterfaceChange`,
`NotifyRouteChange2`, `NotifyUnicastIpAddressChange`,
`CancelMibChangeNotify2`, `GetIfTable2Ex`, `GetIpForwardTable2`,
`GetUnicastIpAddressTable`, and `FreeMibTable` are all already bound as real
Go functions calling into `iphlpapi.dll`, with matching struct types
(`MibIfRow2`/`MibIfTable2`, `MibIpForwardRow2`/`Table2`,
`MibUnicastIpAddressRow`/`Table`, `MibIpInterfaceRow`) in `types_windows.go`.
No raw syscall declarations were needed. One rough edge: `MibIfTable2` and
`MibUnicastIpAddressTable` have no `.Rows()` convenience method in this
x/sys version (only `MibIpForwardTable2` does) - the other two need the same
`unsafe.Slice(&table.Table[0], table.NumEntries)` reslice done by hand.

## What was observed

### Seed step: comparable shape to Linux, with one Windows-specific problem

Counts from this real machine: **66 interfaces**, **16 unicast addresses**,
**57 routes** (`run1-observation-25s.txt` lines 5, 74, 93).

The address and route tables are a close match to what
`internal/collect/netlink`'s `AddrCollector`/`RouteCollector` already
produce conceptually: an index, a family-agnostic address/prefix, a next
hop, a metric. IPv4 and IPv6 came back through the *same* API call
(`family: AF_UNSPEC`) and the *same* Go struct shape, discriminated only by
a `Family` field on the socket address union - no separate IPv6 code path
was needed to seed either addresses or routes. A real IPv6 default route was
present and printed correctly: `dest=::/0 nextHop=fe80::523d:d1ff:fe59:aaec
ifIndex=8` (the Wi-Fi adapter), alongside the IPv4 default route on the same
interface.

The interface table is where Windows and Linux diverge sharply, in a way
that matters for a future adapter. Linux's `nl.LinkList()` returns one entry
per real interface NetRewind would care about (plus veth/tun/etc., already
handled by the existing `OperUnknown`-as-up carve-out in
`isOperUp` in `internal/collect/netlink/link_linux.go`). Windows'
`GetIfTable2Ex` returned **66** rows for what is, physically, about
**9** real network paths on this machine (Wi-Fi, Ethernet, three "Local Area
Connection* N" virtual/tunnel adapters, a Hyper-V vSwitch, a Bluetooth PAN,
a software loopback, plus several fully-disabled pseudo-adapters like
Teredo/6to4/IP-HTTPS). Each real adapter is shadowed by five or six
LWF (Light-Weight Filter) shim pseudo-interfaces that Npcap, Windows
Filtering Platform, QoS Packet Scheduler, and VirtualBox's NDIS filter each
register as *their own* interface with *their own* `ifIndex`/`InterfaceLuid`
(e.g. `ifIndex=26 alias="Local Area Connection* 8-WFP Native MAC Layer
LightWeight Filter-0000"` right next to the real `ifIndex=12 alias="Local
Area Connection* 8"`). A naive seed-and-diff over every row from
`GetIfTable2Ex` would produce roughly 6-7x the noise a real adapter change
should, on this machine. This is a genuinely different problem from
anything `internal/analyze/interface.go` has had to handle - it is not
solved by that package's `OperUnknown` carve-out, and would need its own
filtering rule at the adapter boundary (matching `PRODUCT_RELEASE_PLAN_AR.md`
§4.1's rule that adapters translate, they do not get to change what counts
as a change - so this would have to be "which rows are real interfaces",
decided once at the adapter, not folded into the shared analyzer).

**Update, a tested filter now exists** (`docs/evidence/19-windows-interface-filtering.log`,
`spikes/windows-ip-helper/filter.go` + `filter_test.go`): a two-stage rule —
(1) drop rows whose alias ends in one of 9 known NDIS/WFP/Npcap/VirtualBox/
Wi-Fi-filter/Hyper-V-extension shadow suffixes, (2) drop rows that are
permanent, always-absent scaffolding (`MTU == 0 && OperStatus ==
notPresent` - Teredo, 6to4, IP-HTTPS, the disabled Kernel Debugger NIC) -
tested against the complete real 66-row table as a Go fixture, not a
sample. Real, measured result: **45 shim rows + 4 scaffolding rows dropped,
17 kept**. This corrects this ADR's own earlier prose estimate of "~9 real
paths ... three 'Local Area Connection* N' adapters": the real data shows
**six** distinct numbered LAC* adapters (7-12), not three - the original
estimate under-counted that family. 17, not 9, is the honest number a
`WindowsLinkAdapter` should expect after this filtering stage.

Deliberately NOT resolved by this filter: four further "Local Area
Connection* N" rows (3-6, real distinct MTUs, type 131, currently down,
`MediaConnectState` "unknown") are kept rather than dropped, for lack of
positive evidence they are noise rather than real (if disused) adapters -
possibly GNS3/VirtualBox host-only virtual NICs, not established here. Left
as an honest open question for 5B, not guessed at either way.

### Admin vs. carrier: Windows has the distinction, and this run has a live example of it

`internal/ports.InterfaceObservation` deliberately keeps `AdminUp` and
`OperUp` separate, because `internal/analyze/interface.go` found and fixed a
real bug that only reproduced when carrier dropped independently of admin
state (see that file's `Observe` method doc comment). Windows' `MibIfRow2`
exposes *three* relevant fields, not two: `AdminStatus` (`IF_ADMIN_STATUS`:
up/down/testing), `OperStatus` (`IF_OPER_STATUS`: up/down/testing/unknown/
dormant/notPresent/lowerLayerDown - a richer enum than Linux's operstate
string), and `MediaConnectState` (`NDIS_MEDIA_CONNECT_STATE`:
unknown/connected/disconnected - the actual carrier-detect signal,
independent of both of the others).

This run did not need to force a carrier-down event to see the distinction
in practice - this machine already has one, sitting idle: the Bluetooth PAN
adapter.

```
ifIndex=10 alias="Bluetooth Network Connection 2" mtu=1500
  adminStatus=up operStatus=down mediaConnectState=disconnected
```

Administratively enabled, but no carrier (no paired device connected) -
exactly the case that made the old netlink collector's down-transition logic
(`!cur.adminUp && !curOperUp`, requiring *both* signals false) silently swallow
real carrier-only outages, per `link_linux.go`'s comment on the bug. Windows
would let a `WindowsLinkAdapter` build the same `cause: "carrier"` vs.
`cause: "administrative"` distinction `internal/analyze/interface.go`
computes today, from `MediaConnectState` and `AdminStatus` respectively,
without collapsing them - and arguably with *more* fidelity than Linux,
since `OperStatus`'s `lowerLayerDown`/`notPresent`/`dormant` values are finer
than `AdminUp && OperUp` alone. Translating this three-way Windows shape down
to the two-bool `InterfaceObservation` type is a real design decision for
5B (which of the three wins when they disagree is not obvious for every
combination), but nothing observed here suggests Windows lacks the
information Linux has - if anything it has more of it.

One caveat worth stating plainly: `MediaConnectState` was `unknown` (not
`connected`/`disconnected`) on several adapter types in this seed - notably
the PPP-type adapter `ifIndex=21 alias="Local Area Connection* 7" type=23
(IF_TYPE_PPP)` and the disabled tunnel pseudo-adapters (Teredo, 6to4,
IP-HTTPS, type 131). Media-connect state evidently is not a meaningful
signal for every interface type, the same way `OperUnknown` is not a
meaningful "down" signal for every Linux virtual interface - this will need
its own per-`Type` handling in a real adapter, not a blanket rule.

MTU: directly available (`Mtu` field), and reads `0` exactly on the
interfaces that are administratively down and not present (Teredo, 6to4,
IP-HTTPS, "Ethernet (Kernel Debugger)") - consistent with
`internal/ports.InterfaceObservation.MTU`'s documented convention that `0`
means "unknown," not "changed to zero."

### Notification registration: works, with an unexplained anomaly worth flagging

All three `Notify*Change2` calls returned success (no error) on both runs
and produced a valid notification handle, immediately cancellable via
`CancelMibChangeNotify2` with no error. That much is a clean, reproducible
"yes, the registration mechanism works on this machine, this Go binary,
this x/sys version."

The way each proved it fired is `initialNotification: true`: Microsoft
documents that this makes the API call the callback once immediately, with
a `nil` row and `NotificationType == MibInitialNotification`, purely to
prove the registration is alive before any real event happens - which is
exactly the safe, passive proof-of-life this spike needed, since it commits
to making zero real network changes.

What actually happened, identically on both the 25s and the 60s run: **each
of the three registrations fired its initial notification twice, not
once**:

```
NOTIFY iface: type=InitialNotification row=nil
NOTIFY iface: type=InitialNotification row=nil
NotifyIpInterfaceChange: registered ok, ...
NOTIFY addr: type=InitialNotification row=nil
NOTIFY addr: type=InitialNotification row=nil
NotifyUnicastIpAddressChange: registered ok, ...
NOTIFY route: type=InitialNotification row=nil
NOTIFY route: type=InitialNotification row=nil
NotifyRouteChange2: registered ok, ...
```

(6 total notifications logged, both runs, all `InitialNotification`, all
`row=nil` - see the `total notifications received: 6` summary line in both
`.txt` files.) This is not what the API reference describes (one synthetic
callback per registration).

**Update, confirmed by a follow-up run** (`docs/evidence/18-windows-notification-family-split.log`,
`spikes/windows-ip-helper/run3-split-family-20s.txt` - this was the "obvious
next check" this ADR originally deferred): re-running with a new
`-split-family` flag that registers each of the three notification types
**twice**, once explicitly with `AF_INET` and once with `AF_INET6`, instead
of once with `AF_UNSPEC`, produced exactly **one** initial-notification
callback per `(type, family)` pair - 3 types × 2 families = 6 total, matching
the original count but now spread one-per-family instead of two-on-one-handle.
This confirms the leading hypothesis directly: `AF_UNSPEC` is not a distinct
registration mode, it is sugar for "register once per real address family
internally," and each internal per-family registration produces its own
synthetic initial callback, both delivered through the single handle/callback
the `AF_UNSPEC` caller sees. Not a bug, not unexplained anymore.

This does **not** resolve whether the same per-family duplication applies to
a REAL (non-initial) notification under `AF_UNSPEC` - e.g. would a single
real address change fire the callback once or twice. That remains untested
(no real change occurred in this run either, same passive-only safety
boundary) and is the correctly-narrowed open question going into 5B: a
`WindowsLinkAdapter` registering with `AF_UNSPEC` should not assume
notification-count equals real-event-count, and should either de-duplicate
(the same category of problem `newOverflowReporter` already handles for
netlink) or register per-family explicitly and merge - now known to work
cleanly for at least the initial notification, at the cost of one extra
handle per type (6 instead of 3), which is cheap.

### The null result: no real change fired, on a live, in-use machine

Beyond the six synthetic initial notifications, **zero** further
notifications arrived in either the 25-second or the 60-second window, on a
machine with an active Wi-Fi connection, a Hyper-V vSwitch, VirtualBox NDIS
filters, and Npcap installed - i.e., a reasonably "busy" real Windows
network stack, not an idle VM. Checked directly rather than assumed:

- System uptime at the time of the second run was about 5h41m
  (`LastBootUpTime` 2026-09-13 17:23:25, run at 23:04) - a single continuous
  session, no sleep/resume happened during or before this run to produce a
  transition to observe.
- `Get-VpnConnection` returned no configured VPN connections, and no
  adapter matching VPN/WireGuard/OpenVPN/TAP/PPP naming was active - so a
  VPN connect/disconnect had no chance to occur either. (The PPP-type
  pseudo-adapter seen in the interface seed, `ifIndex=21`, was present but
  disconnected the whole time - Windows keeps this kind of adapter
  provisioned but idle even with no VPN in use.)

This is exactly the kind of honest null result the task brief anticipated
("قد لا يحدث شيء، وهذا مقبول ومفيد للتقرير" in spirit, even if not its exact
words) - it is informative, not a failure: it proves the registration path
is live (via the synthetic notification) but it does **not** independently
confirm that a real interface/address/route change is actually delivered
end-to-end through this callback mechanism on this machine. Nothing here
contradicts that it would be; nothing here proves it either.

## Decision

**Do not perform the active loopback-alias test on this machine, even though
it was offered as an acceptable, optional, low-risk option.** The spike
stops at passive observation plus the synthetic initial-notification proof
of registration. This is a deliberate scope decision, not a limitation
discovered too late to work around:

1. The product plan's own Phase 5B section reserves active network-change
   testing for an isolated VM with a restorable snapshot. Doing it here
   instead, on the operator's real working machine, would be solving 5A's
   problem with 5B's tool.
2. A `netsh interface ip add address` / `delete address` call, even scoped
   to the loopback pseudo-interface, is still a real, if narrow, network
   configuration change made unsupervised on a machine whose only network
   path is the operator's actual internet connection. The upside (one
   additional confirmed notification, on top of six already-confirmed
   synthetic ones proving the same callback plumbing) did not clear that
   bar for this spike.
3. Nothing about *this* decision changes the honest bottom line below - a
   confirmed real-change notification is still the single most important
   piece of missing evidence, and it is explicitly deferred to a proper
   Phase 5B VM run, not silently dropped.

## Bottom line: is this enough to build a real `WindowsLinkAdapter`?

**Not yet, and this spike should not be read as clearing that bar - but
nothing found here is a red flag either.** Specifically:

**Encouraging, confirmed on this real machine:**
- The exact IP Helper functions the plan named
  (`NotifyIpInterfaceChange`/`NotifyRouteChange2`/`NotifyUnicastIpAddressChange`
  plus the `GetIfTable2Ex`/`GetIpForwardTable2`/`GetUnicastIpAddressTable`
  seed calls) are already bound in `golang.org/x/sys v0.47.0`, the version
  this repo already pins - no vendoring or raw syscalls needed.
- Registration, with real handles, succeeds without error, and cancels
  cleanly.
- IPv4 and IPv6 go through the identical API and struct shape - a future
  adapter does not need parallel v4/v6 code paths the way the seed/notify
  layer is concerned.
- The AdminStatus/OperStatus/MediaConnectState split gives at least as much
  admin-vs-carrier fidelity as `internal/ports.InterfaceObservation` needs,
  demonstrated against a real carrier-down adapter on this machine, not a
  hypothetical.
- MTU and the "0 means unknown" convention line up with the existing
  `ports.InterfaceObservation` contract with no translation surprises.

**Update, the active-change run has now happened**
(`docs/evidence/26-windows-real-notifications.log`): on a separate, physical
Windows 11 test machine, a scripted sequence of real changes (adapter
admin down/up on a physical NIC and on a virtual one, IPv4 address
add/remove, route add/remove, a burst of ten addresses added and removed
back-to-back) was applied twice, once under `AF_UNSPEC` registration and
once with per-family registration, with the spike's new `-requery` flag
fetching each notification's real row. 103 and 100 real notifications
respectively, with every substantive per-step count identical between the
two. The list below is updated in place.

**Not established by this run, and not to be assumed true from the docs
alone:**
- ~~Whether a genuine interface/address/route change is actually delivered
  through these callbacks end-to-end~~ **CONFIRMED** (log 26): interface,
  address and route changes, IPv4 and IPv6, `AddInstance`/`DeleteInstance`/
  `ParameterChange`, all delivered, tens of milliseconds after the change.
  Two things the run added that the docs alone would not have: the Row a
  callback receives is a key only (`connected=false`, `dadState=0` on
  every single one - re-query by `InterfaceLuid`/address to get the real
  state, and treat "Element not found" on that re-query as "already gone",
  because it races the change itself); and an address is `AddInstance` at
  Tentative and only `ParameterChange` (plus its /32 host route) ~3 s later
  when DAD completes - `l3.addr_added` semantics belong at the latter.
- ~~What causes the double initial-notification per registration~~
  **CONFIRMED** (see the family-split update above and
  `docs/evidence/18-windows-notification-family-split.log`): `AF_UNSPEC`
  registers once per real address family internally, each with its own
  synthetic initial callback. ~~What remains open, narrowed: whether that same
  per-family duplication also affects REAL (non-initial) notifications~~
  **CONFIRMED it does not** (log 26): a burst of ten address adds produced
  exactly ten `AddInstance` callbacks under `AF_UNSPEC`, and five route
  deletes that the per-family run reports as 2 (v4) + 3 (v6) arrived as
  five, not ten, through the single `AF_UNSPEC` handle. Register once with
  `AF_UNSPEC`; no de-duplication needed. (Interface notifications DO come
  as one row per IP family - `family=2` and `family=23` for the same LUID -
  in both modes; that is the API's data model, and the adapter folds them
  by LUID into one `InterfaceObservation`.)
- Sleep/resume behaviour - still not tested: the test machine is reached
  over Wi-Fi and a laptop may not wake remotely; needs a local console or
  a VM with ACPI control.
- VPN connect/disconnect behaviour - still not tested: no VPN client or
  configuration exists on either machine, and none should be invented.
- ~~Loss behaviour under a burst of rapid changes~~ **Observed at a modest
  scale** (log 26): 20 events in ~1.6 s (ten adds, ten removes), all
  delivered, none coalesced, under both registrations. Not a stress test -
  nothing here says what happens at thousands per second, and the
  netlink collector's `newOverflowReporter` still has no `iphlpapi`
  counterpart to compare against.
- Spontaneous `ParameterChange` notifications on an interface nobody
  touched (the test machine's Wi-Fi fired pairs of them before, during
  and after the sequence, at different points in the two runs). Not a
  problem, but a fact an adapter must be designed around: a parameter
  change is not a link change.
- ~~The right filtering rule for the ~6-7x interface-row noise problem~~
  **A tested filter now exists** (see the update above and
  `docs/evidence/19-windows-interface-filtering.log`): 45 shim + 4
  scaffolding rows dropped, 17 kept, tested against the real 66-row table.
  Still not tested against a real transition (no real change occurred in
  any run so far) - only against the static seed snapshot.

**Recommendation (revised after log 26):** the evidence needed to START a
real `WindowsLinkAdapter` against `internal/ports` now exists: the binding,
registration, real delivery of interface/address/route changes in both
families, `AF_UNSPEC` semantics, a tested interface-row filter, and modest
burst behaviour are all observed on real machines. The design constraints
that adapter must honour are now concrete rather than hypothetical:
register once with `AF_UNSPEC`; key on `InterfaceLuid` and fold the v4/v6
interface rows into one observation; re-query every notification's row and
treat not-found as gone; record address arrival at DAD completion; ignore
`ParameterChange` on interfaces whose re-queried admin/oper/media state did
not change; apply the log 19 filter to the seed table. Still to be tested
before that adapter is called done, not before it is started: sleep/resume
and VPN transitions (need a console or an ACPI-controllable VM, and a VPN
configuration that actually exists), and a real high-rate burst.

## Verification

- `cd spikes/windows-ip-helper && go vet ./...`: clean.
- `cd spikes/windows-ip-helper && go build -o spike.exe .`: succeeds
  (Windows/amd64, this machine).
- `./spike.exe -duration=25s` and `./spike.exe -duration=60s`: both ran to
  completion, exit code 0, real output preserved at
  `spikes/windows-ip-helper/run1-observation-25s.txt` and
  `run2-observation-60s.txt`.
- `cd "<repo root>" && go vet ./...`: still clean after adding
  `spikes/windows-ip-helper/` - confirmed the new nested module (its own
  `go.mod`) is invisible to the root module's build graph, so this spike
  cannot have touched anything the concurrently-in-progress
  `internal/collect/netlink`/`internal/analyze` work depends on.
- `git status --porcelain` at repo root before and after: only new files
  under `spikes/` were added by this work; `go.mod`, `go.sum`, and
  everything under `internal/`, `cmd/`, `desktop/` already showed as
  modified/untracked by other, concurrent work before this spike started,
  and remain exactly as they were.
