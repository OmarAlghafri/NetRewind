# Development environment

NetRewind reads netlink and, from M2, loads eBPF programs into the kernel. That
means a real Linux kernel: WSL2 is not enough, because its network is a NAT'd
virtual segment with no VLANs, no ARP worth watching and no switch behind it.
Compiling and testing work anywhere; observing does not.

The rest of the tree builds, vets and tests on Windows and macOS - the
collectors have non-Linux stubs precisely so that the inner loop does not
require the VM.

## 1. The virtual machine

VirtualBox is used rather than Hyper-V because GNS3 already runs on it here, and
enabling Hyper-V would disturb that.

- Ubuntu Server 24.04 LTS
- 4 vCPU, 8 GB RAM, 40 GB disk
- Adapter 1: NAT — internet, for packages
- Adapter 2: Host-only — the segment the GNS3 topology is wired into

Ubuntu 24.04 is chosen for one specific reason: it ships a kernel built with
BTF, which CO-RE eBPF requires. Verify it before writing any code:

```bash
ls -l /sys/kernel/btf/vmlinux     # must exist
uname -r                          # expect 6.8 or newer
```

If that file is missing, nothing in M2 onwards will load, and it is far cheaper
to discover that now.

## 2. Packages

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

## 3. Getting the source into the VM

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

## 4. First build

```bash
cd /media/sf_netrewind
make all          # fmt, vet, test, build
```

## 5. Wiring into the GNS3 lab

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

## 6. M0 acceptance

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
