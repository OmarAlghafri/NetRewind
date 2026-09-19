# The event schema

Everything NetRewind records is an `Event`. This document is the contract: the
collectors produce these, the store keeps them, the correlation engine reads
them, and the CLI renders them. It is versioned by `schema_v`, currently `1`.

## Five principles

1. **An event is a state change or a measured observation, never a packet.**
   Recording deltas rather than traffic is what makes a week of history fit in
   gigabytes instead of terabytes, and it is why most of what is stored is
   directly useful during an incident rather than needing to be mined for.
2. **Every event is correlatable.** It carries identities that can be matched
   across sources, which is the whole basis of the causal chain.
3. **Every event carries its evidence.** Enough of the raw observation to show
   a human why we believe it, without going back to another tool.
4. **One strict notion of time.** Wall clock and monotonic clock, nanoseconds,
   UTC. See below for why both.
5. **The recorder admits its blind spots.** A gap that is not recorded is
   indistinguishable from a period in which nothing happened. This is the
   difference between a log and evidence.

## The common envelope

Carried by every event without exception. Defined in
[`internal/event/event.go`](../internal/event/event.go).

| Field | Type | Meaning |
|---|---|---|
| `event_id` | ULID | Unique, and lexically sortable by creation time |
| `schema_v` | uint16 | Envelope version |
| `ts_wall` | int64 | Nanoseconds since the Unix epoch, UTC |
| `ts_mono` | int64 | Nanoseconds on the observer's monotonic clock |
| `observer_id` | string | Which recorder saw this |
| `source` | enum | `netlink`, `ebpf`, `conntrack`, `nftables`, `dhcp`, `dns`, `lldp`, `snmp`, `probe`, `config`, `internal` |
| `kind` | string | `family.action`, e.g. `l2.arp_binding_changed` |
| `severity` | enum | `info`, `notice`, `warn`, `error` |
| `confidence` | uint8 | 100 when observed directly, lower when inferred |
| `subject` | EntityRef | The entity the event is about |
| `related` | EntityRef[] | Other entities involved |
| `attrs` | object | Fields specific to this `kind` |
| `evidence` | object | The trimmed raw observation |
| `dedup_key` | string | Folds repeats of the same fact together |
| `count` | uint32 | How many repeats were folded |

### Why two clocks

`ts_wall` answers *when did this happen* and is what a human queries by. It can
jump: NTP corrects it, a VM is resumed, someone sets the date by hand.
`ts_mono` answers *in what order did this happen* and never jumps, but is
meaningless across a reboot.

Recording both is what lets the timeline stay correctly ordered through a clock
correction instead of quietly reordering itself, and it is how
`system.clock_step` is detected: a wall-clock movement the monotonic clock did
not see.

## The identity model

The hardest decision in the schema. An IP address moves. A MAC is forged, and
modern phones randomise it per network. A hostname may not exist. So how do you
say *the same machine* across a week?

Not by storing an address on the event. Events reference a **stable `host_id`**
resolved at ingest time against a temporal identity table:

```
identity_binding
  host_id     stable ULID
  attr_type   mac | ipv4 | ipv6 | hostname | switch_port | dhcp_client_id
  attr_value  string
  valid_from  ts
  valid_to    ts | NULL       -- NULL means still current
  confidence  uint8
```

`EntityRef.ID` holds the resolved identifier and is empty until resolution has
run. `EntityRef.Label` is what a human should read. `EntityRef.Attrs` keeps the
raw observations - mac, ip, ifindex - so an event stays interpretable even if
resolution later turns out to have been wrong.

Six entity kinds: `host`, `iface`, `device`, `subnet`/`vlan`, `service`, `flow`,
plus `observer` for the `system.*` family.

> **Known limitation.** MAC randomisation breaks long-term tracking of mobile
> devices. DHCP client-id and behavioural fingerprinting narrow the gap; they do
> not close it. This is stated rather than papered over.

## The nine families

`⭐` marks the events that open most incidents.

### `link.*` — layer 1, from netlink

`up` · `down` · `flap` · `mtu_changed` · `error_rate_high`

Administrative state (someone shut the port) is distinguished from operational
state (the carrier dropped) via `attrs.cause`. Reporting both as "interface
down" is what makes existing tools useless mid-incident: the two have completely
different causes and completely different fixes.

### `l2.*` — layer 2, from netlink neighbours, eBPF and LLDP

