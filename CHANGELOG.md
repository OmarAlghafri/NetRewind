# Changelog

## 0.8.0 — unreleased

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

### Identity

A temporal identity table, so asking about an address as it was last Tuesday
returns the machine that held it *then*, and does not drag in whichever machine
holds it today. An address turning up behind different hardware creates a
different host rather than joining the existing one — merging them is how an
identity table quietly conflates an attacker with its victim.

### Correlation — the Isnad engine

Twelve rules, as YAML files rather than code. Every link in a chain names the
event it rests on and states whether it *caused* the next one, merely
*correlated* with it, or only *preceded* it. Optional clauses placed before a
rule's first required clause are searched **backwards** in time, nearest first,
which is what lets a rule ask the question the project exists for: the symptom
has arrived, so what changed just before it?

An incident can never be more certain than the observations under it.

### Honesty

- `system.gap` records exactly how long the recorder was not watching.
- `system.drop` records how many events the kernel had to throw away.
- Both are exported as Prometheus counters, and both start at zero rather than
  appearing on first failure — a series that springs into existence when
  something breaks cannot be alerted on before it.
- A folded event contributes its occurrence count to metrics, not one, so they
  do not under-report exactly when things are worst.

### Reading it back

`events`, `timeline`, `what-happened`, `incidents`, `rules`, and `serve` — a
server-rendered web interface with no JavaScript, embedded in the same static
binary, read-only, with the blind-spot banner on every page.

### Delivery

Static binaries for `linux/amd64` and `linux/arm64` with no runtime
dependencies, an installer that refuses to overwrite locally edited rules and
never deletes the event store, a systemd unit granting only four capabilities,
and a container image.

### Proven by

A synthetic lab that builds a topology in network namespaces, injects eleven
faults — a flapping port, a hijacked gateway, a contested address, a re-pointed
route, a vanished default, a service that will not answer, a working path broken
by an ARP change and another broken by a filtering rule — and checks both that
every one can be found in the record afterwards and that correlation named the
cause. It runs in CI. 110 unit tests.

### Not done

- conntrack, for connection lifetimes, resets and `flow.timeout_no_close`
- DHCP and DNS metadata
- LLDP topology, and SNMP for switch state
- An OpenTelemetry exporter and a bootable appliance image
- Multiple recorders on one network
