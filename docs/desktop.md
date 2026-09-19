# The desktop application

NetRewind's desktop application is a viewer for the record: it shows what
changed on the network and what the correlation engine concluded, in Arabic or
English, from one of three sources. It never writes to the record.

| Source | What it is | Needs |
|---|---|---|
| **Demo recording** | A real recording from a run of the fault-injection lab, carried inside the application. | Nothing. |
| **Live recorder** | The `netrewindd` service on the same machine, read over its [local API](api.md). | The recorder installed and running; this user allowed to reach it. |
| **Evidence bundle** | A `.tar.gz` exported from any NetRewind recorder, opened after verification. | The file. |

The first run walks through six steps: language, what the recorder does and
does not do, the choice of source, a check of the live recorder (skipped when
there is none), what the record contains, and one real incident to read.
Everything in it can be revisited from Settings.

## Installing

**Windows** — run `NetRewind_<version>_x64-setup.exe` (or the `.msi`). The
installer needs administrator rights because it also installs the recorder as
a Windows service (see [install.md](install.md#windows)). Uninstalling
unregisters the service and leaves the record under `%ProgramData%\NetRewind`.

**Linux** — install the recorder package first (`netrewind` `.deb`/`.rpm`),
then the desktop package (`netrewind-desktop` `.deb`/`.rpm`) or run the
AppImage. Add your user to the `netrewind` group and log in again so the
viewer can reach the recorder's socket:

```bash
sudo usermod -aG netrewind "$USER"
```

## Pages

- **Health** — the recorder's version, uptime and store size, the gaps the
  record has in it, and the capability report: every collector this build
  knows about, whether it is watching, and — when it is not — why. A
  collector this platform cannot run shows as *not supported on this
  platform*; one that started and failed shows *down* with the recorder's
  own reason. In demo mode there is no recorder, so the page lists only the
  sources present in the recording, never an implied "up".
- **Incidents** — what the correlation engine concluded, newest first, each
  with its causal chain. `caused` is a claim about mechanism; `and at the
  same time` is co-occurrence; the two are drawn differently and never
  blurred.
- **Timeline** — every recorded event, in order.
- **Investigation** — the events touching one host, by address or name.
- **Rules** — with a live recorder, the full catalogue it loaded and how often
  each rule concluded an incident in the loaded window; otherwise, only the
  rules that fired in the recording, and the page says so. An optional
  from/to range narrows the fired-count to incidents opened in that window;
  left unset, every incident counts, as before.
- **Evidence bundles** — export from the live recorder, open a bundle, and
  see what an open bundle is: who produced it, its window, whether its
  checksums and signature verified.
- **Diagnostics** — the source in use, the endpoint, when it was last
  refreshed, event counts by family and severity; what to copy into a
  support request.
- **Settings** — language, the source and its endpoint, the refresh interval,
  and the public key that makes bundle signatures mandatory.

The banner above every page says which source is on screen and, for a live
recorder, when it was last refreshed. When the recorder cannot be reached the
banner carries the recorder's own message (for example *no recorder is
listening at \\.\pipe\netrewind-api*) with a retry and a way back to the demo.

## Evidence bundles

A bundle is a `.tar.gz` holding `manifest.json`, `events.json`,
`incidents.json` and `SHA256SUMS` — and, when the producer signed it,
`SHA256SUMS.sig`, an ed25519 signature over the checksum file. It is made by
`netrewind bundle export`, by the recorder's `/v1/bundle`, or by the
application's own Export button, which asks the live recorder for a window
(last hour, day or week) and saves the result where you choose. DNS names are
left out unless you tick *include DNS names*.

"Export this incident's evidence" on an incident (Incidents page) opens the
Evidence page scoped to that one incident instead of a fixed recent window —
its own start and end, with an adjustable padding (five minutes either side
by default) so the exported window has a little surrounding context. Meant
for sharing one incident with another team or vendor without handing over
the rest of the record.

Opening a bundle checks every member against the checksum file before
anything is parsed. If a public key is configured in Settings, a bundle
without a signature, or with a signature made by another key, is refused. A
bundle is shown, never merged: your local record is not touched.

To restore a bundle into a store you can query with the CLI, use
`netrewind bundle import <file> --into <new.db>`.

## Command-line options

The application accepts options that apply for one run, without changing
the saved settings:

```
netrewind-desktop --source live|demo|bundle
                  --endpoint <socket path or pipe name>
                  --bundle <file.tar.gz>        (implies --source bundle)
                  --page overview|incidents|timeline|host|rules|evidence|diagnostics|settings
                  --lang ar|en
                  --no-wizard
```

For example, a shortcut that opens straight onto the live incidents:
`netrewind-desktop --source live --page incidents --no-wizard`.

## Troubleshooting

| Symptom | Cause | What to do |
|---|---|---|
| *no recorder is listening at …* | The service is not running. | Windows: `netrewindd service status` / `service start` as administrator. Linux: `systemctl status netrewindd`. |
| *the recorder … refused this user* | This account is not allowed to open the endpoint. | Windows: add the account's SID to `api.allow_users` in `%ProgramData%\NetRewind\netrewindd.yaml` and restart the service (the installer adds the installing user). Linux: `usermod -aG netrewind $USER`, then log in again. |
| *This source needs the desktop application* | The UI is running in a browser (development server), where the Rust side does not exist. | Only demo mode works there; run the installed application. |
| *does not match SHA256SUMS* | The bundle file is corrupt or was altered. | Ask the sender for it again. |
| *unsigned bundle is refused* | A public key is configured and the bundle carries no signature. | Clear the key in Settings if unsigned bundles from this sender are acceptable. |
| The health page lists collectors as *down* | They started and failed; the reason is shown. | On Linux, `nft` missing means the policy collector cannot run: install `nftables`. |

## Building from source

```bash
make desktop            # stages the recorder, runs the UI tests, builds the installers
```

Needs Node 22+, Rust stable, and on Linux the WebKitGTK development packages
(`libwebkit2gtk-4.1-dev`, `libayatana-appindicator3-dev`, `librsvg2-dev`,
`patchelf`). Installers land in `desktop/src-tauri/target/release/bundle/`.
`cd desktop && npm run dev` serves the UI at `http://localhost:1420` in demo
mode for design work; `npm test` runs the UI's unit tests and, after
`make desktop-resources`, `cargo test --lib --manifest-path desktop/src-tauri/Cargo.toml`
the shell's.
