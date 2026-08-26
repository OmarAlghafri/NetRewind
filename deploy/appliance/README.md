# The appliance image

A bootable disk image that comes up recording. Write it to a USB stick or a
small SSD, plug the machine into the segment you want watched, and there is
nothing to install and nothing to configure before there is a record.

That is the difference between this and the release tarball. The tarball
assumes you have a Linux host and want the recorder on it. The image assumes you
have a spare box — a mini PC, an old laptop, a thin client — and want it to
become a recorder.

## Building it

```bash
sudo deploy/appliance/build-image.sh
sudo deploy/appliance/build-image.sh --size 2048 --out /tmp/nr.img
```

Linux, root, and `sfdisk`, `mkfs.ext4`, `extlinux` and `wget`. On Windows, run
it inside WSL. Everything else it fetches.

| | |
|---|---|
| Raw image | 1.5 GB (mostly empty; it grows to fill its disk on first boot) |
| Compressed | **178 MB** |
| Actually used | 235 MB |

## Trying it before writing it to anything

```bash
qemu-system-x86_64 -m 512 -drive file=netrewind-appliance.img,format=raw -nographic
```

That is how the image is tested here, and what the boot looks like:

```
 * preparing the appliance ... [ ok ]
 * Starting networking ... udhcpc: lease of 10.0.2.15 obtained
 * Starting netrewindd ... [ ok ]

netrewind login: root
netrewind:~# netrewind events --last 10m
TIME          KIND          SUBJECT           SEV     DETAIL
08:06:31.103  system.start  netrewind-123456  info
08:06:49.048  system.stop   netrewind-123456  info
08:09:31.713  system.start  netrewind-123456  info
08:09:31.758  system.gap    netrewind-123456  warn    gap_duration_ms=170549
08:09:37.494  link.up       nrtest            notice  admin_up=true
08:09:40.703  link.down     nrtest            warn    cause=administrative
```

The third and fourth rows are worth a second look: the box was powered off for
two minutes and fifty seconds between two boots, and it says so. A recorder
whose record cannot show its own downtime is not evidence of anything.

## Writing it to a disk

```bash
gunzip -c netrewind-appliance.img.gz | sudo dd of=/dev/sdX bs=4M status=progress conv=fsync
```

Check `/dev/sdX` twice. `dd` will overwrite whatever you point it at.

## What it does on first boot

- **Grows the filesystem** to the disk it was written to. The image is built
  small so it copies quickly; without this an appliance on a 500 GB SSD would
  keep a week of history where it could keep years.
- **Takes an observer id** from the first hardware address it finds, so events
  say which recorder produced them and two appliances are distinguishable.
- **Brings every interface up on DHCP.** An appliance is plugged into somebody
  else's segment, and needing an address assigned before it can start is asking
  for work during the incident that caused it to be plugged in. It records
  regardless: the netlink and packet collectors need no address at all.

## Getting in

There is **no sshd**. The console — serial at 115200, or a screen — is the only
way in, and root has no password. That is deliberate for a box whose whole job
is to watch a network it has no reason to trust: it listens to nothing, so
there is nothing on it to attack from the segment.

To read the record from elsewhere, start the web interface deliberately:

```bash
netrewind serve --addr 0.0.0.0:8464
```

It will warn you that it is unauthenticated and reachable, because it is.

## Configuring it

`/etc/netrewind/netrewindd.yaml`, the same file as every other way of running
this. After editing:

```bash
netrewindd --check-config
rc-service netrewindd restart
```

The service runs that check before starting, so a bad edit stops the recorder
rather than starting one that records the wrong thing.

## What is not here

**arm64.** A Raspberry Pi needs a different bootloader and a vendor kernel, and
building that properly is its own piece of work. The release tarball installs on
Raspberry Pi OS in a minute and gives you the same recorder, so this is a
missing convenience rather than a missing capability.

**Any way in over the network.** See above; it is a choice, not an omission.

## Two traps, written down because they each cost an afternoon

**syslinux cannot read a modern ext4.** Its own filesystem driver does not
understand `64bit` or `metadata_csum`, which `mkfs.ext4` enables by default. The
disk then boots as far as `Failed to load ldlinux.c32` and stops — naming a file
that is plainly present, because `ldlinux.sys` was installed through the running
kernel and reads fine, while the module load goes through syslinux's own driver.
The build disables those features.

**`extlinux --install` does not install the C32 modules.** It writes
`ldlinux.sys` and nothing else, and the modules have to be copied in beside it
from `/usr/share/syslinux`. Same symptom, different cause.
