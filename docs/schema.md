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

### `flow.*` — layer 4, from conntrack and eBPF

`open` · `close` · `reset` · `timeout_no_close` · `retransmit_spike` ·
**`handshake_fail`** ⭐ · `rollup`

> **Cardinality decision.** Individual flows are *not* recorded as events. Only
> anomalous flows are; everything else is aggregated into a `flow.rollup` every
> 10 seconds. Without this the store fills in a day.

### `dhcp.*` and `dns.*` — naming and addressing, metadata only

`dhcp.offer` · `ack` · `nak` · **`dhcp.server_seen`** ⭐ · `lease_changed`
`dns.query_fail` · **`dns.resolver_changed`** ⭐ · `dns.latency_spike`

Never payloads. Query names are recorded behind a switch that can be turned off
entirely, because there are networks where recording them is not permitted.

### `policy.*` — filtering decisions, from nftables

`drop_burst` · `rule_changed` · **`first_drop_for_pair`** ⭐⭐

`first_drop_for_pair` — the first rejection between two endpoints that were
previously talking successfully — is the strongest single signal that a
configuration change just broke something.

### `metric.*` — measured series

RTT p50/p95, loss, interface bps/errors/drops, conntrack table size. Stored as
series on the same axis, not as events; they generate `metric.anomaly` when they
depart from baseline.

### `change.*` — intended changes

`config_applied` · `device_reboot` · `admin_action`

Fed from NetIntent, or from diffing device configuration over SSH/SNMP. This is
the family that lets the timeline answer *what were we doing when it broke*.

### `system.*` — the recorder reporting on itself

`gap` · `drop` · `clock_step` · `start` · `stop`

Never remove these. `system.gap` records exactly how long the recorder was
blind; `system.drop` records how many events were lost to a full buffer. A
recorder that silently omits what it missed is not evidence of anything.

## The incident

Produced by the correlation engine (M3), not by collectors.

```
incident_id, opened_at, closed_at, status
title, severity, confidence
root_cause  { kind, entity_ref, confidence }
chain[]     { seq, event_id, relation, why, evidence_ref }
victims[]   host_id[]
rule_id
```

`relation` is one of `causes`, `correlates`, `precedes`, and it is what
separates this from a log aggregator: the engine states explicitly when it has
only observed co-occurrence rather than causation. Nothing is permitted to claim
causality it cannot show.

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
