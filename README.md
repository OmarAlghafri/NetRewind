# NetRewind — a black box recorder for the network

A Linux service that records what *changed* on a network — interfaces, ARP
bindings, routes, flows, filtering decisions — and reconstructs the causal chain
of an outage after it is over, when the evidence would normally be gone.

> This is a lab project, built and demonstrated on GNS3. Nothing here has been
> deployed to production hardware.

**Status: M0.** The envelope, the event store and the interface-state collector
work end to end; `netrewind events` will show you a link going down and coming
back. Layer 2 and layer 3 are next, eBPF after that, and the correlation engine
after that. See [the roadmap](#roadmap).

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
- Records each observation in a common envelope with two clocks, a stable
  identity, and the evidence that justifies it
- Folds repeats so a flapping interface is one growing event rather than ten
  thousand rows
- Records its own blind spots: `system.gap` states exactly how long the recorder
  was not watching, because a gap that is not recorded is indistinguishable from
  a quiet period
- Answers questions after the fact:
  `netrewind events --last 24h --host 192.168.20.10`

Planned, in order: ARP and route changes, then eBPF flow observation, then the
correlation engine that turns events into incidents with an explicit causal
chain, then a web timeline and an appliance image.

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
                     event store            internal/store
                          |
              Isnad correlation engine      internal/correlate   (M3)
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
./build/netrewind events --last 15m               # ask what happened
```

## Roadmap

| | | |
|---|---|---|
| **M0** | envelope, store, interface state, CLI | done |
| M1 | ARP, neighbours, routes; identity resolution; `what-happened` | |
| M2 | eBPF flows, nftables decisions, rollups, `system.drop` | |
| M3 | Isnad correlation engine, incidents, rule library | |
| M4 | fault-injection lab, documentation, public release | |
| M5 | web timeline, appliance image, Prometheus/OTel export | |

## Documentation

- [The event schema](docs/schema.md) — the contract everything else depends on
- [Development environment](docs/dev-environment.md) — VM, kernel requirements, GNS3 wiring

## Licence

AGPL-3.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
