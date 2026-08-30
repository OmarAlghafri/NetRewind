#!/bin/sh
# Build a bootable NetRewind appliance image.
#
# The output is a raw disk image that boots straight into the recorder: write it
# to a USB stick or an SSD, plug the machine into the segment you want watched,
# and it starts recording. Nothing to install and nothing to configure to get a
# working record - which is the point of an appliance, as against a package.
#
#   sudo deploy/appliance/build-image.sh
#   sudo deploy/appliance/build-image.sh --size 2048 --out /tmp/nr.img
#
# What it produces
#   - one MBR disk, one ext4 partition, syslinux in the boot sector
#   - Alpine as the base, because the whole userland the recorder needs is
#     busybox, nftables and iproute2, and Alpine is that in about 8 MB
#   - the recorder, the CLI, the rule library and a configuration file
#   - OpenRC services that bring the network up on DHCP and start recording
#
# Requirements: Linux, root, and sfdisk, mkfs.ext4, syslinux/extlinux, and
# wget. Everything else is fetched.
#
# This runs on Linux. On Windows it is meant to be run inside WSL.

set -eu

SIZE_MB=1536
OUT=""
ALPINE_BRANCH="v3.22"
ALPINE_VERSION="3.22.2"
ARCH="x86_64"
BINARIES=""
KEEP_WORK=0

