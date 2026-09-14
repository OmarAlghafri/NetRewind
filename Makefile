# NetRewind build.
#
# The collectors read netlink and (from M2) load eBPF programs, so `make run`
# and the lab targets only work on Linux. Everything else - build, vet, test -
# is expected to work from a Windows or macOS checkout too, which is why the
# non-Linux collector stubs exist.

# Tags carry a leading v by convention. Release assets must not: the updater
# looks for netrewind-<tag without the v>-linux-<arch>.tar.gz (TarballName in
# internal/update). Publishing netrewind-v0.9.0-... instead would leave every
# installed recorder unable to find its own update, and nothing would say so -
# the check would just report no matching asset. Strip it once, here, so only
# one form exists. override, so it applies to VERSION= on the command line too.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
override VERSION := $(patsubst v%,%,$(VERSION))
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
	# The lab's frame injector. Never shipped in a release: it sends DHCP
	# offers, which is the fault this project exists to catch.
	GOOS=linux go build -o $(BUILD)/nrinject ./lab/inject

.PHONY: linux
linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD)/netrewindd-linux-amd64 ./cmd/netrewindd
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD)/netrewind-linux-amd64  ./cmd/netrewind

.PHONY: test
test:
	go test ./...

# ---------------------------------------------------------------------------
# Desktop application (desktop/: Tauri + React)
# ---------------------------------------------------------------------------
#
# The desktop installer carries the recorder on Windows, where there is no
# package manager to install it from: desktop-resources stages the native
# recorder binaries, the rules and the sample configuration where
# desktop/src-tauri/tauri.conf.json's "resources" entry expects them, and
# the NSIS installer registers the service from there. On Linux the recorder
# comes from its own .deb/.rpm (deploy/build-deb.sh, build-rpm.sh), so the
# staged directory only carries a note saying so.
DESKTOP_RES := $(BUILD)/desktop-resources/recorder

.PHONY: desktop-resources
desktop-resources:
	rm -rf $(DESKTOP_RES) && mkdir -p $(DESKTOP_RES)
