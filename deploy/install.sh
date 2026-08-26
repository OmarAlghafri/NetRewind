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
    rm -f "$CONFDIR/netrewindd.yaml.sample"
    # The configuration is left: it is the operator's, not this script's.
    if [ -e "$CONFDIR/netrewindd.yaml" ]; then
        echo "keeping $CONFDIR/netrewindd.yaml"
    fi
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

echo "==> configuration into $CONFDIR"
install -d -m 0750 "$CONFDIR"
if [ -e "$CONFDIR/netrewindd.yaml" ]; then
    # An upgrade must never overwrite a configuration somebody tuned. The new
    # sample is left alongside so its comments can still be read.
    install -m 0640 "$HERE/deploy/netrewindd.yaml" "$CONFDIR/netrewindd.yaml.sample"
    echo "    keeping existing netrewindd.yaml (new sample at netrewindd.yaml.sample)"
else
    install -m 0640 "$HERE/deploy/netrewindd.yaml" "$CONFDIR/netrewindd.yaml"
fi

# Point the configuration at this host's paths, which may have been moved with
# PREFIX, CONFDIR or STATEDIR. Done before anything is started, and before the
# systemd check, so a host without systemd gets a correct file too.
sed -i "s|^db: .*|db: $STATEDIR/events.db|; s|^rules: .*|rules: $CONFDIR/rules|" \
    "$CONFDIR/netrewindd.yaml"

echo "==> state directory $STATEDIR"
install -d -m 0750 "$STATEDIR"

if [ ! -d "$UNITDIR" ]; then
    # Alpine, Void and Devuan are Linux hosts without systemd. The recorder
    # itself runs fine there; only the unit has nowhere to go, and saying so is
    # more use than a failed install command.
    echo
    echo "$UNITDIR does not exist, so there is no systemd to install a unit into."
    echo "The binaries and /etc/netrewind are in place. Run the recorder under"
    echo "whatever supervisor this host uses:"
    echo
    echo "    $PREFIX/netrewindd"
    echo
    "$PREFIX/netrewindd" --check-config
    exit 0
fi

echo "==> unit into $UNITDIR"
install -m 0644 "$HERE/deploy/systemd/$UNIT" "$UNITDIR/$UNIT"
# The unit needs no rewriting: it runs the recorder with no arguments and lets
# the recorder read /etc/netrewind. One file to look at rather than two.
systemctl daemon-reload

# Fail here rather than in a restart loop nobody is watching.
"$PREFIX/netrewindd" --check-config || die "the installed configuration is not usable"

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