`arp_binding_new` · **`arp_binding_changed`** ⭐ · `mac_moved` · `duplicate_ip` ·
`neighbor_failed` · `lldp_neighbor_changed` · `vlan_seen`

### `l3.*` — layer 3, from netlink routes and ICMP

`route_added` · `route_removed` · `route_changed` ·
**`default_route_changed`** ⭐ · `addr_added` · `addr_removed` ·
`icmp_unreachable` · `mtu_blackhole`

### `flow.*` — layer 4, from eBPF and conntrack

**`first_failure_for_pair`** ⭐⭐ · **`handshake_fail`** ⭐ · `rollup` ·
`reset` · `timeout_no_close` — all from two eBPF tracepoints.
`retransmit_spike` is the one still outstanding; it needs
`tcp_retransmit_skb`, which is not attached.

There is no `open` or `close`. Recording that a connection began and ended
would be recording traffic rather than change, which is the line this schema
draws everywhere else, and a busy segment would bury everything worth keeping
under it. What is kept is how connections *ended badly*.

`first_failure_for_pair` is the strongest single signal in the system. An
unanswered connection on its own is ordinary — closed ports are closed. An
unanswered connection between two machines that *were talking a moment ago*
means something changed: a filtering rule, an ACL, a route, a service.

The pair is (source, destination, destination port) — the ephemeral source port
is not part of the relationship. A success is remembered for 24 hours, because a
service that last worked a month ago is not evidence that anything just changed.
It is reported once per break and re-armed the moment the pair works again, so a
fault that recurs after a recovery is reported afresh rather than swallowed.

> **Known limitation.** The memory of which pairs worked is in-process. After a
> restart the recorder has no baseline and reports ordinary `handshake_fail`
> until it sees a pair succeed again.

The source is an eBPF program on the `sock/inet_sock_set_state` tracepoint. A
stable tracepoint rather than a kprobe: kprobes break silently when the kernel
renames or inlines a function, and a recorder that stops recording without
saying so is the failure mode this project exists to prevent.

A connection going straight from the opening SYN to closed was never answered.
Something refused it, dropped it, or was not listening — and that single signal
is what tells a filtering change, a dead service and a broken path apart from
the client's point of view, which nothing at layer 3 can do.

> **Cardinality decision.** Individual connections are *not* recorded as events.
> A busy segment opens thousands a second, and recording each would fill the
> store in a day while telling an operator nothing a counter could not. Only
> anomalies earn a row; everything else is aggregated into a `flow.rollup` every
> 10 seconds. Failures fold by destination and port, so a port scan cannot flood
> the store either.

> **Not a connection.** A socket entering or leaving `LISTEN` is a service
> starting or stopping, not a connection opening or closing, and is ignored.

### `dhcp.*` and `dns.*` — naming and addressing, metadata only

`dhcp.offer` · `ack` · `nak` · **`dhcp.server_seen`** ⭐ · `lease_changed`
`dns.query_fail` · **`dns.resolver_changed`** ⭐ · `dns.latency_spike`

Never payloads. Query names are recorded behind a switch that can be turned off
entirely, because there are networks where recording them is not permitted.

### `policy.*` — the filtering rules in force, from nftables

**`rule_changed`** — implemented. `drop_burst` — planned, and needing rule
counters rather than the ruleset itself.

The collector records that the ruleset changed and what changed in it, never
what the right ruleset would be. Each rule is qualified by the table and chain
containing it, so the evidence reads
`table inet filter / chain input :: tcp dport 9300 drop`. Comparing bare rule
text would report a rule moved from one chain to another as no change at all —
and that is exactly the sort of edit that breaks a network quietly.

Adding a rule that drops or rejects is a `warn`; adding one that logs or counts
is a `notice`. Rule handles and packet counters are stripped before comparison:
they move constantly on a live firewall without the policy moving, and a
collector that cried wolf every few seconds would stop being read.

> **Known limitation.** This collector polls every five seconds, so a change
> made and reverted inside one interval is invisible. Parsing `nft monitor`
> output would catch it, and would break whenever `nft` changes how it prints.

The corresponding *consequence* — the first failure between two endpoints that
were previously talking — lives in `flow.first_failure_for_pair` ⭐⭐, because it
is observable no matter what did the blocking: a rule here, an ACL on a switch,
or a firewall three hops away.

### `metric.*` — measured series

