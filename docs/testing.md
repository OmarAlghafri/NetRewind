# Testing

Every claim in the README is backed either by a test in the tree or by a
recorded run under [docs/evidence](evidence/). This page says how to run
each suite and what each recorded run covers.

## Suites

| Suite | Command | Where it runs | What it covers |
|---|---|---|---|
| Go unit and integration tests | `go test ./...` (`CGO_ENABLED=0` on Windows) | Windows, Linux | event schema, store and migrations, identity, correlation engine and every rule, the analyzers behind each collector, the local API (including a real socket/pipe round trip), evidence bundles, IPC access control, the daemon's config, registry contract and write pipeline, the CLI |
| Go race suite | `CGO_ENABLED=1 go test -race ./...` | Linux | the same, under the race detector |
| Cross-platform vet | `make vet` | any | `go vet` for this platform and for Linux |
| Load tests | `make load` | any | sustained write throughput of the store and of the full write path |
| Fault-injection lab | `sudo make lab` | Linux with netlink, eBPF (BTF) and nftables | fourteen real faults in network namespaces — flapping port, gateway hijack, contested address, rogue DHCP, lost default route, path broken under a working connection, … — each checked against the record and against correlation's conclusion. This is the recorder's regression gate |
| Desktop UI | `cd desktop && npm test` | any (jsdom) | the data-source hook against a fake recorder and against failures, settings persistence, the capability table, the source banner, settings |
| Desktop shell | `cargo test --lib --manifest-path desktop/src-tauri/Cargo.toml` | any | evidence-bundle verification (checksums, tampering, missing members, newer formats, signature required/valid/wrong key), launch-option parsing |
| Desktop typecheck and build | `cd desktop && npm run build` | any | TypeScript, the production bundle |
| Windows collectors | `go test ./internal/collect/iphelper/` | any (Windows for the collectors themselves) | the interface-row filter; the collectors' real behaviour is covered by the recorded runs below |
| Accessibility | axe-core over the running UI | any browser | see evidence 21 |

CI (`.github/workflows/ci.yml`) runs the Go suite, `govulncheck`, the desktop
typecheck/build, the UI and shell unit tests, `npm audit` and `cargo audit` on
every push, the lab on Linux runners, and builds release artefacts on a tag.

## Recorded runs

Each file under `docs/evidence/` is the transcript of a real run — the
commands, the output, and what it does and does not prove.

| Evidence | Covers |
|---|---|
| 01 | Building, vetting and testing on Windows |
| 02 | Kernel and BPF prerequisites in the Linux lab environment |
| 04 | The full fourteen-fault lab gate |
| 05 | A phase-by-phase walkthrough of the lab, driven by hand |
| 06 | Release and self-update verification |
| 07 | The Go suite under the race detector on Linux |
| 08 | A carrier-loss defect found by the analyzer tests and proven fixed against the lab |
| 09 | The store migration runner: backup, single-transaction migration, restore |
| 10 | Evidence bundles: export, redaction, checksums, zip-slip protection, import |
| 11 | The local API over a real Unix socket and a real named pipe |
| 12 | The desktop application in demo mode, both languages |
| 13 | A disk-space constraint on the build machine at the time, and how it was handled |
| 14 | Neighbour, route and address analyzers extracted behind the ports layer |
| 15 | Building the `.deb` and `.rpm` and inspecting their contents |
| 16, 17 | The AI evaluation corpus and grading harness |
| 18, 19 | Windows IP Helper notification semantics; the interface-row filter |
| 20, 23, 24, 27 | AI candidate benchmarks: two models, two languages; the corrected sweep that decided no model ships |
| 21 | Accessibility audit of the desktop application (axe-core, contrast, keyboard, colour-blindness) |
| 22 | SBOMs and vulnerability scans across Go, npm and Cargo |
| 25 | `.deb` and `.rpm` lifecycle on real systemd hosts: install, enable, record, upgrade, remove, purge, restart |
| 26 | Real interface, address and route changes delivered through the Windows IP Helper API, under both registration modes |
| 28 | The Windows service and installer lifecycle, the installed product recording real adapter changes and exporting a bundle, a missed link event found and fixed, the Linux desktop bundles, and the screenshots |

`docs/evidence/DEMONSTRATION.md` is a longer narrative of building and
breaking the recorder by hand; `docs/evidence/baselines/` holds the measured
throughput, memory and store-size baselines; `docs/evidence/web/` holds
rendered pages from `netrewind serve`.

## Things a test cannot cover, and what covers them

- Whether an installed Windows service records a real adapter change: run
  `netrewindd service install`, disable and re-enable an adapter, and read
  `netrewind events --last 5m`. Expected: `link.down` (cause administrative)
  and `link.up`, plus the addresses and routes that left and returned.
- Whether the desktop application reaches the recorder as an ordinary user:
  `netrewind status` from that user's shell answers the same question over
  the same channel.
- A true kernel reboot on Linux (the packaged unit's ordering against
  `network-pre.target`): `systemctl is-active netrewindd` after boot, and a
  `system.start` event at the top of `netrewind timeline`.
