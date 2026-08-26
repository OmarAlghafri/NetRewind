#!/bin/sh
# What this kernel can support, checked rather than assumed.
#
# eBPF support is not one thing: BTF, ring buffers and each tracepoint are all
# separate capabilities a kernel may or may not have been built with, and
# discovering a missing one after writing a collector against it is expensive.

set -u

mount -t tracefs tracefs /sys/kernel/tracing 2>/dev/null || true
mount -t debugfs debugfs /sys/kernel/debug 2>/dev/null || true

TRACE=/sys/kernel/tracing
[ -d "$TRACE/events" ] || TRACE=/sys/kernel/debug/tracing

echo "kernel:  $(uname -r)"

if [ -r /sys/kernel/btf/vmlinux ]; then
    echo "btf:     present ($(( $(stat -c %s /sys/kernel/btf/vmlinux) / 1024 )) KiB) - CO-RE will work"
else
    echo "btf:     MISSING - CO-RE eBPF cannot load here"
fi

echo "clang:   $(clang --version 2>/dev/null | head -1 || echo 'not installed')"

for tp in sock/inet_sock_set_state tcp/tcp_retransmit_skb tcp/tcp_probe; do
    if [ -d "$TRACE/events/$tp" ]; then
        echo "tp:      OK   $tp"
    else
        echo "tp:      MISS $tp"
    fi
done

for tool in nft iptables ss; do
    if command -v "$tool" >/dev/null 2>&1; then
        echo "tool:    OK   $tool"
    else
        echo "tool:    MISS $tool"
    fi
done

if [ -d "$TRACE/events/sock/inet_sock_set_state" ]; then
    echo
    echo "inet_sock_set_state fields:"
    grep -E '^\s+field:' "$TRACE/events/sock/inet_sock_set_state/format" | sed 's/^/  /'
fi
