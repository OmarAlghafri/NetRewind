# Changelog

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