RTT p50/p95, loss, interface bps/errors/drops, conntrack table size. Stored as
series on the same axis, not as events; they generate `metric.anomaly` when they
depart from baseline.

### `change.*` — intended changes

`config_applied` · `device_reboot` · `admin_action`

Fed from NetIntent, or from diffing device configuration over SSH/SNMP. This is
the family that lets the timeline answer *what were we doing when it broke*.

### `system.*` — the recorder reporting on itself

`gap` · `drop` · `clock_step` · `start` · `stop` · `collector_down` ·
`update_available` · `updated` · `update_failed`

Never remove these. A recorder that silently omits what it missed is not
evidence of anything: the record would show a quiet network, and a quiet
network is what an operator concludes when nothing is wrong.

There are three distinct ways to miss something, and they are separate kinds
because they need separate answers.

`system.gap` is the recorder not running, measured against a heartbeat written
every ten seconds. Nothing at all was recorded for that period.

`system.drop` is the recorder running but unable to keep up. Every source that
can lose data reports through it: an eBPF ring buffer that filled, a netlink
socket the kernel overran, or a packet socket whose buffer overflowed. Each
says which source and how much, because losing a tenth of the frames on one
interface is a different problem from losing three netlink messages.

`system.collector_down` is the dangerous one. The recorder is running, the
timeline is unbroken, and one source stopped feeding it — so a whole family of
events is missing with nothing to mark the absence. Unlike a gap there is no
interruption to notice. This is what a kernel without BTF, or a missing
capability, produces, and without it the record would show no connection
failures on a host where connections were never being watched.


## Every kind, in one place

The prose above explains why each family exists. This is the list, and a test
fails if it and `internal/event/kinds.go` ever disagree. Seven of them are
declared but not yet produced, and are marked *reserved*: a schema that
promises what the code does not do is the failure this project is built
against, so the gap is written down rather than glossed over.

The desktop GUI shows a human name for each kind, in English and Arabic,
next to this same technical code - generated from `kinds.go` into
`desktop/src/i18n/generated/kinds.json` (`go run ./internal/event/gen`)
and kept in step with it by `internal/event/kind_catalogue_test.go`, the
same guard that keeps this table honest applied to the GUI's own copy.

| Kind | Source | What it means |
|---|---|---|
| `link.up` | netlink | An interface started carrying |
| `link.down` | netlink | An interface stopped carrying, or was taken down |
| `link.flap` | netlink | Three carrier losses inside five minutes: the link itself is faulty |
| `link.mtu_changed` | netlink | The MTU changed, which breaks large transfers while leaving ping working |
| `link.error_rate_high` | netlink | Interface counters show over 1% of packets in error |
| `l2.arp_binding_new` | netlink | An address answered at layer 2 for the first time. Carries `address_changed_hands` when the identity table has seen it before on other hardware, which is a substitution rather than an arrival |
| `l2.arp_binding_changed` ⭐ | netlink | A different machine now answers for an address |
| `l2.mac_moved` | netlink | The same hardware address is now behind a different interface |
| `l2.duplicate_ip` | netlink | One address is being claimed by two machines |
| `l2.neighbor_failed` | netlink | An address stopped answering at layer 2 entirely |
| `l2.lldp_neighbor_changed` | lldp | *Reserved:* needs LLDP, which needs switches |
| `l2.vlan_seen` | wire | *Reserved:* needs a trunk carrying more than one VLAN |
| `l3.route_added` | netlink | A new route won for a destination |
| `l3.route_removed` | netlink | The last route to a destination went away |
| `l3.route_changed` | netlink | Traffic for a destination now takes a different path |
| `l3.default_route_changed` ⭐ | netlink | Everything beyond the local segment now goes somewhere else |
| `l3.addr_added` | netlink | An interface gained an address |
| `l3.addr_removed` | netlink | An interface lost an address |
| `l3.icmp_unreachable` | wire | Something on the path said it could not deliver |
| `l3.mtu_blackhole` | wire | Fragmentation was needed and forbidden: large packets vanish, ping works |
| `flow.reset` | ebpf | A connection was refused or torn down by a reset |
| `flow.timeout_no_close` | ebpf | An established connection ended without either side closing it |
| `flow.retransmit_spike` | ebpf | *Reserved:* needs the `tcp_retransmit_skb` tracepoint |
| `flow.handshake_fail` | ebpf | A connection attempt never completed its handshake |
| `flow.first_failure_for_pair` ⭐ | ebpf | Two machines that had been connecting no longer can |
| `flow.rollup` | ebpf | A summary of ordinary connection activity over ten seconds |
| `dhcp.offer` | wire | A DHCP server offered a lease |
| `dhcp.ack` | wire | A lease was granted |
| `dhcp.nak` | wire | A lease request was refused |
| `dhcp.server_seen` ⭐ | wire | A second DHCP server is answering on this segment |
| `dhcp.lease_changed` | wire | A client's address, gateway or resolver changed |
| `dns.query_fail` | wire | A resolver did not answer, or answered with a failure |
| `dns.resolver_changed` ⭐ | wire | A client began using a different resolver |
| `dns.latency_spike` | wire | Resolution became slow enough to be felt |
| `policy.drop_burst` | nftables | *Reserved:* needs rule counters, not the ruleset |
| `policy.rule_changed` | nftables | The filtering rules in force changed |
| `metric.anomaly` | probe | A measured series left its baseline: loss or latency |
| `change.config_applied` | config | *Reserved:* needs config diffing, which is not wired up |
| `change.device_reboot` | snmp | *Reserved:* needs SNMP or a device saying so |
| `change.admin_action` | config | *Reserved:* needs device authentication logs |
| `system.gap` | internal | The recorder was not watching, and for exactly how long |
| `system.drop` | internal | Events were lost: a netlink overrun, a full ring buffer, or a full packet socket |
| `system.clock_step` | internal | The wall clock jumped relative to the monotonic clock |
| `system.start` | internal | The recorder started |
| `system.stop` | internal | The recorder stopped deliberately, which is how a later gap is explained |
| `system.collector_down` | internal | A source stopped feeding the record while the recorder kept running |
| `system.update_available` | internal | A newer release exists. Reported whether or not installing it is allowed |
| `system.updated` | internal | The recorder replaced its own binary and restarted. The gap either side of it has an explanation because of this row |
| `system.update_failed` | internal | An update was refused or did not install; the running version is unchanged |