usage() {
    cat <<USAGE
usage: build-image.sh [options]

  --size MB     image size in megabytes (default $SIZE_MB)
  --out PATH    where to write the image (default ./dist/netrewind-appliance.img)
  --version VER what the recorder reports as its version. Defaults to what git
                describes, which is a bare commit hash on an untagged tree -
                and an appliance that cannot name its own version is one
                nobody can tell the state of. \`make image\` passes the same
                version the tarballs are built with.
  --arch ARCH   x86_64 (default) - aarch64 needs a different bootloader and is
                not built here; use the release tarball on a Raspberry Pi
  --binaries D  use netrewindd and netrewind from D instead of building them.
                Point it at an unpacked release tarball to write an image of
                exactly the binaries that were published, rather than of
                another build of the same source. It also means the machine
                that makes images needs no Go toolchain - which matters,
                because it needs loop devices instead, and the two are not
                always the same machine.
  --keep-work   leave the staging directory for inspection
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --size) SIZE_MB="$2"; shift 2 ;;
        --out) OUT="$2"; shift 2 ;;
        --version) VERSION="$2"; shift 2 ;;
        --arch) ARCH="$2"; shift 2 ;;
        --binaries) BINARIES="$2"; shift 2 ;;
        --keep-work) KEEP_WORK=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage; exit 2 ;;
    esac
done

HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
[ -n "$OUT" ] || OUT="$REPO/dist/netrewind-appliance.img"

die() { echo "build-image: $1" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run this as root: it needs loop devices and mount"
[ "$ARCH" = "x86_64" ] || die "only x86_64 is built here; see --help"

for tool in sfdisk mkfs.ext4 extlinux wget; do
    command -v "$tool" >/dev/null 2>&1 || die "missing $tool"
done

WORK=$(mktemp -d)
ROOT="$WORK/root"
LOOP=""

cleanup() {
    set +e
    if mountpoint -q "$ROOT" 2>/dev/null; then
        umount "$ROOT/proc" 2>/dev/null
        umount "$ROOT/sys" 2>/dev/null
        umount "$ROOT/dev" 2>/dev/null
        umount "$ROOT"
    fi
    [ -n "$LOOP" ] && losetup -d "$LOOP" 2>/dev/null
    [ "$KEEP_WORK" = 1 ] || rm -rf "$WORK"
    [ "$KEEP_WORK" = 1 ] && echo "staging left at $WORK"
}
trap cleanup EXIT INT TERM

mkdir -p "$WORK/bin"

if [ -n "$BINARIES" ]; then
    echo "==> taking the binaries from $BINARIES"
    for b in netrewindd netrewind; do
        [ -f "$BINARIES/$b" ] || die "no $b in $BINARIES"
        install -m 0755 "$BINARIES/$b" "$WORK/bin/$b"
    done
    # Run it rather than trust it. This is the same check the updater makes
    # before replacing anything, and it answers three questions at once: the
    # file executes on this machine, it is the architecture the image needs,
    # and it is the version the image is about to claim to be.
    REPORTED=$("$WORK/bin/netrewindd" --version 2>&1) ||
        die "$BINARIES/netrewindd does not run here: $REPORTED"
    echo "    $REPORTED"
    if [ -n "${VERSION:-}" ]; then
        case "$REPORTED" in
        *"${VERSION#v}"*) ;;
        *) die "the binaries report '$REPORTED' but --version says ${VERSION#v}.
  An image that names itself something the binaries inside it do not is an
  image nobody can tell the state of." ;;
        esac
    fi
    # Nothing is compiled here, so there is no toolchain to check: whatever
    # built these answered for that when it did.
else

echo "==> building the binaries"
command -v go >/dev/null 2>&1 || die "go is needed to build the recorder"

# The same guard `make release` has, repeated here because this script is
# documented as something to run directly and `make image` is not the only way
# in. go.mod names a toolchain rather than raising the go line so the tree keeps
# building on a distro that pins GOTOOLCHAIN=local with an older Go; on exactly
# those machines the directive has no effect and the build quietly links a
# standard library with known holes in it, one of them in html/template. An
# appliance is written to a disk and forgotten, which is the worst place for it.
WANT=$(sed -n 's/^toolchain //p' "$REPO/go.mod")
HAVE=$(go env GOVERSION)
if [ -n "$WANT" ]; then
    NEWEST=$(printf '%s\n%s\n' "${WANT#go}" "${HAVE#go}" | sort -V | tail -1)
    [ "go$NEWEST" = "$HAVE" ] || die "go.mod asks for $WANT; this is $HAVE.
  An image is written to a disk and left running for months. Building it with
  an older standard library is not something to do by accident.
  GOTOOLCHAIN is $(go env GOTOOLCHAIN); if it is local, install $WANT or build
  this somewhere the toolchain can be fetched."
fi
# The version is taken from the caller when there is one, and only guessed at
# otherwise. Guessing was how 0.8.0 shipped an image that called itself
# "0fb51d7": the tree it was built from had no tag yet, git describe fell back
# to the commit hash, and the image went out identifying itself by something no
# operator could match against a release. A bare hash is also not a version any
# updater can parse, so such an image would refuse every update it was offered
# for the rest of its life - on the one deployment nobody ever looks at.
VERSION="${VERSION:-$(cd "$REPO" && git describe --tags --always --dirty 2>/dev/null || echo dev)}"
# Tags carry a leading v; release artefacts do not. Same rule as the Makefile.
VERSION="${VERSION#v}"
( cd "$REPO" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-buildvcs=false \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$WORK/bin/netrewindd" ./cmd/netrewindd )
( cd "$REPO" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-buildvcs=false \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$WORK/bin/netrewind" ./cmd/netrewind )

fi

# Both paths need a version to stamp the image with, and --binaries leaves the
# default unresolved because it never reaches the block above.
VERSION="${VERSION:-$(cd "$REPO" && git describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"

echo "==> creating a ${SIZE_MB} MB disk"
mkdir -p "$(dirname "$OUT")"
rm -f "$OUT"
truncate -s "${SIZE_MB}M" "$OUT"

# One bootable partition. No EFI system partition: syslinux on the MBR boots on
# anything with a BIOS or a CSM, and an appliance that has to be told which
# firmware it is running on is not an appliance.
printf 'label: dos\n1 : start=2048, type=83, bootable\n' | sfdisk --quiet "$OUT"

# Two spellings, because busybox losetup does not understand util-linux's long
# options and Alpine ships busybox. Asking for the next free device and then
# attaching to it by name works with both.
LOOP=$(losetup -f)
[ -n "$LOOP" ] || die "no free loop device"
losetup -P "$LOOP" "$OUT" 2>/dev/null || losetup --partscan "$LOOP" "$OUT" ||
    die "could not attach $OUT to $LOOP"

PART="${LOOP}p1"
# Partition scanning is asynchronous; the node may not exist for a moment.
for _ in 1 2 3 4 5; do
    [ -b "$PART" ] && break
    sleep 1
done
[ -b "$PART" ] || die "the kernel did not create $PART; loop partition scanning may be unavailable"

# Those disabled features are not a preference.
#
# syslinux has its own ext driver, and it does not understand 64bit or
# metadata_csum, both of which modern mkfs.ext4 turns on by default. The result
# is a disk that gets as far as loading ldlinux.sys - which extlinux installs
# through the running kernel, so it works - and then fails on "Failed to load
# ldlinux.c32", because that read goes through syslinux's own driver. The error
# names a missing file that is plainly present, which is a bad afternoon.
mkfs.ext4 -q -L netrewind -O ^64bit,^metadata_csum,^metadata_csum_seed "$PART"
mkdir -p "$ROOT"
mount "$PART" "$ROOT"

echo "==> unpacking Alpine $ALPINE_VERSION"
MINIROOT="alpine-minirootfs-$ALPINE_VERSION-$ARCH.tar.gz"
MIRROR="https://dl-cdn.alpinelinux.org/alpine/$ALPINE_BRANCH/releases/$ARCH"
CACHE="${TMPDIR:-/tmp}/$MINIROOT"
[ -f "$CACHE" ] || wget -q -O "$CACHE" "$MIRROR/$MINIROOT" || die "could not download $MINIROOT"
tar -xzf "$CACHE" -C "$ROOT"

# The package install runs inside the image, so it needs the kernel's
# filesystems and a way out to the network.
cp /etc/resolv.conf "$ROOT/etc/resolv.conf"
mkdir -p "$ROOT/proc" "$ROOT/sys" "$ROOT/dev"
mount -t proc none "$ROOT/proc"
mount -t sysfs none "$ROOT/sys"
mount --bind /dev "$ROOT/dev"

cat > "$ROOT/etc/apk/repositories" <<REPOS
https://dl-cdn.alpinelinux.org/alpine/$ALPINE_BRANCH/main
https://dl-cdn.alpinelinux.org/alpine/$ALPINE_BRANCH/community
REPOS

echo "==> installing the base system"
# linux-lts rather than linux-virt: an appliance goes on real hardware and
# needs the drivers for whatever network card it finds.
chroot "$ROOT" /sbin/apk add --no-cache --quiet \
    alpine-base linux-lts linux-firmware-none \
    openrc busybox-openrc \
    e2fsprogs util-linux \
    iproute2 nftables \
    chrony \
    haveged \
    >/dev/null

echo "==> installing NetRewind"
install -m 0755 "$WORK/bin/netrewindd" "$ROOT/usr/local/bin/netrewindd"
install -m 0755 "$WORK/bin/netrewind" "$ROOT/usr/local/bin/netrewind"
install -d -m 0750 "$ROOT/etc/netrewind/rules"
install -m 0640 "$REPO"/rules/*.yaml "$ROOT/etc/netrewind/rules/"
install -m 0640 "$REPO/deploy/netrewindd.yaml" "$ROOT/etc/netrewind/netrewindd.yaml"
install -d -m 0750 "$ROOT/var/lib/netrewind"

# The appliance keeps its record on its own disk.
sed -i 's|^db: .*|db: /var/lib/netrewind/events.db|; s|^rules: .*|rules: /etc/netrewind/rules|' \
    "$ROOT/etc/netrewind/netrewindd.yaml"

echo "==> installing services"
install -m 0755 "$HERE/rootfs/etc/init.d/netrewindd" "$ROOT/etc/init.d/netrewindd"
install -m 0755 "$HERE/rootfs/etc/init.d/netrewind-firstboot" "$ROOT/etc/init.d/netrewind-firstboot"
install -m 0644 "$HERE/rootfs/etc/motd" "$ROOT/etc/motd"
install -m 0644 "$HERE/rootfs/etc/network/interfaces" "$ROOT/etc/network/interfaces"

chroot "$ROOT" /sbin/rc-update add devfs sysinit >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add procfs sysinit >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add sysfs sysinit >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add hwdrivers sysinit >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add modules boot >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add hostname boot >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add bootmisc boot >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add syslog boot >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add networking boot >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add haveged boot >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add netrewind-firstboot boot >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add chronyd default >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add netrewindd default >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add killprocs shutdown >/dev/null 2>&1
chroot "$ROOT" /sbin/rc-update add mount-ro shutdown >/dev/null 2>&1

# A login on the serial port.
#
# The stock inittab gives you tty1, which is a screen and a keyboard. An
# appliance is a box in a rack or on a shelf with a serial cable, and one
# without a serial getty boots perfectly and cannot be logged into at all -
# which is indistinguishable, from the outside, from one that did not boot.
if ! grep -q '^ttyS0::' "$ROOT/etc/inittab"; then
    printf '\n# Serial console, which is how a headless appliance is reached.\nttyS0::respawn:/sbin/getty -L 115200 ttyS0 vt100\n' \
        >> "$ROOT/etc/inittab"
fi
# And let root log in there, which securetty otherwise refuses.
grep -q '^ttyS0$' "$ROOT/etc/securetty" 2>/dev/null || echo ttyS0 >> "$ROOT/etc/securetty"

echo netrewind > "$ROOT/etc/hostname"
cat > "$ROOT/etc/fstab" <<FSTAB
LABEL=netrewind  /  ext4  rw,relatime  0 1
FSTAB

# Root has no password and no way in over the network: there is no sshd here.
# The console is the only way to reach it, which suits a box whose whole job is
# to watch and answer questions.
chroot "$ROOT" /usr/bin/passwd -d root >/dev/null 2>&1 || true

echo "==> installing the bootloader"
mkdir -p "$ROOT/boot/extlinux"
KERNEL=$(cd "$ROOT/boot" && ls vmlinuz-* 2>/dev/null | head -1)
INITRD=$(cd "$ROOT/boot" && ls initramfs-* 2>/dev/null | head -1)
[ -n "$KERNEL" ] || die "no kernel was installed"
[ -n "$INITRD" ] || die "no initramfs was installed"

cat > "$ROOT/boot/extlinux/extlinux.conf" <<BOOTCFG
DEFAULT netrewind
PROMPT 0
TIMEOUT 10

LABEL netrewind
    LINUX /boot/$KERNEL
    INITRD /boot/$INITRD
    # Both consoles: a serial console is what a headless appliance is reached
    # on, and a screen is what somebody plugs in when the serial cable is not
    # to hand.
    APPEND root=LABEL=netrewind rootfstype=ext4 rw quiet console=tty0 console=ttyS0,115200
BOOTCFG

# extlinux --install writes ldlinux.sys but not the C32 modules it then loads,
# so a disk built without this step gets as far as "Failed to load ldlinux.c32"
# and stops. They have to be copied in beside it.
SYSLINUX_LIB=""
for d in /usr/share/syslinux /usr/lib/syslinux/modules/bios /usr/lib/syslinux; do
    if [ -f "$d/ldlinux.c32" ]; then SYSLINUX_LIB="$d"; break; fi
done
[ -n "$SYSLINUX_LIB" ] || die "cannot find ldlinux.c32; is syslinux installed?"
for mod in ldlinux.c32 libcom32.c32 libutil.c32 mboot.c32 menu.c32; do
    [ -f "$SYSLINUX_LIB/$mod" ] && cp "$SYSLINUX_LIB/$mod" "$ROOT/boot/extlinux/"
done

extlinux --install "$ROOT/boot/extlinux" >/dev/null
# The MBR boot code lives outside the filesystem, so it goes on the disk rather
# than the partition.
for mbr in /usr/share/syslinux/mbr.bin /usr/lib/syslinux/mbr/mbr.bin /usr/lib/syslinux/mbr.bin; do
    if [ -f "$mbr" ]; then dd if="$mbr" of="$LOOP" bs=440 count=1 conv=notrunc status=none; break; fi
done

sync
echo "==> done"
cleanup
trap - EXIT INT TERM

ls -lh "$OUT" | awk '{print "    " $9 "  " $5}'
cat <<DONE

Write it to a disk and boot it:

    dd if=$OUT of=/dev/sdX bs=4M status=progress conv=fsync

Or try it first:

    qemu-system-x86_64 -m 512 -drive file=$OUT,format=raw -nographic

It comes up on DHCP and starts recording. Log in on the console as root -
there is no password and no sshd, because the console is the only way in.
DONE
