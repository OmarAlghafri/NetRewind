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
# Created rather than assumed. /usr/local/bin exists on most systems, which is
# why this was missing for so long, and on the ones where it does not - a
# minimal container image, a stripped appliance root - the install stopped
# here, after the kernel checks and before anything else, with a message about
# a file it could not create rather than a directory it needed.
install -d -m 0755 "$PREFIX"
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
    #
    # Nor may it be edited in passing. This script used to point db: and rules:
    # at this host's paths unconditionally, including on an upgrade - so an
    # operator who had moved the store to a bigger disk got it moved back, the
    # recorder started a fresh one at the default path, and the timeline they
    # had been keeping for months read as empty. A recorder that appears to
    # have no history is the exact confusion this project exists to remove, and
    # an upgrade is the worst moment to produce it.
    install -m 0640 "$HERE/deploy/netrewindd.yaml" "$CONFDIR/netrewindd.yaml.sample"
    echo "    keeping existing netrewindd.yaml unchanged (new sample at netrewindd.yaml.sample)"
    echo "    store: $(sed -n 's/^db: *//p' "$CONFDIR/netrewindd.yaml" | head -1)"
else
    install -m 0640 "$HERE/deploy/netrewindd.yaml" "$CONFDIR/netrewindd.yaml"
    # Point a fresh configuration at this host's paths, which may have been
    # moved with CONFDIR or STATEDIR. Done before anything is started, and
    # before the systemd check, so a host without systemd gets a correct file
    # too.
    sed -i "s|^db: .*|db: $STATEDIR/events.db|; s|^rules: .*|rules: $CONFDIR/rules|" \
        "$CONFDIR/netrewindd.yaml"
fi

# The recorder reads /etc/netrewind/netrewindd.yaml on its own; anywhere else
# has to be named. Without this, CONFDIR was accepted and then ignored, and the
# check below - along with the running service - read a file that was not the
# one just installed.
CONFIG_FLAG=""
if [ "$CONFDIR" != /etc/netrewind ]; then
    CONFIG_FLAG=" --config $CONFDIR/netrewindd.yaml"
fi

echo "==> state directory $STATEDIR"
install -d -m 0750 "$STATEDIR"

if [ ! -d "$UNITDIR" ]; then
    # Alpine, Void and Devuan are Linux hosts without systemd. The recorder
    # itself runs fine there; only the unit has nowhere to go, and saying so is
    # more use than a failed install command.
    echo
    echo "$UNITDIR does not exist, so there is no systemd to install a unit into."
    echo "The binaries and $CONFDIR are in place. Run the recorder under"
    echo "whatever supervisor this host uses:"
    echo
    echo "    $PREFIX/netrewindd$CONFIG_FLAG"
    echo
    # shellcheck disable=SC2086 # CONFIG_FLAG is two words or none, deliberately
    "$PREFIX/netrewindd" --check-config $CONFIG_FLAG
    exit 0
fi

echo "==> unit into $UNITDIR"
# The unit ships with the default paths in it, and is rewritten here only so
# that PREFIX and CONFDIR mean something. With no overrides the result is the
# file as shipped.
sed -e "s|^ExecStart=.*|ExecStart=$PREFIX/netrewindd$CONFIG_FLAG|" \
    -e "s|^ExecStartPre=.*|ExecStartPre=$PREFIX/netrewindd --check-config$CONFIG_FLAG|" \
    -e "s|^ReadOnlyPaths=/etc/netrewind$|ReadOnlyPaths=$CONFDIR|" \
    "$HERE/deploy/systemd/$UNIT" > "$UNITDIR/$UNIT"
chmod 0644 "$UNITDIR/$UNIT"
systemctl daemon-reload

# Fail here rather than in a restart loop nobody is watching.
# shellcheck disable=SC2086
"$PREFIX/netrewindd" --check-config $CONFIG_FLAG || die "the installed configuration is not usable"

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