## The incident

Produced by the correlation engine (M3), not by collectors.

```
incident_id, opened_at, closed_at, status
title, severity, confidence
root_cause  { kind, entity_ref, confidence }
chain[]     { seq, event_id, relation, why, clause, evidence_ref }
victims[]   host_id[]
rule_id
```

`relation` is one of `causes`, `correlates`, `precedes`, and it is what
separates this from a log aggregator: the engine states explicitly when it has
only observed co-occurrence rather than causation. Nothing is permitted to claim
causality it cannot show.

`clause` names the rule clause (its own `as`) that this link matched -
letting a client key a translation of `why` by `rule_id` + `clause` even
when `why` itself is the English `event.Describe` fallback rather than a
clause-specific sentence. Empty when the link did not come from a rule
clause at all. See [an Arabic translation](rules.md#an-arabic-translation-if-you-have-one).

A rule's first **required** clause is its anchor. Clauses after it are matched
forwards in time; clauses before it are matched **backwards**, nearest first.
That is what lets a rule answer the question the project exists for — the
symptom has arrived, so what changed just before it — rather than only being
able to describe consequences. See [writing a rule](rules.md).

## Six decisions settled up front

| Decision | Resolution |
|---|---|
| Time | ns · monotonic + wall · NTP required · every clock step recorded |
| Cardinality | Rare facts individually · repeats folded by `dedup_key` + `count` · flows rolled up every 10s · a per-second ceiling that drops **loudly** |
| Identity | Temporal identity table · stable `host_id` · resolved at ingest, not at query |
| Retention | Raw 7 days → folded 30 days → incidents and summaries 365 days |
| Privacy | No payloads ever · DNS names behind a switch · optional hostname hashing |
| Storage | One wide table, `attrs` as JSON, indexed on time, kind, family and subject. Numbers decode as `json.Number`, never `float64` - nanosecond timestamps and byte counters exceed what a float can hold exactly |

## Folding

An event with a `dedup_key` folds into an existing row that shares the key
inside `FoldWindow` (60s), incrementing `count` and advancing `ts_last`. The
original `ts_wall` is kept, because an incident timeline needs to know when
something *started*, not only that it is still going.

A flapping interface therefore produces one growing event, not ten thousand
rows - and the `count` is itself the evidence of flapping.
