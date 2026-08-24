#!/bin/sh
# Fault injection against a synthetic topology.
#
# Everything happens inside a dedicated network namespace, so a scenario can
# delete the default route or hijack the gateway without touching the machine's
# real networking. The recorder runs inside that namespace too and sees only the
# lab, which makes every run reproducible and safe to repeat.
#
#   sudo lab/inject.sh all              build, record, inject everything, report
#   sudo lab/inject.sh setup            build the topology only
#   sudo lab/inject.sh run <scenario>   inject one fault into a running lab
#   sudo lab/inject.sh teardown
#
# Topology
#     netns nrlab                       the observer's world
#       nrlab0  10.99.0.1/24  <-veth->  netns nrh1  10.99.0.11/24
#       nrlab1  10.99.1.1/24  <-veth->  netns nrh2  10.99.1.11/24

set -eu

BUILD="${BUILD:-./build}"
DAEMON="$BUILD/netrewindd"
CLI="$BUILD/netrewind"
DB="${NETREWIND_DB:-/tmp/netrewind-lab/events.db}"
RULES="${RULES:-./rules}"
METRICS_ADDR="${METRICS_ADDR:-127.0.0.1:9464}"
LOG=/tmp/netrewind-lab/recorder.log

LAB=nrlab
H1=nrh1
H2=nrh2
MAC_H1_REAL=02:00:00:00:00:11
MAC_H1_FAKE=02:00:00:00:00:aa
MAC_H1_OTHER=02:00:00:00:00:bb
GW_A=10.99.0.201
GW_B=10.99.0.202

inlab() { ip netns exec "$LAB" "$@"; }

setup() {
    teardown 2>/dev/null || true
    ip netns add "$LAB"
    ip netns add "$H1"
    ip netns add "$H2"

    ip link add nrlab0 type veth peer name nrp0
    ip link set nrlab0 netns "$LAB"
    ip link set nrp0 netns "$H1"

    ip link add nrlab1 type veth peer name nrp1
    ip link set nrlab1 netns "$LAB"
    ip link set nrp1 netns "$H2"

    inlab ip link set lo up
    inlab ip addr add 10.99.0.1/24 dev nrlab0
    inlab ip addr add 10.99.1.1/24 dev nrlab1
    inlab ip link set nrlab0 up
    inlab ip link set nrlab1 up

    ip netns exec "$H1" ip link set lo up
    ip netns exec "$H1" ip link set nrp0 address "$MAC_H1_REAL"
    ip netns exec "$H1" ip addr add 10.99.0.11/24 dev nrp0
    ip netns exec "$H1" ip link set nrp0 up

    ip netns exec "$H2" ip link set lo up
    ip netns exec "$H2" ip addr add 10.99.1.11/24 dev nrp1
    ip netns exec "$H2" ip link set nrp1 up

    # nft is needed by the filtering scenario and by the policy collector.
    apk add --no-cache nftables >/dev/null 2>&1 || true

    # Give the neighbour table something real to start from, and install a
    # default route so the gateway scenarios have one to attack.
    inlab ping -c1 -W1 10.99.0.11 >/dev/null 2>&1 || true
    inlab ip neigh replace "$GW_A" lladdr 02:00:00:00:00:01 dev nrlab0 nud reachable
    inlab ip route add default via "$GW_A" dev nrlab0 metric 100
    echo "lab up: netns $LAB with hosts $H1 and $H2"
}

teardown() {
    ip netns del "$LAB" 2>/dev/null || true
    ip netns del "$H1" 2>/dev/null || true
    ip netns del "$H2" 2>/dev/null || true
    ip link del nrlab0 2>/dev/null || true
    ip link del nrlab1 2>/dev/null || true
}

# Each scenario is one fault a network actually suffers, expressed in the
# smallest number of commands that reproduce it.
scenario_link_flap() {
    echo "-- injecting: access port flaps"
    inlab ip link set nrlab1 down
    sleep 2
    inlab ip link set nrlab1 up
}

scenario_arp_change() {
    echo "-- injecting: a host's hardware address changes under its address"
    inlab ip neigh replace 10.99.0.11 lladdr "$MAC_H1_FAKE" dev nrlab0 nud reachable
}

scenario_gateway_hijack() {
    echo "-- injecting: the default gateway is answered by a different machine"
    inlab ip neigh replace "$GW_A" lladdr 02:00:00:00:00:ff dev nrlab0 nud reachable
}

scenario_duplicate_ip() {
    echo "-- injecting: two machines claim one address"
    inlab ip neigh replace 10.99.0.11 lladdr "$MAC_H1_OTHER" dev nrlab0 nud reachable
    sleep 1
    inlab ip neigh replace 10.99.0.11 lladdr "$MAC_H1_FAKE" dev nrlab0 nud reachable
    sleep 1
    inlab ip neigh replace 10.99.0.11 lladdr "$MAC_H1_OTHER" dev nrlab0 nud reachable
}

