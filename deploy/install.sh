#!/bin/sh
# Install NetRewind from a release tarball onto a Linux host.
#
#   sudo ./install.sh              install and start
#   sudo ./install.sh --no-start   install only
#   sudo ./install.sh --uninstall  remove the binaries, unit and rules
#
# The event store is never touched by --uninstall. It is the record, and a
# script that deleted evidence as a side effect of removing a program would be
# indefensible.

set -eu

PREFIX="${PREFIX:-/usr/local/bin}"
CONFDIR="${CONFDIR:-/etc/netrewind}"
UNITDIR="${UNITDIR:-/etc/systemd/system}"
STATEDIR=/var/lib/netrewind
UNIT=netrewindd.service
HERE=$(cd "$(dirname "$0")" && pwd)

die() { echo "install: $1" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run this as root"

uninstall() {
    echo "stopping and disabling $UNIT"
    systemctl disable --now "$UNIT" 2>/dev/null || true
    rm -f "$UNITDIR/$UNIT"
    systemctl daemon-reload 2>/dev/null || true
    rm -f "$PREFIX/netrewindd" "$PREFIX/netrewind"
    rm -rf "$CONFDIR/rules"
    echo
    echo "removed. The event store under $STATEDIR was left alone:"
    echo "  that is the record, and this script does not delete evidence."
    exit 0
}

start=yes
case "${1:-}" in
    --uninstall) uninstall ;;
    --no-start)  start=no ;;
    "")          ;;
    *)           die "unknown option: $1" ;;
esac

# Refuse early rather than half-installing onto a kernel that cannot run it.
[ -d /sys/class/net ] || die "this does not look like Linux"
if [ ! -r /sys/kernel/btf/vmlinux ]; then
    echo "warning: no /sys/kernel/btf/vmlinux - the eBPF connection collector"
    echo "         will not load. Everything netlink-based still works."
fi

echo "==> binaries into $PREFIX"
install -m 0755 "$HERE/netrewindd" "$PREFIX/netrewindd"
install -m 0755 "$HERE/netrewind" "$PREFIX/netrewind"

echo "==> rules into $CONFDIR/rules"
install -d -m 0750 "$CONFDIR/rules"
for f in "$HERE"/rules/*.yaml; do
    # Never overwrite a rule that has been edited locally: the library is meant
    # to be added to, and an upgrade that silently reverted someone's rule
    # would teach them not to write any.
    target="$CONFDIR/rules/$(basename "$f")"
    if [ -e "$target" ]; then
        echo "    keeping existing $(basename "$f")"
    else
        install -m 0640 "$f" "$target"
    fi
done

echo "==> state directory $STATEDIR"
install -d -m 0750 "$STATEDIR"

echo "==> unit into $UNITDIR"
install -m 0644 "$HERE/deploy/systemd/$UNIT" "$UNITDIR/$UNIT"
# The packaged unit points at the repository layout; a host install reads its
# rules from /etc.
sed -i "s|--db /var/lib/netrewind/events.db|--db $STATEDIR/events.db --rules $CONFDIR/rules|" \
    "$UNITDIR/$UNIT"
systemctl daemon-reload

if [ "$start" = yes ]; then
    echo "==> starting"
    systemctl enable --now "$UNIT"
    sleep 2
    systemctl --no-pager --lines=8 status "$UNIT" || true
else
    echo "==> not started (--no-start). Start it with:"
    echo "    systemctl enable --now $UNIT"
fi

cat <<'DONE'

Installed. Three things worth doing next:

  netrewind timeline --last 15m        read the record
  netrewind serve                      read it in a browser, on loopback
  netrewind events --family system     check whether it has any blind spots

Before trusting the record, decide where this host sits relative to the traffic
you care about: the recorder sees what reaches it, and docs/runbook.md sets out
what each position costs you.
DONE
