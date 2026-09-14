# NetRewind 1.0.0 — live run on a real Windows machine

These were captured on a separate physical Windows 11 machine while the
**published 1.0.0 installer** (`NetRewind_1.0.0_x64-setup.exe`) was installed
and running as the `netrewindd` service. The desktop application is reading the
**live recorder** on that machine over the local named pipe
(`\\.\pipe\netrewind-api`) — not demo data. The recorder's observer id is set to
the neutral `workstation`; no account name, address or private data appears.

The images committed here are **cropped to the NetRewind window** (app only).
They were taken as full-screen captures — some with the app maximised, some
windowed with the Windows desktop showing behind it — and the full-desktop
originals are kept locally under `full-desktop/` (not committed), so the crop
can always be re-derived. The committed crops are what belongs in a repository:
the application, not the machine it happened to run on.

The run tells one story end to end: install → healthy → a fault is injected →
the recorder records it and the engine diagnoses it → the fault is fixed → the
machine is healthy again. The recorder, the service, the installer, the store
under `C:\ProgramData\NetRewind`, and the screenshot tooling were all removed
from the machine afterwards, and the network adapter was returned to its
original state.

## The fault

A safe, isolated adapter (a VirtualBox host-only NIC, `Ethernet 2`, carrying
no real traffic — the machine's connectivity is on Wi-Fi and was never
touched) was **flapped five times** with `Disable-NetAdapter` /
`Enable-NetAdapter`. That is what a failing transceiver, a marginal cable, or
a duplex mismatch looks like to the OS. The recorder recorded the repeated
`link.down` / `link.up`, and the correlation engine concluded the
**`port-flapping`** incident (88% confidence) on its own, named the root cause,
and printed the remediation advice — without anyone asking it a question. The
fix was simply to stop flapping and leave the adapter up; the address and
route it had been carrying returned on their own, and the recorder recorded
that too. The incident stays in the record afterwards, which is the point: the
evidence is not erased by the recovery.

## The images, in order

| # | File | Original capture | What it shows |
|---|------|------------------|---------------|
| 01 | `01-health-en.png` | full-screen | Health page, connected to the live recorder: version 1.0.0, observer `workstation`, uptime, store path, no observation gap, and the capability report (Windows collectors *watching*, Linux ones *unsupported* with the reason). |
| 02 | `02-timeline-en.png` | full-screen | Raw timeline — every recorded state change in order (here, a benign address add/remove used to seed the record). |
| 03 | `03-incidents-none-en.png` | full-screen | Incidents **before** the fault: "No incidents in this window — that does not necessarily mean nothing went wrong." |
| 04 | `04-rules-en.png` | windowed | The correlation-rule catalogue loaded in the recorder. |
| 05 | `05-evidence-en.png` | full-screen | Evidence-bundle page (export from the live recorder / open a bundle). |
| 06 | `06-diagnostics-en.png` | windowed | Diagnostics page. |
| 07 | `07-settings-en.png` | windowed | Settings: language, record source set to *Live recorder on this machine*, the pipe endpoint, refresh interval, bundle-signature key. |
| 08 | `08-fault-timeline-en.png` | full-screen | The **fault** in the raw record: `link.down` (warn) / `link.up`, and the address and route that left and returned with the adapter. |
| 09 | `09-fault-incident-en.png` | windowed | The **diagnosis**: "A port is flapping" (88%, `port-flapping`), the causal chain, the root cause, and the suggested next step. |
| 10 | `10-fault-incident-ar.png` | windowed | The same incident in **Arabic (RTL)** — the UI chrome is fully bilingual (تحذير / الثقة / السبب الجذري / الخطوة التالية المقترحة). |
| 11 | `11-recovered-health-en.png` | full-screen | Healthy again after the fix: recorder up, no observation gap. The flapping incident remains in the record as history. |

## Provenance

- Installer: `NetRewind_1.0.0_x64-setup.exe`, SHA-256
  `986635db9eacb18ad500a9d7de60d5e0c34db161bce3a5a93312c6aae75e9127` — the same
  bytes published on the v1.0.0 GitHub release.
- Captured 2026-09-14 on a physical Windows 11 Home machine (WSL2 present but
  unused here). Everything installed for the run was removed afterwards and the
  clean state was verified.
