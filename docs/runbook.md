| `netrewind_store_writable` | it is 0 | The store is refusing writes and events are being lost right now. This is the one signal that still works when the store itself is what broke |
| `netrewind_clock_steps_total` | it increases | The wall clock jumped. Timestamps either side of it are not comparable |# Running the recorder

How to deploy NetRewind, where to put it, what to watch, and what to do with it
when something breaks.

> This is a lab project. It has been proven against network namespaces and is
> intended for a GNS3 lab. Nothing here has been deployed to production
> hardware, and this document describes how it is meant to be operated, not a
> record of it having been.

## What it needs

| | |
|---|---|
| Kernel | 5.15+ for the netlink collectors. eBPF flow observation additionally needs BTF (`/sys/kernel/btf/vmlinux`) and the `sock/inet_sock_set_state` tracepoint |
| Capabilities | `CAP_NET_ADMIN` to read netlink; `CAP_BPF` and `CAP_PERFMON` to load the eBPF program. Nothing else — it never writes to the network |
| CPU / RAM | 2 cores, 512 MB is comfortable for a single segment |
| Disk | An event store grows with *change*, not traffic. A quiet segment is tens of megabytes a week; a flapping one, a few hundred. Size for the retention you want and check `netrewind_stored_events` |
| Userspace | `nft` if you want the filtering collector. Everything else is in the binary |

Check the kernel before installing:

```bash
sudo lab/check-kernel.sh
```

It reports BTF, clang, each tracepoint, and whether nftables and conntrack are
usable. A missing capability found now costs minutes; found after a collector is
written against it, weeks.

## Installing

From a release tarball — `linux/amd64` and `linux/arm64` are both built, and the
same eBPF object serves both because the program is architecture-neutral
bytecode:

```bash
tar -xzf netrewind-0.8.0-linux-arm64.tar.gz
cd netrewind-0.8.0-linux-arm64
sudo ./install.sh
```

The installer will not overwrite a rule you have edited, or a configuration you
have tuned: the rule library is meant to be added to, and an upgrade that
silently reverted someone's work would teach them not to do any. A newer sample
configuration is left beside yours as `netrewindd.yaml.sample`. `--uninstall`
removes the binaries, the unit and the stock rules, and deliberately leaves both
the configuration and the event store alone — the store is the record, and a
script that deleted evidence as a side effect of removing a program would be
indefensible.

From source:

```bash
make linux
sudo install -m 0755 build/netrewindd-linux-amd64 /usr/local/bin/netrewindd
sudo install -m 0755 build/netrewind-linux-amd64  /usr/local/bin/netrewind
sudo install -d -m 0750 /etc/netrewind/rules
sudo cp rules/*.yaml /etc/netrewind/rules/
sudo cp deploy/netrewindd.yaml /etc/netrewind/
sudo cp deploy/systemd/netrewindd.service /etc/systemd/system/
sudo systemctl enable --now netrewindd
```

Or as a container, for watching a segment for an afternoon without installing
anything — see [deploy/Dockerfile](../deploy/Dockerfile). `--network host` is
not optional there: without it the recorder watches the container's own
namespace, which is a network of one interface that nothing interesting ever
happens on. It would run, report nothing, and look like a quiet network.

Connection observation in a container needs the host to have tracefs mounted at
`/sys/kernel/tracing`, bind-mounted in. Check with `mount | grep tracefs`
first: bind-mounting a path the host does not have gives the container an empty
directory rather than an error. Docker Desktop on macOS and Windows runs
containers in a VM with no tracefs at all, so `flow.*` is unavailable there
whatever you mount — the recorder records that fact rather than looking
healthy.

The unit grants only the four capabilities above, runs with `ProtectSystem=strict`
and a private `/tmp`, and is exempted from the OOM killer's usual attention —
the recorder has to survive the conditions it exists to record.

## Configuring it

Everything lives in `/etc/netrewind/netrewindd.yaml`, which the recorder reads
on its own. Every key is optional and the defaults are a working recorder; the
[sample](../deploy/netrewindd.yaml) documents each one. Command-line flags
override the file, which is how to try a setting once without editing anything.

Two behaviours are deliberate and worth knowing:

**An unknown key is an error, not a warning.** A misspelled setting that
silently does nothing is the quietest failure a configuration file has — you
would believe a thing was turned on for as long as it took to need it.

**A configuration that would leave the recorder useless is refused.** Setting
`retention: 0s` would have the hourly prune delete the entire record, including
the incident being investigated. Every problem is reported at once, so fixing a
file takes one pass rather than one restart per mistake:

```bash
netrewindd --check-config
```

That is what the unit runs before starting, so a bad edit stops the service
rather than starting a recorder that is not recording what you asked for.

## Where to put it

This is the decision that determines what the record is worth, and it deserves
more thought than the install.

The recorder sees what reaches it. That is the whole limitation, and pretending
otherwise is how observability tools end up trusted for answers they cannot
give.

**At the gateway, inline.** The best single position. Every conversation between
the segment and the outside world passes through it, and it sees ARP for the
whole broadcast domain, its own routing table, and every connection crossing the
boundary. This is where an appliance belongs.

**On a mirrored port.** Sees the traffic but not the gateway's own routing table
or filtering decisions. Layer 2 and connection observation still work; `l3.*`
and `policy.*` describe the recorder's own stack rather than the network's.

