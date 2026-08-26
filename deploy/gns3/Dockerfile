# NetRewind as a node inside a GNS3 topology.
#
# Separate from deploy/Dockerfile on purpose. That image is the product: one
# process, no shell, ENTRYPOINT straight into the recorder - which is what you
# want from a container and exactly wrong for a lab node, where the whole point
# is to open the console and ask the recorder what it saw.
#
# So this variant starts the recorder in the background and gives the console a
# shell. It is a lab fixture and is never released.
#
#   docker build -f deploy/gns3/Dockerfile -t netrewind-lab:latest .
#
# GNS3 attaches interfaces itself, so there is no --network host here: the node
# sits on the topology's own segment and sees that segment's ARP, DHCP and
# broadcast traffic, which is the point of putting it on the access switch.

FROM golang:1.26.6-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=lab
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/netrewindd ./cmd/netrewindd && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/netrewind  ./cmd/netrewind

FROM alpine:3.22
RUN apk add --no-cache nftables iproute2 tcpdump busybox-extras

COPY --from=build /out/netrewindd /usr/local/bin/netrewindd
COPY --from=build /out/netrewind  /usr/local/bin/netrewind
COPY rules/ /etc/netrewind/rules/
COPY deploy/gns3/node-start.sh /usr/local/bin/node-start.sh
RUN chmod +x /usr/local/bin/node-start.sh && mkdir -p /var/lib/netrewind

# The console lands in a shell with the recorder already running behind it.
ENV NETREWIND_DB=/var/lib/netrewind/events.db
CMD ["/usr/local/bin/node-start.sh"]
