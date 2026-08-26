#!/bin/sh
# Bring a GNS3 node up: address it, then either record or just be a host.
#
# One image serves both roles. The recorder and the two ordinary hosts on the
# segment differ only by an environment variable, which keeps the lab to a
# single artefact to build, transfer and load onto the GNS3 VM.
#
# Environment:
#   NETREWIND_ADDR      CIDR for eth0, e.g. 10.1.10.50/24
#   NETREWIND_GATEWAY   default gateway
#   NETREWIND_RECORD    "off" to skip the recorder and be a plain host
#   NETREWIND_DB        where to keep the record

DB="${NETREWIND_DB:-/var/lib/netrewind/events.db}"

# GNS3 attaches interfaces asynchronously after the container starts. Seeding
# the collectors against a half-built node would record the topology coming up
# as though it were changing, so the first minute of every lab run would be
# noise that has nothing to do with the fault being studied.
i=0
while [ "$i" -lt 30 ]; do
    if ip -o link show eth0 >/dev/null 2>&1; then
        break
    fi
    i=$((i + 1))
    sleep 1
done

if [ -n "$NETREWIND_ADDR" ]; then
    ip addr flush dev eth0 2>/dev/null
    ip addr add "$NETREWIND_ADDR" dev eth0 2>/dev/null
    ip link set eth0 up
    if [ -n "$NETREWIND_GATEWAY" ]; then
        # The switchport takes a moment to leave spanning-tree listening even
        # with portfast, so the gateway may not be reachable on the first try.
        j=0
        while [ "$j" -lt 20 ]; do
            ip route replace default via "$NETREWIND_GATEWAY" dev eth0 2>/dev/null && break
            j=$((j + 1))
            sleep 1
        done
    fi
fi

# The eBPF collector attaches to a kernel tracepoint, and finding one means
# reading tracefs. A container gets its own mount namespace without it, so the
# collector would report itself down and the whole flow.* family would be
# missing from a lab built to study connections.
#
# The GNS3 VM's kernel carries BTF, so this is the only thing standing between
# the node and full connection observation. Best effort: if the container is not
# permitted to mount, the recorder says so in the record rather than pretending.
if [ ! -d /sys/kernel/tracing/events ]; then
    mount -t tracefs tracefs /sys/kernel/tracing 2>/dev/null ||
        mount -t debugfs debugfs /sys/kernel/debug 2>/dev/null || true
fi

if [ "$NETREWIND_RECORD" = "off" ]; then
    cat <<'HOST'

  An ordinary host on this segment. It exists so the recorder has neighbours
  to watch: ping things, and its ARP entries become events next door.

HOST
    export PS1='host:\w$ '
    exec /bin/sh
fi

netrewindd --db "$DB" --rules /etc/netrewind/rules \
    --observer-id "$(hostname)" --probe "${NETREWIND_GATEWAY:-}" \
    > /var/log/netrewindd.log 2>&1 &

cat <<'BANNER'

  NetRewind is recording this segment.

    netrewind timeline --last 15m      what happened, narrated
    netrewind incidents --last 1h      what it concluded, with the chain
    netrewind events --family system   whether it has any blind spots
    tail -f /var/log/netrewindd.log    the recorder's own log

BANNER

export PS1='netrewind:\w$ '
exec /bin/sh