ifeq ($(OS),Windows_NT)
	go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(DESKTOP_RES)/netrewindd.exe ./cmd/netrewindd
	go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(DESKTOP_RES)/netrewind.exe  ./cmd/netrewind
	mkdir -p $(DESKTOP_RES)/rules && cp rules/*.yaml $(DESKTOP_RES)/rules/
	cp deploy/netrewindd.yaml $(DESKTOP_RES)/netrewindd.sample.yaml
else
	echo "The NetRewind recorder for Linux is installed from its own package (netrewind .deb/.rpm), not from the desktop bundle." > $(DESKTOP_RES)/README.txt
endif

# Type-check, unit-test and build the desktop application's installers into
# desktop/src-tauri/target/release/bundle/. Needs Node, Rust and the Tauri
# CLI (npm ci installs it); on Linux also the WebKitGTK development packages.
.PHONY: desktop
desktop: desktop-resources
	cd desktop && npm ci && npm test && npx tauri build

# The load tests measure sustained throughput, so they run alone. Under
# `go test ./...` the packages run concurrently and the number measures the
# contention between them rather than the store.
.PHONY: load
load:
	NETREWIND_LOAD_TEST=1 go test ./internal/store/ -run "Burst|Storm|Prune" -v -count=1
	# And the whole write path, not only the store. The writer makes a batch
	# durable and then offers every event of it to correlation, on one
	# goroutine; correlation is the slower half, so the store's number on its
	# own says nothing about whether the queue drains.
	NETREWIND_LOAD_TEST=1 go test ./cmd/netrewindd/ -run "ThePipelineKeepsUp" -v -count=1

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

#
# No __TARGET_ARCH define: the program hooks a tracepoint and reads its argument
# struct directly, so it uses none of the PT_REGS macros that would make it
# architecture-specific. BPF bytecode is portable, which is what lets one
# committed object serve amd64 and arm64 alike - and arm64 is not incidental,
# since a Raspberry Pi is the cheapest thing this is meant to run on.
# -target bpf makes clang forget which machine it is on, so it stops looking in
# the multiarch include directory and <linux/types.h> fails on the asm/types.h
# it includes. Pointing it back at the host's headers is the standard fix and
# does not make the output architecture-specific: the BPF program uses no
# machine types, which is what lets one committed object serve amd64 and arm64.
BPF_INCLUDE := /usr/include/$(shell uname -m)-linux-gnu

# BPF_STAMP records which source the committed object was built from.
#
# The object cannot be compared byte for byte across machines. Two clang
# versions produce different instructions from the same source - not merely
# different debug info - so a comparison against a freshly built object says
# only "you have a different clang", which is not the question. The question is
# whether somebody edited the C and forgot to regenerate, and a hash of the
# source answers exactly that, on any machine, forever.
BPF_STAMP := $(BPF_OBJ).source-sha256

.PHONY: bpf
bpf:
	clang -O2 -g -target bpf -Wall -Werror -I$(BPF_INCLUDE) -c $(BPF_SRC) -o $(BPF_OBJ)
	@sha256sum $(BPF_SRC) | cut -d' ' -f1 > $(BPF_STAMP)
	@echo "built $(BPF_OBJ) from $(BPF_SRC) ($$(cat $(BPF_STAMP) | cut -c1-16)...)"

# Verify the committed object was built from the committed source.
#
# Two separate things, both of which matter: the source still compiles, and the
# object beside it came from this version of it.
.PHONY: bpf-check
bpf-check:
	@test -f $(BPF_STAMP) || { echo "no $(BPF_STAMP); run 'make bpf'"; exit 1; }
	@clang -O2 -g -target bpf -Wall -Werror -I$(BPF_INCLUDE) -c $(BPF_SRC) -o /dev/null \
	  || { echo "$(BPF_SRC) does not compile"; exit 1; }
	@recorded=$$(cat $(BPF_STAMP)); \
	 actual=$$(sha256sum $(BPF_SRC) | cut -d' ' -f1); \
	 if [ "$$recorded" != "$$actual" ]; then \
	   echo "$(BPF_SRC) has changed since $(BPF_OBJ) was built."; \
	   echo "  object built from: $$recorded"; \
	   echo "  source is now:     $$actual"; \
	   echo "run 'make bpf' and commit both files."; \
	   exit 1; \
	 fi; \
	 echo "$(BPF_OBJ) matches $(BPF_SRC), and the source compiles"

# The bootable appliance. Linux and root: it needs loop devices and mount.
.PHONY: image
image: toolchain-check
	sudo deploy/appliance/build-image.sh --version $(VERSION) --out $(DIST)/netrewind-appliance.img

.PHONY: kernel-check
kernel-check:
	sudo lab/check-kernel.sh

# Metrics, for checking the exporter by hand.
.PHONY: metrics
metrics:
	curl -s http://127.0.0.1:9464/metrics

# ---------------------------------------------------------------------------
# Release
# ---------------------------------------------------------------------------
#
# Two architectures, because the cheapest hardware this is meant to run on is a
# Raspberry Pi and the most common is an amd64 mini PC. One committed eBPF
# object serves both: the program is architecture-neutral bytecode.
PLATFORMS := linux/amd64 linux/arm64
DIST      := dist

# ---------------------------------------------------------------------------
# Signing
# ---------------------------------------------------------------------------
#
# Checksums prove a download arrived as the server sent it. They do not prove
# who built it: anyone who can publish a release can publish checksums for it.
# A signature made with a key that never touches CI is what turns the update
# channel from "trust whoever holds the repository" into something an operator
# can reason about, and it is the difference that matters most on a recorder
# that installs its own replacements.
#
# The key lives outside the repository and outside CI. That is the whole point:
# a key CI can reach is a key a CI compromise can sign with.
SIGNING_KEY ?= $(HOME)/.netrewind/signing.key

.PHONY: signing-key
signing-key:
	@test ! -f $(SIGNING_KEY) || { \
	  echo "$(SIGNING_KEY) already exists. Refusing to overwrite it:"; \
	  echo "every release signed with the old key would stop verifying."; exit 1; }
	@mkdir -p $(dir $(SIGNING_KEY))
	@openssl genpkey -algorithm ed25519 -out $(SIGNING_KEY)
	@chmod 600 $(SIGNING_KEY)
	@echo "wrote $(SIGNING_KEY)"
	@echo
	@echo "Back this up somewhere offline now. Losing it means every recorder"
	@echo "configured with the matching public key stops accepting updates, and"
	@echo "there is no way to recover it. Never commit it and never put it in CI."
	@echo
	@$(MAKE) --no-print-directory signing-pubkey

# The public half, in the form netrewindd.yaml wants. The raw ed25519 key is
# the last 32 bytes of the DER encoding; crypto/ed25519 takes nothing else.
.PHONY: signing-pubkey
signing-pubkey:
	@test -f $(SIGNING_KEY) || { echo "no key at $(SIGNING_KEY); run 'make signing-key'"; exit 1; }
	@echo "put this in /etc/netrewind/netrewindd.yaml:"
	@echo
	@echo "update:"
	@echo "  public_key: \"$$(openssl pkey -in $(SIGNING_KEY) -pubout -outform DER | tail -c 32 | base64 | tr -d '\n')\""

# ---------------------------------------------------------------------------
# Toolchain
# ---------------------------------------------------------------------------
#
# go.mod names a toolchain rather than raising the go line, so the tree still
# builds on a distro that sets GOTOOLCHAIN=local with an older Go - see the
# comment there. The cost of that choice is that on exactly those machines
# nothing makes the newer toolchain happen: the build succeeds, and quietly
# links a standard library with known holes in it, including in html/template,
# which is what renders network-supplied strings into the web interface.
#
# Building day to day that way is fine and deliberate. Publishing that way is
# not: the binary goes to other people, and a recorder with update.apply on
# will install it without anybody looking. So the release path checks, and
# refuses, rather than warning into a scrollback nobody reads.
.PHONY: toolchain-check
toolchain-check:
	@want=$$(sed -n 's/^toolchain //p' go.mod); \
	 if [ -z "$$want" ]; then exit 0; fi; \
	 have=$$(go env GOVERSION); \
	 newest=$$(printf '%s\n%s\n' "$${want#go}" "$${have#go}" | sort -V | tail -1); \
	 if [ "go$$newest" != "$$have" ]; then \
	   echo "go.mod asks for $$want; this is $$have."; \
	   echo; \
	   echo "  Building with it is fine. Publishing with it is not: the older"; \
	   echo "  standard library has known holes, and one of them is in"; \
	   echo "  html/template, which renders strings this recorder reads off the"; \
	   echo "  watched network."; \
	   echo; \
	   echo "  GOTOOLCHAIN is $$(go env GOTOOLCHAIN). If it is local, the"; \
	   echo "  toolchain directive cannot take effect: install $$want, or build"; \
	   echo "  the release somewhere GOTOOLCHAIN can fetch it."; \
	   exit 1; \
	 fi; \
	 echo "toolchain: $$have, and go.mod asks for $$want"

.PHONY: release
release: toolchain-check test
	rm -rf $(DIST) && mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  name=netrewind-$(VERSION)-$$os-$$arch; \
	  echo "==> $$name"; \
	  stage=$(DIST)/$$name; \
	  mkdir -p $$stage/rules $$stage/docs $$stage/deploy/systemd; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w $(LDFLAGS)" \
	      -o $$stage/netrewindd ./cmd/netrewindd || exit 1; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w $(LDFLAGS)" \
	      -o $$stage/netrewind  ./cmd/netrewind  || exit 1; \
	  cp rules/*.yaml $$stage/rules/; \
	  cp docs/*.md $$stage/docs/; \
	  cp deploy/systemd/*.service $$stage/deploy/systemd/; \
	  cp deploy/netrewindd.yaml $$stage/deploy/; \
	  cp LICENSE NOTICE README.md $$stage/; \
	  cp deploy/install.sh $$stage/; \
	  tar -czf $(DIST)/$$name.tar.gz -C $(DIST) $$name; \
	  rm -rf $$stage; \
	done
	@cd $(DIST) && sha256sum *.tar.gz > SHA256SUMS && cat SHA256SUMS
	@$(MAKE) --no-print-directory sign
	@echo
	@ls -lh $(DIST)/*.tar.gz

# Sign the checksums, and then verify the signature with the same code the
# recorder uses to check it.
#
# Signing without verifying is how you publish a release that every updater
# refuses: openssl's ed25519 needs -rawin, and without it the signature is over
# a hash of the file rather than the file, which nothing accepts. Better to
# find that here than to find it from the field.
.PHONY: sign
sign:
	@test -f $(DIST)/SHA256SUMS || { echo "nothing to sign; run 'make release'"; exit 1; }
	@# One shell block on purpose: make runs each recipe line in its own shell,
	@# so an `exit 0` on the "no key" path would end that line and then carry
	@# straight on into the signing below.
	@if [ ! -f $(SIGNING_KEY) ]; then \
	  echo; \
	  echo "!! no signing key at $(SIGNING_KEY) - this release will be UNSIGNED."; \
	  echo "   Any recorder configured with update.public_key will refuse it."; \
	  echo "   Run 'make signing-key' to create one."; \
	else \
	  openssl pkeyutl -sign -inkey $(SIGNING_KEY) -rawin \
	      -in $(DIST)/SHA256SUMS -out $(DIST)/SHA256SUMS.sig || exit 1; \
	  go run ./internal/update/cmd/verifysig \
	      "$$(openssl pkey -in $(SIGNING_KEY) -pubout -outform DER | tail -c 32 | base64 | tr -d '\n')" \
	      $(DIST)/SHA256SUMS $(DIST)/SHA256SUMS.sig || exit 1; \
	  echo "signed $(DIST)/SHA256SUMS -> SHA256SUMS.sig"; \
	fi
