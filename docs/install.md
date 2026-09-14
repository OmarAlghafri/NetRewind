# Installing NetRewind

NetRewind is two programs. The **recorder** (`netrewindd`) runs as a service
and keeps the record; the **desktop application** shows it. Install the
recorder on the machine whose network view you want kept, and the desktop
application wherever you read from — usually the same machine.

## Linux

### Recorder

Supported: x86_64 and arm64, a kernel with BTF (`/sys/kernel/btf/vmlinux`)
for the eBPF flow collector, systemd. Ubuntu 24.04 and AlmaLinux 9 are the
distributions the packages were verified on.

From the `.deb` or `.rpm` in a release:

```bash
sudo apt install ./netrewind_<version>_amd64.deb      # Debian, Ubuntu
sudo dnf install ./netrewind-<version>.x86_64.rpm     # RHEL, AlmaLinux, Fedora
sudo systemctl enable --now netrewindd
```

The package installs the binaries under `/usr/local/bin`, the configuration
and rules under `/etc/netrewind` (kept on upgrade; an edited file is never
overwritten), the unit `netrewindd.service`, and creates the `netrewind`
group. It does not start the service: enabling it is the operator's
decision. The record lives in `/var/lib/netrewind/events.db` and survives
removal and purge — delete it yourself if you want it gone.

`nftables` is recommended, not required: without `nft` the recorder runs,
records everything else, and reports the policy collector as down.

From a release tarball instead (no package manager):

```bash
tar -xzf netrewind-<version>-linux-amd64.tar.gz
cd netrewind-<version>-linux-amd64 && sudo ./install.sh
```

Check it:

```bash
sudo netrewind status                 # health and the capability report over the local API
sudo netrewind timeline --last 15m
```

### Desktop application

Install the `netrewind-desktop` `.deb`/`.rpm` from the release, or run the
AppImage. The application reads the recorder through
`/run/netrewind/api.sock`, which is group-readable by `netrewind`:

```bash
sudo usermod -aG netrewind "$USER"    # then log out and in again
```

### Upgrading and removing

Install the newer package over the older one; a running recorder is
restarted onto the new binary, a stopped one stays stopped. `apt remove` /
`dnf remove` stop and unregister the service and keep `/etc/netrewind` (deb)
or save your edited files as `.rpmsave` (rpm); `apt purge` removes the
configuration too. The record is never removed by a package operation.

## Windows

Supported: Windows 10 22H2 and Windows 11, x64.

### With the installer

Run `NetRewind_<version>_x64-setup.exe` as an administrator. It installs the
desktop application and the recorder under `C:\Program Files\NetRewind`,
registers the recorder as the `netrewindd` service (LocalSystem, automatic
start, restart on failure), writes `C:\ProgramData\NetRewind\netrewindd.yaml`
granting the installing user access to the recorder's pipe, and starts it.
The `.msi` installs the same files for deployment tooling; register the
service afterwards with the command below.

Uninstalling (Settings → Apps, or `uninstall.exe`) unregisters the service
and removes the program; the record and configuration under
`C:\ProgramData\NetRewind` stay.

### Recorder only, or by hand

```powershell
netrewindd.exe service install          # as administrator; --allow-user S-1-5-... grants another account
netrewindd.exe service status
netrewindd.exe service stop | start | uninstall
```

`service install` writes the configuration if none exists, with the rules
directory next to the executable. The service logs to
`C:\ProgramData\NetRewind\netrewindd.log` and to the Application event log.

To let another account read the recorder, add its SID to `api.allow_users`
in `netrewindd.yaml` and restart the service. A user's SID:
`whoami /user`.

### What the Windows recorder watches

Interfaces, addresses and routes through the IP Helper notifications, and
the neighbour (ARP/ND) table by polling every two seconds. Flows, filtering
policy, DHCP/DNS on the wire and active probes have no Windows source in this
release; the capability report (`netrewind status`, or the Health page)
lists them as unsupported rather than pretending.

## Configuration

Every setting is in `netrewindd.yaml` (`/etc/netrewind/netrewindd.yaml` on
Linux, `%ProgramData%\NetRewind\netrewindd.yaml` on Windows); the sample in
`deploy/netrewindd.yaml` documents each key. `netrewindd --check-config`
validates a file without starting. The most common keys:

| Key | Meaning |
|---|---|
| `db` | Path of the event store. |
| `rules` | Directory of correlation rules; empty disables correlation. |
| `retention` | How much history to keep (`168h`). Incidents are kept four times as long. |
| `api.enabled`, `api.path` | The local API and its socket/pipe. |
| `api.group` (Linux) / `api.allow_users` (Windows) | Who, besides the recorder's own user, may read it. |
| `update.check`, `update.apply` | Look for, and install, new releases. Installing is off by default. |
| `record_dns_names` | Record DNS query names. Off by default. |

## Verifying a download

Every release ships `SHA256SUMS` and, when the maintainer's key was used,
`SHA256SUMS.sig`. Check the file you downloaded against the sums, and see
[releasing.md](releasing.md) for how the signature is made and verified.
