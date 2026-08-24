#!/bin/sh
# Why did the wire collector see nothing?
#
# Kept in the repository rather than typed at a prompt: the next person to hit
# this will want the same four answers, and the shape of the question is worth
# more than the shape of one run.

set -u
BUILD="${BUILD:-./build}"
LOG=/tmp/netrewind-lab/recorder.log

echo "== what the recorder said about the wire collector"
grep -iE "wire|collector failed|watching" "$LOG" 2>/dev/null | head -8 || echo "  (no log)"

echo
echo "== is the injector present and runnable"
if [ -x "$BUILD/nrinject" ]; then
    ls -l "$BUILD/nrinject"
else
    echo "  MISSING: $BUILD/nrinject"
fi

echo
echo "== send one frame and see whether it leaves"
./inject.sh setup >/dev/null 2>&1
ip netns exec nrlab "$BUILD/nrinject" dhcp -iface nrlab0 \
    -server 10.99.0.1 -mac 02:00:00:00:00:01 -gw 10.99.0.1
echo "  injector exit: $?"

echo
echo "== does a capture on that interface see it"
ip netns exec nrlab timeout 3 "$BUILD/netrewindd" \
    --db /tmp/diag.db --rules "" --log-level debug >/tmp/diag.log 2>&1 &
sleep 2
ip netns exec nrlab "$BUILD/nrinject" dhcp -iface nrlab0 \
    -server 10.99.0.99 -mac 02:00:00:00:00:99 -gw 10.99.0.99 -offer 10.99.0.77
sleep 2
grep -iE "wire|dhcp" /tmp/diag.log | head -10 || echo "  nothing about the wire collector"

./inject.sh teardown >/dev/null 2>&1
rm -f /tmp/diag.db*
