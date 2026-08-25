# NetRewind as a container.
#
# One of the three ways the recorder is delivered, and the one to reach for when
# you want to watch a segment for an afternoon without installing anything. It
# is not the appliance: a container shares the host's kernel, so what it can
# observe is the host's network namespace, not its own.
#
#   docker build -f deploy/Dockerfile -t netrewind .
#   docker run --rm -it \
#     --network host --pid host \
#     --cap-add NET_ADMIN --cap-add BPF --cap-add PERFMON \
#     -v /sys/kernel/tracing:/sys/kernel/tracing \
#     -v netrewind-data:/var/lib/netrewind \
#     -p 127.0.0.1:8464:8464 \
#     netrewind
#
# --network host is not optional. Without it the recorder watches the container's
# own namespace, which is a network of one interface that nothing interesting
# ever happens on - it would run, report nothing, and look like a quiet network.
#
# The tracing mount is what lets eBPF attach its tracepoint, and it only works
# if the host actually has tracefs mounted there - check with
# `mount | grep tracefs` before assuming it does. A bind mount of a path that
# does not exist gives the container an empty directory rather than an error,
# so this fails quietly if you skip that check.
#
# Docker Desktop on macOS and Windows runs containers inside a Linux VM that
# does not mount tracefs at all, so connection observation is not available
# there however the container is run. Everything netlink-based works.
#
# Either way the recorder says so rather than looking healthy: the flow
# collector reports why it could not attach, and that becomes a
# system.collector_down event in the record and an incident explaining that an
# absence of connection failures proves nothing on this host.

# ---- build ----------------------------------------------------------------
FROM golang:1.26.6-alpine AS build

WORKDIR /src

# Dependencies first, so a source change does not re-download the module graph.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
# CGO off: the SQLite driver is pure Go, so the result is a static binary that
# runs on a scratch image and on any distribution without a libc to match.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/netrewindd ./cmd/netrewindd && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/netrewind ./cmd/netrewind

# ---- run ------------------------------------------------------------------
# Alpine rather than scratch for one reason: nft. The filtering collector shells
# out to it, and without it that one collector reports why and the rest carry on.
FROM alpine:3.24

RUN apk add --no-cache nftables iproute2 ca-certificates && \
    adduser -D -H -u 10001 netrewind

COPY --from=build /out/netrewindd /usr/local/bin/netrewindd
COPY --from=build /out/netrewind  /usr/local/bin/netrewind
COPY rules/ /etc/netrewind/rules/
COPY deploy/netrewindd.yaml /etc/netrewind/netrewindd.yaml

# Runs as root by default because reading netlink and loading eBPF need
# capabilities the container has to be granted anyway. The unprivileged user is
# here for `docker run --user 10001 netrewind serve`, which only reads a store.
VOLUME /var/lib/netrewind
EXPOSE 8464 9464

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
    CMD netrewind events --db /var/lib/netrewind/events.db --last 1m --limit 1 >/dev/null || exit 1

# No arguments: the image ships /etc/netrewind/netrewindd.yaml and the recorder
# reads it on its own, so the container behaves like an installed one and there
# is a single place to look. Override any of it with a flag, or mount your own
# file over it.
#
# Metrics are deliberately not on by default. This image requires --network host
# to see anything at all, and with it, binding metrics to 0.0.0.0 would publish
# a description of the watched network to that same network. Turn them on when
# you mean to:
#
#   docker run ... netrewind --metrics-addr 127.0.0.1:9464
#
ENTRYPOINT ["/usr/local/bin/netrewindd"]
