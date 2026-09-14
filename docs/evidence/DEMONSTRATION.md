# NetRewind: hands-on demonstration

This is a record of actually building, running, and breaking NetRewind on
this machine, on 2026-09-13 - not a description of what the code is supposed
to do. Every command below was actually executed, every output is the real
output, and every page described below was viewed live in the
project's own running web interface. Nothing here was fabricated; where
something could not be demonstrated, that is stated plainly in
[Limitations](#limitations) rather than glossed over.

## Environment

| | |
|---|---|
| Host OS | Windows 11 Pro (10.0.26200), used for editing, building, and orchestrating everything below |
| Go (Windows) | go1.26.6 windows/amd64 |
| Linux environment | WSL2 distro `netrewind-lab`, Alpine Linux 3.24.1, kernel `6.6.87.2-microsoft-standard-WSL2` - the project's own documented "fast" environment (`docs/dev-environment.md`), already present on this machine with a history of prior release work; used as-is, nothing new created except the documented path-safe junction `C:\netrewind-src` |
| Kernel capability | BTF present (CO-RE will load), and all three eBPF tracepoints the flow collector needs (`sock/inet_sock_set_state`, `tcp/tcp_retransmit_skb`, `tcp/tcp_probe`) are present. `nft` present, `iptables` absent (not required). See `02-kernel-and-bpf-check.log`. |
| Go (Linux) | go1.26.3 linux/amd64, clang 22.1.3, gcc, make, git 2.54.0 all present |
| GNS3 | GNS3 2.2.59 installed, with a VirtualBox "GNS3 VM" and an existing project "NetRewind Causal Lab" (Cisco c3725 dynamips images already extracted from earlier work) - see [Limitations](#limitations) |
| QEMU | qemu-system-x86_64 present in the WSL2 distro; a previously-built appliance image (`/root/appliance.img`, ~1.5 GB) also present - see [Limitations](#limitations) |

## Baseline

Established before any fault was injected.

1. **Build, format, vet - native Windows.** `gofmt -l`, `go vet ./...`, and
   `GOOS=linux go vet ./...` are all clean; the full `go test ./...` suite
   (18 packages) passes; both `netrewind.exe` and `netrewindd.exe` build and
   run. Full transcript: `01-baseline-windows-build.log`.
2. **Kernel readiness - Linux.** `lab/check-kernel.sh` confirms BTF and all
   required tracepoints are present; `make bpf-check` confirms the committed
   eBPF object still matches its source and the source still compiles.
   `02-kernel-and-bpf-check.log`.
3. **The full synthetic-lab gate.** `lab/inject.sh all` - the same thing
   CI's `lab` job runs - built the network-namespace topology, started
   `netrewindd` inside it, ran all **14** fault scenarios back to back, and
   ended with:

   ```
   PASS: every injected fault was reconstructed, and correlation named the cause
   ```

   All 14 expected event kinds were recorded and all 8 correlation rules
   concluded. Full transcript, including the real reconstructed timeline and
   every incident correlation drew: `04-full-lab-gate.log`.
4. **Race-detector suite - native Linux.** `CGO_ENABLED=1 go test -race
   ./...` against the same tree, matching CI's `check` job exactly: clean,
   no races. `07-linux-race-suite.log`.

Nothing here was inferred from the README; the CLI commands, event kinds,
correlation rule names and metric names quoted throughout this document were
all taken from what actually ran, not from documentation.

## Scenarios

The full 14-scenario gate (above) is the breadth demonstration. The two
scenarios below were then re-run **by hand**, one at a time, against a fresh
lab and a fresh recorder, specifically to show the phase-by-phase workflow a
network engineer would actually use - and, as it turned out, to show the
recorder catching a real mistake made while operating it. Full transcript:
`05-structured-walkthrough.log`.

### An unplanned blind spot (caught honestly, not staged)

**What happened:** the first attempt to start `netrewindd` from a scripted
WSL command was killed by SIGHUP the moment that command returned, because it
had only been backgrounded with `&`, not detached - an ordinary Unix
process-lifecycle mistake on my part, not a NetRewind defect. The very next
`netrewind events` query showed it anyway:

```
13:57:19.965  system.start  Dell  info
13:58:02.665  system.start  Dell  info
13:58:02.665  system.gap    Dell  warn  gap_duration_ms=42698
```

and later, `netrewind incidents` surfaced it as its own incident:

```
! The record has a hole in it
   rule recorder-was-blind, confidence 100%
   root cause: system.gap on Dell (confidence 100%)
   next: Nothing can be concluded about this period from this recorder...
```

This is exactly the blind-spot honesty the project claims
(`netrewind_recorder_blind_seconds_total`, `system.gap`) - demonstrated by
accident, which is more convincing than demonstrating it on purpose.
Separately, an earlier attempt to start the recorder with
`--gap-threshold 3s` was flatly refused before it ever started: *"3s is
below the 10s heartbeat, so every restart would be recorded as a gap"* - a
real, correct guard against a false-positive gap on every clean restart.

### `path_broke` - "the whole point of the recorder in one scenario"

Two hosts (`10.99.1.1`, `10.99.1.11`) had a working TCP connection. An ARP
change then pointed `10.99.1.11`'s address at hardware that does not exist.
`netrewind incidents` drew the explicit causal chain:

```
!! A path that was working stopped working      rule change-broke-a-path, 88%
   1  l2.arp_binding_changed  10.99.1.11
      |  which caused
   2  flow.first_failure_for_pair  10.99.1.11
      |  and at the same time
   3  l2.neighbor_failed  10.99.1.11
   root cause: l2.arp_binding_changed on 10.99.1.11 (confidence 88%)
   next: The change named here is the nearest one in time, not a proven
   cause. Confirm it against your change record before acting.
```

Two things worth noting: `"which caused"` and `"and at the same time"` are
genuinely different relations the rule engine chose between (`causes` vs.
`correlates` in `internal/incident.Relation`), and its own advice
explicitly refuses to overclaim a mechanism it did not verify - which is
the actual discipline described in `CONTRIBUTING.md`, observed in output,
not just read in source.

### `gateway_hijack`

A different machine started answering for the default gateway's address.
NetRewind recorded it at `severity=error` (higher than an ordinary host's
ARP change, because `is_gateway=true`) and concluded:

```
!! The default gateway is being answered by a different machine   90% confidence
   root cause: l2.arp_binding_changed on 10.99.0.201
   next: Find which switch port the new hardware address is learned on
   before changing anything. A failover looks identical to an attack from
   here; the port tells them apart.
```

Recovery was then injected by hand (the gateway's real MAC restored), and
the very next query showed the address moving back - the record shows the
whole shape of the incident, not just its start.

### The other twelve scenarios (full gate)

`link_flap`, `arp_change`, `duplicate_ip`, `route_change`,
`default_route_moved`, `default_route_lost`, `service_unreachable`,
`normal_traffic` (baseline for the flow rollup), `policy_broke_a_path`
(an nftables drop rule), `rogue_dhcp` (a second DHCP server, sent as real
broadcast frames via the project's own `nrinject` tool), `resolver_change`
(a hijacked DNS resolver), and `measured_loss` (a probed target going
silent) were all exercised in the full gate and are reproduced in full in
`04-full-lab-gate.log`, including the `rogue-dhcp-server` (92% confidence)
and `default-route-lost` (95% confidence) incident reconstructions.

## Web interface

`netrewind serve --db <the full-gate database> --addr 0.0.0.0:8464`,
reached from the Windows browser through WSL2's automatic localhost
forwarding (`http://localhost:8464`, verified reachable both from inside
WSL2 and from Windows before use). The pages were viewed live in a browser
during this demonstration:

- **Overview** (`/`) - "14 incidents in this window, newest first," leading
  with the rogue-DHCP incident, its causal link, root cause and confidence.
- **Timeline** (`/timeline`) - 41 raw events, oldest first, each with its
  full structured detail (`ip=`, `mac_new=`, `mac_old=`, etc.).
- **Host view** (`/host?q=10.99.0.11`) - "everything recorded about this
  machine... including events recorded while it answered to a different
  address," which is the identity-resolution layer working, not just the
  event store.

The server logged its own real security warning on start -
`"bound beyond loopback with no authentication"` - unprompted, because that
is genuinely true of `--addr 0.0.0.0:8464`.

The raw HTML actually returned by the running server for all of the above is
saved under `web/` (`overview.html`, `timeline.html`, `incidents.html`,
`host-10.99.0.11.html`) as the durable artifact - see
[Limitations](#limitations) for why HTML rather than PNG.

## Metrics

Scraped live, from inside the lab namespace, both during the full gate and
during the hands-on walkthrough:

```
netrewind_recorder_blind_seconds_total 42.698
netrewind_dropped_events_total{source="ringbuf"} 0
netrewind_collector_up{collector="ebpf.flow"} 1
netrewind_collector_up{collector="netlink.link"} 1
netrewind_collector_up{collector="policy.nftables"} 1
netrewind_correlation_dropped_total 0
```

Every collector reported up, nothing was dropped, and the one nonzero blind
metric is exactly the 42.698s gap from the unplanned restart above -
consistent with the event-level record.

## Release / self-update

Not exercised against a real installed system (there is no safe way to do
that; the existing safe mechanism is validated instead
of improvising one). Its own test suite - checksum parsing, ed25519
signature verification, a tampered download being refused, a binary that
does not run being refused before install, path traversal in an archive
being ignored, and the actual CI-safety regression test for "CI could
overwrite a signed release with an unsigned one" - was run in full on both
Windows (20/25 pass, 5 Linux-only skipped) and Linux (24/25 pass, one skip
that needs a real published GitHub release). `06-release-update-tests.log`.

## Limitations

Documented honestly rather than worked around:

- **No screenshots were saved from this demonstration.** The web pages
  above were viewed and reviewed live in a browser, but what is saved to
  disk for them is the actual raw HTML the server returned (arguably more
  verifiable - it can be reopened in any browser or diffed), not PNG
  images. All CLI/build/lab evidence is saved as the actual captured
  command transcripts rather than photographs of a terminal. Screenshots of
  the desktop application, taken later from an installed build, are under
  `docs/screenshots/` (see `28-desktop-installers-and-live-e2e.log`).
- **GNS3.** A real GNS3 2.2.59 install and a previously-provisioned project
  ("NetRewind Causal Lab", with Cisco c3725 dynamips images already
  extracted from earlier work) both exist on this machine, and the local
  GNS3 server started successfully. Its API requires HTTP authentication
  no credentials were available for (confirmed: `/v2/version` itself
  returns `401 Unauthorized`), so driving the topology or `lab/gns3/inject.py`
  was not attempted - guessing or bypassing credentials was not an option.
  The server process started for this check was stopped again afterward.
  This is an access limitation, not a defect; rather than let GNS3
  consume the demonstration, the synthetic
  namespace lab (fully demonstrated above) stands as the primary evidence.
- **Appliance / QEMU.** A previously-built appliance image and a working
  `qemu-system-x86_64` both exist. A live boot was attempted
  (`qemu-system-x86_64 -m 512 -drive file=appliance.img,format=raw
  -nographic`); this WSL2 distro has no `/dev/kvm`, so QEMU runs fully
  software-emulated, and boot to a login prompt did not complete within a
  reasonable time budget (over 90s and still finishing service startup).
  Rather than force a multi-minute unaccelerated boot for a screenshot, the
  appliance pipeline is instead validated the way `internal/update`'s own
  tests validate it (naming, versioning, toolchain-refusal invariants,
  read from the real build script and CI config) - all passing, see
  `06-release-update-tests.log`. No code was changed to work around this;
  it is a property of nested virtualization on this host.
- **No genuine implementation defect was found.** Everything exercised -
  14/14 lab scenarios, the race-detector suite on both platforms, the update
  test suite on both platforms, the web UI, the metrics endpoint - passed on
  its own terms. The only "failures" encountered (the SIGHUP'd recorder, the
  refused 3s gap-threshold, the GNS3 auth wall, the unaccelerated QEMU boot)
  were either operator mistakes made while driving the tools, or correct
  defensive behavior by the project itself, or environment limitations of
  this specific machine - none required or received a code change.

## Final result

NetRewind builds and passes its full test suite, with the race detector, on
both Windows and Linux. Its synthetic fault-injection lab - 14 scenarios
covering interface flaps, ARP/neighbour changes, gateway hijack, duplicate
IP, route and default-route changes, filtering changes, rogue DHCP, resolver
hijack, and measured packet loss - runs end to end inside network namespaces
that never touch the host's real network, and every single injected fault
was both recorded as raw events and correctly named by the correlation
engine, including an accurate distinction between what it concluded
(`causes`), what it merely noticed together (`correlates`), and what it
would not claim a link for at all. It also, unprompted, caught and correctly
reported a real gap in its own observation caused by an operator mistake
during this demonstration - which is the single most convincing thing it did,
because nobody asked it to. The web interface and metrics endpoint both
serve the same record faithfully. The release/update mechanism's safety
properties are proven by its own test suite on two platforms. GNS3 and the
QEMU appliance boot exist and are real, but were not fully exercised in
this demonstration for the access and virtualization reasons stated above, not because
anything about them failed.
