# Development environment

NetRewind reads netlink and, from M2, loads eBPF programs into the kernel. That
means a real Linux kernel. The rest of the tree builds, vets and tests on
Windows and macOS - the collectors have non-Linux stubs precisely so the inner
loop does not require Linux at all.

There are two Linux environments, and they are for different things.

## The fast one: WSL2, for the collectors

Good enough to develop and prove every collector against, because the synthetic
lab builds its own topology out of network namespaces and veth pairs. Faults are
injected into that, so nothing depends on the surrounding network being
interesting.

Two things are worth knowing, both verified rather than assumed:

```bash
uname -r                          # 6.6.87.2-microsoft-standard-WSL2
ls -l /sys/kernel/btf/vmlinux     # present - so CO-RE eBPF will load here
```

The WSL2 kernel does ship BTF, which is what CO-RE eBPF needs. That was not a
given and it is worth rechecking after a WSL update.

A full distribution is not required. The binaries are built with
`CGO_ENABLED=0`, so a minimal rootfs is enough to run them:

```powershell
# ~4 MB download, ~50 MB on disk
curl -L -o alpine.tar.gz https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/x86_64/alpine-minirootfs-3.24.1-x86_64.tar.gz
wsl --import netrewind-lab C:\Users\<you>\wsl\netrewind-lab alpine.tar.gz
wsl -d netrewind-lab -u root -- apk add --no-cache iproute2
```

Then, from the distro, with the cross-compiled binaries in `build/`:

```bash
./lab/accept-m0.sh ./build     # interface state
./lab/inject.sh all            # the full fault suite
```

## The realistic one: a VM wired into GNS3

What WSL2 cannot give you is a network worth observing: its segment is NAT'd,
with no VLANs, no switches and no routing protocols. For anything that has to be
true of a real network - LLDP topology, VLAN behaviour, a routing daemon
reconverging - use a VM attached to the GNS3 lab.

VirtualBox rather than Hyper-V, because GNS3 already runs on it here and
enabling Hyper-V would disturb that.

- Ubuntu Server 24.04 LTS
- 4 vCPU, 8 GB RAM, 40 GB disk
- Adapter 1: NAT — internet, for packages
- Adapter 2: Host-only — the segment the GNS3 topology is wired into

Ubuntu 24.04 ships a kernel built with BTF, same as WSL2. Verify anyway:

```bash
ls -l /sys/kernel/btf/vmlinux
uname -r                          # expect 6.8 or newer
```

### Packages

```bash
sudo apt update
sudo apt install -y clang llvm libbpf-dev linux-tools-common \
    linux-tools-$(uname -r) build-essential git rsync make
```

Go from apt is too old. Install it from the tarball:

```bash
curl -LO https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc && source ~/.bashrc
go version
```

### Getting the source into the VM

The canonical checkout lives on the Windows host. Share it into the VM:

```
VirtualBox → Machine → Settings → Shared Folders
    Path:  C:\My project\NetRewind
    Name:  netrewind
    Auto-mount, permanent
```

Then in the VM:

```bash
sudo usermod -aG vboxsf $USER    # log out and back in
ls /media/sf_netrewind
```

`vboxsf` is slow enough that Go's build cache suffers on it. Two mitigations,
both already wired into the Makefile:

- Build output goes to `build/`, which is gitignored and stays out of the way.
- If it is still painful, `make sync` rsyncs the tree onto the VM's own disk and
  builds there. Edit on Windows, `make sync` in the VM.

### First build

```bash
cd /media/sf_netrewind
make all          # fmt, vet, test, build
```

### Wiring into the GNS3 lab

The topology is the one NetAtlas already builds from code: R1-R2-R3 as an OSPF
area 0 core, SW1 and SW2 as access switches carrying VLAN 10 and VLAN 20, with a
trunk closing the ring.

Attach the recorder to it by adding a GNS3 **Cloud** node bound to the host-only
adapter the VM's second interface sits on, and cabling that cloud to a port on
SW1. The recorder then sees VLAN 20 exactly as a host on that segment would.

The alternative - running NetRewind as a Docker node inside GNS3 - is lighter
and plugs straight into a topology link, but it needs a privileged container and
BTF in the GNS3 VM's own kernel. Worth trying at M2; the cloud node is the
reliable path in the meantime.

## Acceptance

Run the recorder against a local store:

```bash
sudo ./build/netrewindd --db ./var/events.db --log-level debug
```

In another shell, break something and then ask what happened:

```bash
sudo ip link set eth1 down
sleep 2
sudo ip link set eth1 up

./build/netrewind events --db ./var/events.db --last 5m --family link
```

Expected: a `link.down` and a `link.up`, correctly timestamped, with
`down_duration_ms` on the recovery. `make accept IFACE=eth1` does the same thing
in one command.

To check that the recorder is honest about its own blind spots, stop it, wait,
and start it again:

```bash
sudo ./build/netrewindd --db ./var/events.db --gap-threshold 2s
# Ctrl-C, wait five seconds, run it again
./build/netrewind events --db ./var/events.db --last 10m --kind system.gap
```

Expected: a `system.gap` naming exactly how long the record was blind.
