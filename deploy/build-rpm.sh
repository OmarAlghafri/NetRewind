#!/bin/sh
# Build an .rpm package from release binaries.
#
#   VERSION=0.9.2 ARCH=x86_64 sudo deploy/build-rpm.sh
#
# Mirrors deploy/build-deb.sh's reasoning: rpm's own %config(noreplace)
# mechanism is what gives an upgrade the same "never silently overwrite a
# configuration or rule file somebody edited" guarantee deploy/install.sh
# enforces by hand for the tarball path, so the spec below uses it instead
# of reimplementing that logic.
set -eu

VERSION="${VERSION:-dev}"
VERSION="${VERSION#v}"
# rpm version strings cannot contain a hyphen; a "-dirty" git-describe
# suffix has to become something rpm accepts instead of failing the build.
VERSION=$(echo "$VERSION" | tr '-' '_')
ARCH="${ARCH:-x86_64}"
BUILD="${BUILD:-./build}"
OUT="${OUT:-./dist}"
TOPDIR="$(mktemp -d)"
trap 'rm -rf "$TOPDIR"' EXIT

GOARCH=amd64
[ "$ARCH" = "aarch64" ] && GOARCH=arm64

DAEMON="$BUILD/netrewindd-linux-$GOARCH"
CLI="$BUILD/netrewind-linux-$GOARCH"
[ -x "$DAEMON" ] || DAEMON="$BUILD/netrewindd"
[ -x "$CLI" ] || CLI="$BUILD/netrewind"
[ -x "$DAEMON" ] || { echo "build-rpm: not found or not executable: $DAEMON (build it first)" >&2; exit 1; }
[ -x "$CLI" ] || { echo "build-rpm: not found or not executable: $CLI" >&2; exit 1; }

for d in BUILD RPMS SOURCES SPECS SRPMS BUILDROOT; do
    mkdir -p "$TOPDIR/$d"
done

DAEMON_ABS=$(cd "$(dirname "$DAEMON")" && pwd)/$(basename "$DAEMON")
CLI_ABS=$(cd "$(dirname "$CLI")" && pwd)/$(basename "$CLI")
ROOT_ABS=$(pwd)

cat > "$TOPDIR/SPECS/netrewind.spec" <<EOF
Name: netrewind
Version: $VERSION
Release: 1
Summary: Network black-box recorder
License: AGPL-3.0-only
URL: https://github.com/OmarAlghafri/NetRewind
BuildArch: $ARCH
# Weak dependency, same reasoning as deploy/build-deb.sh's Recommends: the
# policy collector needs nft(8), the rest of the recorder does not, and a
# host without it gets a collector-not-watching incident, not a dead unit.
Recommends: nftables
# The Go binaries are already static and built without debug info by the
# real release path (Makefile's -ldflags "-s -w"); rpm's own post-build
# stripping/debug-package machinery assumes a GNU userland (file, xargs -d)
# that is not present when building on a musl/busybox host such as this
# project's own WSL2 lab distro (docs/dev-environment.md), so it is turned
# off here rather than worked around per-host.
%global debug_package %{nil}
%global __os_install_post %{nil}
%description
Watches netlink, eBPF and nftables for state changes and records them so an
incident can be reconstructed after the fact, without packet capture.

%install
install -d -m 0755 %{buildroot}/usr/local/bin
install -d -m 0750 %{buildroot}/etc/netrewind/rules
install -d -m 0750 %{buildroot}/var/lib/netrewind
install -d -m 0755 %{buildroot}/etc/systemd/system
install -m 0755 "$DAEMON_ABS" %{buildroot}/usr/local/bin/netrewindd
install -m 0755 "$CLI_ABS" %{buildroot}/usr/local/bin/netrewind
install -m 0640 "$ROOT_ABS/deploy/netrewindd.yaml" %{buildroot}/etc/netrewind/netrewindd.yaml
sed -i "s|^db: .*|db: /var/lib/netrewind/events.db|; s|^rules: .*|rules: /etc/netrewind/rules|" \\
    %{buildroot}/etc/netrewind/netrewindd.yaml
for f in "$ROOT_ABS"/rules/*.yaml; do
    install -m 0640 "\$f" "%{buildroot}/etc/netrewind/rules/\$(basename "\$f")"
done
install -m 0644 "$ROOT_ABS/deploy/systemd/netrewindd.service" %{buildroot}/etc/systemd/system/netrewindd.service

%files
%attr(0755,root,root) /usr/local/bin/netrewindd
%attr(0755,root,root) /usr/local/bin/netrewind
%attr(0644,root,root) /etc/systemd/system/netrewindd.service
%dir %attr(0750,root,root) /var/lib/netrewind
%config(noreplace) %attr(0640,root,root) /etc/netrewind/netrewindd.yaml
%config(noreplace) %attr(0640,root,root) /etc/netrewind/rules/*.yaml

%pre
# The group the local API socket is shared with (api.group in
# netrewindd.yaml); created before the files land so the unit can use it.
getent group netrewind >/dev/null || groupadd --system netrewind

%post
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
fi

%preun
# The event store under /var/lib/netrewind is never touched here, on any
# removal: it is the record. Same rule as deploy/install.sh and
# deploy/build-deb.sh's postrm. %preun runs while the unit file still
# exists, so "disable --now" works here (unlike a dpkg postrm).
if [ "\$1" = 0 ] && [ -d /run/systemd/system ]; then
    systemctl disable --now netrewindd 2>/dev/null || true
fi

%postun
# \$1 >= 1 is an upgrade: the new binary is on disk but the old process is
# still running it. Restart only if it was running (same as the .deb's
# postinst); a final removal (\$1 = 0) was already handled in %preun.
if [ "\$1" -ge 1 ] && [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
    systemctl try-restart netrewindd 2>/dev/null || true
fi
EOF

mkdir -p "$OUT"
rpmbuild --define "_topdir $TOPDIR" -bb "$TOPDIR/SPECS/netrewind.spec"
find "$TOPDIR/RPMS" -name '*.rpm' -exec cp {} "$OUT/" \;
echo "==> built:"
find "$OUT" -name "netrewind-${VERSION}*.rpm"
