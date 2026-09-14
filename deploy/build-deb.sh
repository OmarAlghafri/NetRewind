#!/bin/sh
# Build a .deb package from release binaries.
#
#   VERSION=0.9.2 ARCH=amd64 sudo deploy/build-deb.sh
#
# This is Linux, and dpkg-deb is what does the packing. Everything it needs
# beyond that (the two static binaries, rules/, the systemd unit, the sample
# config) already exists in this tree - this script only arranges them the
# way dpkg expects and lets dpkg's own conffile mechanism do what
# deploy/install.sh's tarball path has to do by hand: an upgrade must never
# silently overwrite a configuration or a rule file somebody edited. Listing
# them as conffiles is the native way to get that guarantee from dpkg itself,
# rather than reimplementing dpkg's own upgrade logic inside a postinst
# script.
set -eu

VERSION="${VERSION:-dev}"
VERSION="${VERSION#v}"
ARCH="${ARCH:-amd64}"
BUILD="${BUILD:-./build}"
OUT="${OUT:-./dist}"
PKGROOT="$(mktemp -d)"
trap 'rm -rf "$PKGROOT"' EXIT
# mktemp -d creates its directory at mode 0700 (that is the point of mktemp).
# dpkg-deb records whatever mode the package root directory has as the
# archive's own "./" entry, and installing a package whose "/" entry is
# 0700 is not a risk worth leaving to whether a given dpkg version happens
# to skip re-permissioning it - fixed explicitly instead of assumed safe.
chmod 0755 "$PKGROOT"

DAEMON="$BUILD/netrewindd-linux-$ARCH"
CLI="$BUILD/netrewind-linux-$ARCH"
[ -x "$DAEMON" ] || DAEMON="$BUILD/netrewindd"
[ -x "$CLI" ] || CLI="$BUILD/netrewind"
[ -x "$DAEMON" ] || { echo "build-deb: not found or not executable: $DAEMON (build it first)" >&2; exit 1; }
[ -x "$CLI" ] || { echo "build-deb: not found or not executable: $CLI" >&2; exit 1; }

echo "==> staging package tree"
install -d -m 0755 "$PKGROOT/DEBIAN"
install -d -m 0755 "$PKGROOT/usr/local/bin"
install -d -m 0750 "$PKGROOT/etc/netrewind/rules"
install -d -m 0750 "$PKGROOT/var/lib/netrewind"
install -d -m 0755 "$PKGROOT/etc/systemd/system"

install -m 0755 "$DAEMON" "$PKGROOT/usr/local/bin/netrewindd"
install -m 0755 "$CLI" "$PKGROOT/usr/local/bin/netrewind"

install -m 0640 deploy/netrewindd.yaml "$PKGROOT/etc/netrewind/netrewindd.yaml"
# Point the shipped default at the paths this package actually uses - the
# same rewrite deploy/install.sh does for a fresh (non-upgrade) install.
sed -i "s|^db: .*|db: /var/lib/netrewind/events.db|; s|^rules: .*|rules: /etc/netrewind/rules|" \
    "$PKGROOT/etc/netrewind/netrewindd.yaml"

for f in rules/*.yaml; do
    install -m 0640 "$f" "$PKGROOT/etc/netrewind/rules/$(basename "$f")"
done

install -m 0644 deploy/systemd/netrewindd.service "$PKGROOT/etc/systemd/system/netrewindd.service"

# Every rule file and the main config are conffiles: dpkg then refuses to
# silently clobber a locally-edited one on upgrade (it offers to keep, take
# the new one, or show a diff), which is exactly deploy/install.sh's own
# rule ("An upgrade must never overwrite a configuration somebody tuned")
# enforced by dpkg itself instead of reimplemented here.
{
    echo "/etc/netrewind/netrewindd.yaml"
    for f in rules/*.yaml; do
        echo "/etc/netrewind/rules/$(basename "$f")"
    done
} > "$PKGROOT/DEBIAN/conffiles"

SIZE_KB=$(du -sk "$PKGROOT" | cut -f1)

cat > "$PKGROOT/DEBIAN/control" <<EOF
Package: netrewind
Version: $VERSION
Section: net
Priority: optional
Architecture: $ARCH
Installed-Size: $SIZE_KB
Maintainer: NetRewind <https://github.com/OmarAlghafri/NetRewind>
Homepage: https://github.com/OmarAlghafri/NetRewind
Description: Network black-box recorder
 Watches netlink, eBPF and nftables for state changes and records them so an
 incident can be reconstructed after the fact, without packet capture. See
 docs/runbook.md in the source tree for what a given placement can and
 cannot see.
EOF

cat > "$PKGROOT/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = configure ] && [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
    echo "netrewindd installed. Review /etc/netrewind/netrewindd.yaml, then:"
    echo "  systemctl enable --now netrewindd"
fi
EOF
chmod 0755 "$PKGROOT/DEBIAN/postinst"

cat > "$PKGROOT/DEBIAN/postrm" <<'EOF'
#!/bin/sh
# The event store under /var/lib/netrewind is never touched here, on purge
# or otherwise: it is the record, and a package script that deleted evidence
# as a side effect of removal would be indefensible (deploy/install.sh
# states the same rule for the tarball install path).
set -e
if [ "$1" = purge ] || [ "$1" = remove ]; then
    if [ -d /run/systemd/system ]; then
        systemctl disable --now netrewindd 2>/dev/null || true
        systemctl daemon-reload || true
    fi
fi
EOF
chmod 0755 "$PKGROOT/DEBIAN/postrm"

mkdir -p "$OUT"
PKG="$OUT/netrewind_${VERSION}_${ARCH}.deb"
dpkg-deb --root-owner-group --build "$PKGROOT" "$PKG"
echo "==> built $PKG"
dpkg-deb --info "$PKG"