scenario_route_change() {
    echo "-- injecting: a prefix is re-pointed at a different next hop"
    inlab ip route add 10.200.0.0/24 via 10.99.0.11 dev nrlab0 metric 100
    sleep 1
    inlab ip route replace 10.200.0.0/24 via 10.99.1.11 dev nrlab1 metric 100
}

scenario_default_route_moved() {
    echo "-- injecting: the default route is re-pointed"
    inlab ip neigh replace "$GW_B" lladdr 02:00:00:00:00:02 dev nrlab0 nud reachable
    inlab ip route replace default via "$GW_B" dev nrlab0 metric 100
}

scenario_default_route_lost() {
    echo "-- injecting: the default route disappears"
    inlab ip route del default 2>/dev/null || true
}

scenario_service_unreachable() {
    echo "-- injecting: a service that does not answer"
    # Nothing is listening on 9999, so each attempt goes from the opening SYN
    # straight to closed - which is what a filtered port, a dead service and a
    # broken path all look like from the client.
    # busybox explicitly: whichever netcat is installed changes the flags, and
    # a scenario that silently stops connecting proves nothing.
    i=0
    while [ "$i" -lt 4 ]; do
        inlab busybox nc -w 1 10.99.0.11 9999 </dev/null >/dev/null 2>&1 || true
        i=$((i + 1))
    done
}

scenario_normal_traffic() {
    echo "-- generating: connections that succeed, for the rollup"
    i=0
    while [ "$i" -lt 3 ]; do
        ip netns exec "$H1" busybox nc -l -p 9100 >/dev/null 2>&1 &
        listener=$!
        sleep 1
        echo hello | inlab busybox nc -w 2 10.99.0.11 9100 >/dev/null 2>&1 || true
        kill "$listener" 2>/dev/null || true
        i=$((i + 1))
    done
}

scenario_path_broke() {
    echo "-- injecting: a working path is broken by an ARP change"
    # The whole point of the recorder in one scenario: two machines that were
    # connecting fine, a change on the path, and then they cannot. Both halves
    # are observable, so the chain can be drawn between them.
    ip netns exec "$H2" busybox nc -l -p 9200 >/dev/null 2>&1 &
    listener=$!
    sleep 1
    echo hello | inlab busybox nc -w 2 10.99.1.11 9200 >/dev/null 2>&1 || true
    sleep 1

    # Point the neighbour entry at hardware that is not there. Packets now go
    # into the void, so the handshake times out rather than being refused.
    inlab ip neigh replace 10.99.1.11 lladdr 02:00:00:00:00:de dev nrlab1 nud permanent
    sleep 1
    inlab busybox nc -w 3 10.99.1.11 9200 </dev/null >/dev/null 2>&1 || true

    kill "$listener" 2>/dev/null || true
    inlab ip neigh del 10.99.1.11 dev nrlab1 2>/dev/null || true
}

scenario_policy_broke_a_path() {
    echo "-- injecting: a filtering rule breaks a path that was working"
    if ! command -v nft >/dev/null 2>&1; then
        echo "   (skipped: nft not installed)"
        return 0
    fi

    ip netns exec "$H1" busybox nc -l -p 9300 >/dev/null 2>&1 &
    listener=$!
    sleep 1
    echo hello | inlab busybox nc -w 2 10.99.0.11 9300 >/dev/null 2>&1 || true
    sleep 1

    # nftables is per network namespace, so this cannot escape the lab.
    inlab nft add table inet nrlab 2>/dev/null || true
    inlab nft add chain inet nrlab out '{ type filter hook output priority 0; }' 2>/dev/null || true
    inlab nft add rule inet nrlab out tcp dport 9300 drop

    # The ruleset is polled, so the change has to be given time to be noticed
    # before the consequence arrives - otherwise the chain reads backwards.
    echo "   waiting for the ruleset poll"
    sleep 7

    # Dropped rather than refused, so the handshake times out instead of being
    # answered. That is what a filtering change looks like from the client.
    inlab busybox nc -w 3 10.99.0.11 9300 </dev/null >/dev/null 2>&1 || true

    kill "$listener" 2>/dev/null || true
    inlab nft delete table inet nrlab 2>/dev/null || true
}

SCENARIOS="link_flap arp_change gateway_hijack duplicate_ip route_change default_route_moved default_route_lost service_unreachable normal_traffic path_broke policy_broke_a_path"

