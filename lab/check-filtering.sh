#!/bin/sh
# Whether this kernel can support a filtering-decision collector.
#
# nftables and conntrack are both kernel features, not just userspace tools:
# installing nft proves nothing if the kernel was built without nf_tables.

set -u

apk add --no-cache nftables conntrack-tools >/dev/null 2>&1 || true

if command -v nft >/dev/null 2>&1; then
    echo "nft binary:    $(nft --version 2>/dev/null)"
else
    echo "nft binary:    MISSING"
fi

if nft list ruleset >/dev/null 2>&1; then
    echo "nft kernel:    OK"
else
    echo "nft kernel:    UNSUPPORTED"
fi

if [ -r /proc/net/nf_conntrack ]; then
    echo "conntrack:     OK ($(wc -l < /proc/net/nf_conntrack) entries)"
else
    modprobe nf_conntrack 2>/dev/null || true
    if [ -r /proc/net/nf_conntrack ]; then
        echo "conntrack:     OK after modprobe"
    else
        echo "conntrack:     UNSUPPORTED (no /proc/net/nf_conntrack)"
    fi
fi