**As a host on the segment.** The cheapest to try, and enough to catch ARP
changes, a rogue DHCP server and a contested address — the faults that hit a
whole broadcast domain. It will not see conversations that do not involve it.

Whichever you pick, write it down beside the store. Six months later, "why is
there nothing about the server VLAN in here" has a boring answer, and it will
not be remembered.

## Watching the recorder

Turn the endpoint on:

```bash
netrewindd --metrics-addr 127.0.0.1:9464
```

Bind it to localhost or a management address. It has no authentication, and the
recorder should not be reachable from the network it is watching.

The metrics worth alerting on are not the ones counting what was seen:

| Metric | Alert when | Because |
|---|---|---|
| `netrewind_recorder_blind_seconds_total` | it increases at all | The record has a hole in it. Anything concluded about that window is worthless |
| `netrewind_dropped_events_total` | it increases | Events arrived faster than they could be read. The record is incomplete and does not say where |
| `netrewind_collector_up` | any drops to 0 | A whole source is missing. The timeline will look calm because it has gone deaf |
| `netrewind_clock_steps_total` | it increases | The wall clock jumped. Timestamps either side of it are not comparable |
| `netrewind_stored_events` | growth changes shape | Either the network became unstable or retention needs revisiting |

Everything else — `netrewind_events_total`, `netrewind_incidents_total` — is for
dashboards and capacity, not for paging anyone.

## Using it during an incident

The workflow is three commands, in this order.

**1. Was the recorder even watching?**

```bash
netrewind events --last 24h --family system
```

If there is a `system.gap` covering the outage, stop. Nothing else the record
says about that window can be relied on, and it is better to know that in the
first minute than the twentieth.

**2. What did it conclude?**

```bash
netrewind incidents --last 24h --min-severity warn
```

Read the relation between the links, not just the links. `which caused` is a
claim about mechanism. `and at the same time` is co-occurrence and nothing more —
the engine is telling you it does not know, and treating that as a cause is how
the wrong cable gets replaced.

**3. What happened to the thing that broke?**

```bash
netrewind what-happened --host 192.168.20.10 --at 15:00 --window 10m
```

All three are also pages, if a browser suits the moment better:

```bash
netrewind serve      # http://127.0.0.1:8464
```

It shows the same record, narrated by the same code, so a screenshot of the
terminal and a screenshot of the browser cannot disagree about what an event
meant. It is read-only, has no authentication, and binds to loopback — bind it
wider only behind something that does authenticate.

The identity table means this follows a machine across an address change, and
does *not* drag in whatever other machine holds that address today.

If the answer is "nothing was recorded", that is an answer — but check step 1
again before believing it.

## Retention and pruning

`retention` (default 7 days, or `--retention`) prunes events hourly. Incidents are kept far
longer: they are the conclusion, they are small, and they are what someone comes
back to months later. Their links will eventually point at events that have been
pruned, which is why every link carries its own description — the account
survives its evidence.

To keep raw events longer, raise `retention` and watch
`netrewind_stored_events`. The store is one SQLite file; back it up by copying it
while the recorder is stopped, or use `sqlite3 .backup` while it runs.

## Privacy

The recorder stores **no packet payloads**, ever. It records that a connection
was attempted, not what was said over it.

DNS query names are the one piece of content-adjacent data it can hold, and
there are networks where recording them is not permitted. That collector is not
yet implemented; when it is, it will be behind a switch that can be turned off
entirely rather than a setting that has to be remembered.

If you need to demonstrate what is in the store to someone who will ask:

```bash
netrewind events --last 7d -o json | head -50
```

Every field is a state change, an address, a port, a timestamp, or a rule the
recorder itself applied.

## Troubleshooting

**`flow: attach tracepoint (is tracefs mounted in this mount namespace?)`**
Entering a network namespace gets a fresh mount namespace with `/sys` remounted,
which hides tracefs. Mount it inside the same namespace that runs the recorder:

```bash
ip netns exec nrlab sh -c 'mount -t tracefs tracefs /sys/kernel/tracing; exec netrewindd ...'
```

**`flow: verifier rejected the program`**
The kernel refused the eBPF program. The verifier's own message is passed
through unchanged — read it rather than the wrapper. Usually a missing BTF or a
kernel older than the tracepoint.

**`policy: cannot read the nftables ruleset`**
`nft` is missing, or the process lacks the capability. Not a failure of the
recorder: the other collectors carry on, and the log says so once rather than
every interval.

**A collector failed but the daemon kept running.** Deliberate. One source
failing is a smaller loss than all of them, and `netrewind_collector_up` reports
which one went.

**The timeline looks suspiciously quiet.** Check `--family system` first, then
`netrewind_collector_up`. A recorder that has gone deaf and a network that has
gone quiet look identical from the outside, which is the entire reason the
`system.*` family exists.

The three things to look for there are `system.gap` (the recorder was not
running), `system.drop` (it was running and could not keep up), and
`system.collector_down` (it was running, the timeline is unbroken, and one
source stopped feeding it). The last is the one that reads as a healthy record:
the usual cause is a kernel without BTF, which stops the connection collector
loading, so the record shows no connection failures on a host where connections
were never being watched. The event carries the reason.