run_one() {
    name=$(echo "$1" | tr '-' '_')
    case " $SCENARIOS " in
        *" $name "*) "scenario_$name" ;;
        *) echo "unknown scenario: $1" >&2
           echo "available: $(echo "$SCENARIOS" | tr '_' '-')" >&2
           exit 1 ;;
    esac
}

all() {
    [ -x "$DAEMON" ] || { echo "not found: $DAEMON" >&2; exit 1; }
    [ -x "$CLI" ] || { echo "not found: $CLI" >&2; exit 1; }

    rm -rf "$(dirname "$DB")"
    mkdir -p "$(dirname "$DB")"
    setup

    # The recorder starts after the topology exists, so the healthy state is its
    # baseline and only the injected faults read as changes.
    #
    # tracefs has to be mounted inside the same `ip netns exec` that runs the
    # recorder: entering a network namespace gets a fresh mount namespace with
    # /sys remounted, and eBPF tracepoints cannot be attached without it.
    inlab sh -c "mount -t tracefs tracefs /sys/kernel/tracing 2>/dev/null || true
                 exec '$DAEMON' --db '$DB' --rules '$RULES' --log-level info \
                     --metrics-addr '$METRICS_ADDR'" >"$LOG" 2>&1 &
    pid=$!
    sleep 2
    kill -0 "$pid" 2>/dev/null || { echo "recorder died:" >&2; cat "$LOG" >&2; teardown; exit 1; }
    echo "recorder running in netns $LAB (pid $pid)"
    echo

    for s in $SCENARIOS; do
        run_one "$s"
        sleep 1
    done

    # Connection activity is summarised on a timer rather than reported per
    # connection, so the run has to outlast one rollup interval to see it.
    echo "-- waiting for a connection rollup"
    sleep 12

    # Scrape before stopping: the interesting metrics are the ones that say
    # what the recorder missed, and they are only reachable while it runs.
    echo
    echo "================ metrics the recorder exports =================="
    METRICS=$(inlab busybox wget -qO- "http://$METRICS_ADDR/metrics" 2>/dev/null || true)
    if [ -n "$METRICS" ]; then
        echo "$METRICS" | grep -E "^netrewind_(build_info|recorder_blind|dropped|clock_steps|collector_up)" || true
        echo "$METRICS" | grep -E "^netrewind_(events|incidents)_total" | head -12 || true
    else
        echo "  (metrics endpoint did not answer)"
    fi

    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    sleep 1

    echo
    echo "================ what the recorder reconstructed ================"
    "$CLI" timeline --db "$DB" --last 10m
    echo
    echo "================ following one host across the incident ========"
    "$CLI" what-happened --db "$DB" --host 10.99.0.11 --at now --window 10m
    echo
    echo "================ what correlation concluded ===================="
    "$CLI" incidents --db "$DB" --last 10m
    echo

    teardown
    assert_expected
}

# The gate: every injected fault has to be findable in the record afterwards.
assert_expected() {
    fail=0
    for kind in link.down link.up l2.arp_binding_changed l2.duplicate_ip \
                l3.route_added l3.default_route_changed l3.route_removed \
                flow.handshake_fail flow.rollup flow.first_failure_for_pair \
                policy.rule_changed; do
        n=$("$CLI" events --db "$DB" --last 10m --kind "$kind" -o json | grep -c '"event_id"' || true)
        if [ "$n" -ge 1 ]; then
            printf '  ok    %-28s %s recorded\n' "$kind" "$n"
        else
            printf '  MISS  %-28s not recorded\n' "$kind"
            fail=1
        fi
    done
    echo
    # Correlation has to reach the right conclusion, not merely have the
    # evidence available to reach it.
    for rule in gateway-hijack contested-address default-route-moved \
                default-route-lost service-unreachable change-broke-a-path; do
        n=$("$CLI" incidents --db "$DB" --last 10m --rule "$rule" -o json | grep -c '"incident_id"' || true)
        if [ "$n" -ge 1 ]; then
            printf '  ok    %-28s concluded\n' "$rule"
        else
            printf '  MISS  %-28s not concluded\n' "$rule"
            fail=1
        fi
    done

    echo
    if [ "$fail" -eq 0 ]; then
        echo "PASS: every injected fault was reconstructed, and correlation named the cause"
        exit 0
    fi
    echo "FAIL: something injected left no trace, or was not recognised" >&2
    echo "recorder log:" >&2
    cat "$LOG" >&2
    exit 1
}

case "${1:-all}" in
    setup)    setup ;;
    teardown) teardown ;;
    run)      run_one "${2:?usage: inject.sh run <scenario>}" ;;
    all)      all ;;
    *)        echo "usage: $0 {all|setup|teardown|run <scenario>}" >&2; exit 1 ;;
esac
