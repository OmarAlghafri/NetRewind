# NetRewind — a black box recorder for the network

A Linux service that records what *changed* on a network — interfaces, ARP
bindings, routes, flows, filtering decisions — and reconstructs the causal chain
of an outage after it is over, when the evidence would normally be gone.

> This is a lab project, built and demonstrated on GNS3. Nothing here has been
> deployed to production hardware.

**Status: layers 1–4 recorded, correlated, and proven end to end.** A synthetic
lab injects eleven real faults — a flapping port, a hijacked gateway, a contested
address, a re-pointed route, a vanished default, a path broken under a working
connection — and checks both that every one is reconstructed from the record and
that correlation names the cause. See [the roadmap](#roadmap).

## The problem

When a network breaks, the honest description of the situation is usually:

> The connection dropped at 10:43 and we do not know why.

The evidence is scattered across switch logs, firewall logs, DHCP and DNS, each
with its own clock and its own format — and most of it has already rolled over
by the time anyone looks. So the engineer guesses, swaps a cable, restarts a
service, and the fault comes back in two days.

Worse is the intermittent fault: *it drops for ninety seconds every couple of
days, at no particular time*. There is no practical way to be watching when it
happens. Packet capture fills the disk in an hour. So it goes unsolved for
months.

## The idea

Record state changes, not packets.

Almost everything that breaks a local network is a state change: a carrier
drops, an ARP binding moves, a route disappears, a second DHCP server appears,
an ACL starts rejecting a pair of hosts that were talking a minute ago. Those
are cheap to observe and cheap to keep — orders of magnitude smaller than
capturing traffic — and they are *already the answer*, rather than raw material
that has to be mined for one.

Put them all on one timeline, at nanosecond resolution, and the story assembles
itself:

```
10:41:58  a second DHCP server appeared on 192.168.20.99
10:42:17    nine hosts took a different default gateway
10:42:19      34% packet loss outbound
10:42:23        41 TCP connections to Server X dropped
10:47:12  ended - port Gi0/14 was shut manually
```

## What it does

- Watches interface state through netlink, distinguishing an administratively
  shut port from a dropped carrier — different causes, different fixes, and
  every other tool reports them identically
- Watches the neighbour table, so an ARP binding moving under a running network
  is recorded — and flagged as an error rather than a warning when the address
  it moved under is the default gateway
- Watches routing, reporting a change only when the route that actually *wins*
  for a destination changes, so a standby path being installed is not mistaken
  for traffic moving
- Ties observations to machines through a temporal identity table, so asking
  about an address as it was last Tuesday returns the machine that held it
  *then*, not the one holding it now
- Folds repeats so a flapping interface is one growing event rather than ten
  thousand rows
- Records its own blind spots: `system.gap` states exactly how long the recorder
  was not watching, because a gap that is not recorded is indistinguishable from
  a quiet period
- Reads the record back as a narrative, not a table

```
$ netrewind what-happened --host 10.99.0.11 --at 15:00 --window 10m

  !  12:15:50.621             nrlab1 was shut down administratively
  -  12:15:52.628  +2.01s     nrlab1 came back after 2.006s
  !  12:15:53.639  +1.01s     10.99.0.11 moved from 02:00:00:00:00:11 to 02:00:00:00:00:aa
  !! 12:15:54.647  +970ms     10.99.0.201 moved from 02:00:00:00:00:01 to 02:00:00:00:00:ff - this is the default gateway
  !! 12:15:55.660  +1.01s     10.99.0.11 is being claimed by more than one machine  (x3)
  -  12:15:59.685  +4.02s     route to 10.200.0.0/24 now goes via 10.99.1.11 (was 10.99.0.11)
  !! 12:16:01.713  +2.02s     the default route was removed - nothing beyond the local segment is reachable
```

- Watches TCP connections from inside the kernel with eBPF, and reports when
  **two machines that were talking a moment ago can no longer connect** — the
  strongest single signal that something just changed. Ordinary activity is
  summarised every ten seconds rather than recorded per connection, and when the
  kernel has to drop something, it says so
- Watches the filtering rules in force, so a change to them lands on the same
  timeline as the connections it breaks — qualified by table and chain, because
  the same rule in a different chain is a different rule
- Correlates those events into incidents — and states, for every step, whether
  it *caused* the next one or merely happened alongside it

```
$ netrewind incidents --last 1h

!! A path that was working stopped working
   19:09:19 to 19:09:23  (4s)   rule change-broke-a-path, confidence 88%
   affected: 10.99.1.11

   1  19:09:19.078  l2.arp_binding_changed  10.99.1.11
      This is the last thing that changed on the path before it broke.
      |  which caused
   2  19:09:23.086  flow.first_failure_for_pair  10.99.1.11
      Two machines that had been connecting successfully can no longer
      complete a handshake. Whatever else is true, something between
      them changed.
      |  and at the same time
   3  19:09:23.090  l2.neighbor_failed  10.99.1.11
      Other connections started failing in the same window.

   root cause: l2.arp_binding_changed on 10.99.1.11 (confidence 88%)
   next:
      The change named here is the nearest one in time, not a proven
      cause. Confirm it against your change record before acting.
```

Note the middle column. `which caused` is a claim about mechanism; `and at the
same time` is only co-occurrence. The engine never blurs the two, because a tool
that does teaches its operator to distrust it — and an operator who distrusts
the timeline is back to guessing.

Planned next: conntrack for connection lifetimes and resets, then a web timeline
and an appliance image.

## How it is different

| Tool | What it gives | What it does not |
|---|---|---|
| NetFlow / ntopng | Traffic statistics | No causal context, no L2/L3 state |
| Zeek / Arkime | Rich logs from a SPAN port | Heavy storage; blind to state and config changes |
| Cilium / Pixie / Retina | Excellent eBPF observability | Built for containers and Kubernetes; know nothing about VLANs, ARP or switches |
| SIEM | Log aggregation | Large infrastructure, and the correlation is still your job |
| SNMP / Nagios | "The interface is down" | One instant, no history, no cause |

The gap NetRewind aims at: light, agentless, aware of the ordinary enterprise
LAN, and producing a causal narrative rather than a wall of alerts.

## Architecture

```
netlink · eBPF · conntrack · nftables · DHCP/DNS · LLDP · config
                          |
                    event collectors        internal/collect
                          |
                    common envelope         internal/event
                          |
              identity resolution           internal/identity
                          |
                     event store            internal/store
                          |
              Isnad correlation engine      internal/correlate
                          |
        incidents with an explicit causal chain
                          |
            CLI  ·  web timeline  ·  OpenTelemetry export
```

The correlation core is named **Isnad**, after the classical Arabic method of
establishing a chain of transmission in which every link is named and every link
is verifiable. That is the standard the engine is held to: it distinguishes
`causes` from `correlates` from `precedes`, and never claims a causal link it
cannot show the evidence for.

## Getting started

Building and testing work on any platform. Observing requires Linux — see
[docs/dev-environment.md](docs/dev-environment.md) for the VM and lab setup.

```bash
make all                                          # fmt, vet, test, build
sudo ./build/netrewindd --db ./var/events.db      # record
./build/netrewind timeline --last 15m             # read it back
```

To see the whole thing work without a network to break, run the synthetic lab.
It builds a topology in network namespaces, records it, injects seven faults and
checks that every one of them can be found in the record afterwards:

```bash
sudo make lab
```

## Roadmap

| | | |
|---|---|---|
| **M0** | envelope, store, interface state, CLI | done |
| **M1** | neighbours, routes, addresses; temporal identity; narrative queries; fault-injection lab | done |
| **M2** | eBPF connection observation, rollups, `system.drop` | done |
| **M2b** | nftables rule changes | done |
| M2c | conntrack: connection lifetimes, resets, `flow.timeout_no_close` | |
| **M3** | Isnad correlation engine with backward cause matching, twelve-rule library | done |
| M4 | GNS3 lab, documentation, public release | |
| M5 | web timeline, appliance image, Prometheus/OTel export | |

## Documentation

- [The event schema](docs/schema.md) — the contract everything else depends on
- [Writing a rule](docs/rules.md) — how correlation recognises a failure, and how to teach it a new one
- [Development environment](docs/dev-environment.md) — kernel requirements, the synthetic lab, GNS3 wiring

## Licence

AGPL-3.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
