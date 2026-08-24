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
# The tracing mount is what lets eBPF attach its tracepoint. Without it the flow
# collector fails with a message saying exactly that, and the netlink collectors
# carry on.

# ---- build ----------------------------------------------------------------
FROM golang:1.24-alpine AS build

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

# Runs as root by default because reading netlink and loading eBPF need
# capabilities the container has to be granted anyway. The unprivileged user is
# here for `docker run --user 10001 netrewind serve`, which only reads a store.
VOLUME /var/lib/netrewind
EXPOSE 8464 9464

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
    CMD netrewind events --db /var/lib/netrewind/events.db --last 1m --limit 1 >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/netrewindd"]
CMD ["--db", "/var/lib/netrewind/events.db", \
     "--rules", "/etc/netrewind/rules", \
     "--metrics-addr", "0.0.0.0:9464"]
