# NetRewind build.
#
# The collectors read netlink and (from M2) load eBPF programs, so `make run`
# and the lab targets only work on Linux. Everything else - build, vet, test -
# is expected to work from a Windows or macOS checkout too, which is why the
# non-Linux collector stubs exist.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
BUILD   := build

# CGO stays off: the SQLite driver is pure Go, so the result is one static
# binary with no runtime dependencies. That is what makes the appliance image
# trivial to build later.
export CGO_ENABLED = 0

.PHONY: all
all: fmt vet test build

.PHONY: build
build:
	go build -ldflags "$(LDFLAGS)" -o $(BUILD)/netrewindd ./cmd/netrewindd
	go build -ldflags "$(LDFLAGS)" -o $(BUILD)/netrewind  ./cmd/netrewind

.PHONY: linux
linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD)/netrewindd-linux-amd64 ./cmd/netrewindd
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD)/netrewind-linux-amd64  ./cmd/netrewind

.PHONY: test
test:
	go test ./...

.PHONY: vet
vet:
	go vet ./...
	GOOS=linux go vet ./...

.PHONY: fmt
fmt:
	gofmt -w ./cmd ./internal

# Reading netlink notifications needs CAP_NET_ADMIN; in the lab that means root.
.PHONY: run
run: build
	sudo $(BUILD)/netrewindd --db ./var/events.db --log-level debug

.PHONY: events
events:
	$(BUILD)/netrewind events --db ./var/events.db --last 15m

# M0 acceptance: take an interface down and back up, then look for it in the
# record. IFACE must be one this machine actually has and can afford to lose.
.PHONY: accept
accept: build
	@test -n "$(IFACE)" || (echo "usage: make accept IFACE=eth1"; exit 1)
	sudo ip link set $(IFACE) down
	sleep 2
	sudo ip link set $(IFACE) up
	sleep 1
	$(BUILD)/netrewind events --db ./var/events.db --last 2m --family link

# Escape hatch for developing against a VirtualBox shared folder: vboxsf is slow
# enough to make Go's build cache miserable, so copy the tree onto the VM's own
# disk and build there.
SYNC_DIR ?= $(HOME)/netrewind-build
.PHONY: sync
sync:
	rsync -a --delete --exclude build/ --exclude .git/ --exclude var/ ./ $(SYNC_DIR)/
	$(MAKE) -C $(SYNC_DIR) build

.PHONY: clean
clean:
	rm -rf $(BUILD) var

# The synthetic lab: a topology in network namespaces that faults can be
# injected into safely, because nothing in it touches the real network.
.PHONY: lab
lab: build
	sudo BUILD=$(BUILD) lab/inject.sh all

.PHONY: lab-up
lab-up:
	sudo lab/inject.sh setup

.PHONY: lab-down
lab-down:
	sudo lab/inject.sh teardown

# eBPF objects are compiled separately by clang, not by the Go toolchain, and
# the result is committed so the tree still builds on a machine without clang.
# Regenerate after editing anything under internal/collect/flow/bpf.
BPF_SRC := internal/collect/flow/bpf/flow.bpf.c
BPF_OBJ := internal/collect/flow/bpf/flow.bpf.o

.PHONY: bpf
bpf:
	clang -O2 -g -target bpf -D__TARGET_ARCH_x86 \
	    -Wall -Werror -c $(BPF_SRC) -o $(BPF_OBJ)
	@echo "built $(BPF_OBJ)"

.PHONY: kernel-check
kernel-check:
	sudo lab/check-kernel.sh
