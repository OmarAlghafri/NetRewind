#!/bin/sh
# M0 acceptance: prove that a real kernel state change reaches the CLI.
#
# Creates a throwaway interface, records it going down and coming back, then
# asks the recorder what happened. Run as root on any Linux kernel - a VM, a
# container, or WSL2 - since all it needs is netlink and one spare interface.
#
#   sudo lab/accept-m0.sh [path-to-build-dir]

set -eu

BUILD="${1:-./build}"
DAEMON="$BUILD/netrewindd"
CLI="$BUILD/netrewind"
DB="${NETREWIND_DB:-/tmp/netrewind-accept/events.db}"
IFACE="nracc0"
PEER="nracc1"

[ -x "$DAEMON" ] || { echo "not found: $DAEMON" >&2; exit 1; }
[ -x "$CLI" ] || { echo "not found: $CLI" >&2; exit 1; }

cleanup() {
    [ -n "${DAEMON_PID:-}" ] && kill "$DAEMON_PID" 2>/dev/null || true
    ip link del "$IFACE" 2>/dev/null || true
}
trap cleanup EXIT

rm -rf "$(dirname "$DB")"
mkdir -p "$(dirname "$DB")"

# A dummy interface is the cleanest subject; veth is the fallback where the
# dummy module is not built in.
if ip link add "$IFACE" type dummy 2>/dev/null; then
    KIND=dummy
elif ip link add "$IFACE" type veth peer name "$PEER" 2>/dev/null; then
    KIND=veth
    ip link set "$PEER" up
else
    echo "could not create a test interface: no dummy or veth support" >&2
    exit 1
fi
ip link set "$IFACE" up
echo "== created $IFACE ($KIND), currently up"

# Start the recorder only now, so the interface is part of its seeded baseline.
# Without that seeding the first notification would look like a change.
"$DAEMON" --db "$DB" --log-level info >/tmp/netrewind-accept.log 2>&1 &
DAEMON_PID=$!
sleep 2
kill -0 "$DAEMON_PID" 2>/dev/null || { echo "recorder died on startup:" >&2; cat /tmp/netrewind-accept.log >&2; exit 1; }
echo "== recorder running (pid $DAEMON_PID)"

echo "== taking $IFACE down"
ip link set "$IFACE" down
sleep 3
echo "== bringing $IFACE back up"
ip link set "$IFACE" up
sleep 2

kill "$DAEMON_PID" 2>/dev/null || true
wait "$DAEMON_PID" 2>/dev/null || true
DAEMON_PID=""
sleep 1

echo
echo "== what the recorder saw =="
"$CLI" events --db "$DB" --last 5m --family link

# The gate: a down and an up, both attributed to this interface.
DOWN=$("$CLI" events --db "$DB" --last 5m --kind link.down -o json | grep -c "\"$IFACE\"" || true)
UP=$("$CLI" events --db "$DB" --last 5m --kind link.up -o json | grep -c "\"$IFACE\"" || true)

echo
if [ "$DOWN" -ge 1 ] && [ "$UP" -ge 1 ]; then
    echo "M0 PASS: the outage was recorded and replayed from the store"
    exit 0
fi
echo "M0 FAIL: expected a link.down and a link.up for $IFACE" >&2
echo "recorder log:" >&2
cat /tmp/netrewind-accept.log >&2
exit 1
