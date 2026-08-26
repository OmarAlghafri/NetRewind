# The GNS3 lab

Everything on one segment can be proved against network namespaces, which is
what [`lab/inject.sh`](../inject.sh) does in seconds and without a hypervisor.
This topology exists for the four things namespaces cannot produce:

- **real VLANs and a dot1q trunk**, so a port moved to the wrong VLAN is a real
  fault rather than a simulated one
- **real OSPF**, so a path change is a routing protocol reconverging and not a
  script writing a route
- **a gateway address that moves between two physical routers**, which is the
  single most valuable event this recorder produces and the hardest to fake
- **Cisco IOS**, so the recorder is watching a network built the way networks
  are actually built

## What it builds

```
   R1 Fa0/0 --10.0.12.0/30-- Fa0/0 R2 Fa0/1 --10.0.23.0/30-- Fa0/0 R3
      | Fa0/1 (trunk)                                          Fa0/1 |
     SW1 Fa1/0                                              Fa1/0 SW2
      | Fa1/1 ------------- trunk ------------------------ Fa1/1 |
      |Fa1/2      |Fa1/3                                        |Fa1/2
   NetRewind      PC1                                           PC2
   10.1.10.50     10.1.10.100                                   10.1.20.100
   (VLAN 10)      (VLAN 10)                                     (VLAN 20)
```

R1 holds `10.1.10.1`, the gateway the recorder probes. R3 holds `10.1.10.2`, a
standby path. Both are OSPF area 0 with R2 between them.

The recorder sits on an access port in VLAN 10, which is where a recorder
actually goes: it sees that broadcast domain's ARP, its DHCP, and its own path
to the gateway. It does not see what it is not on, and the runbook says so.

## Requirements

- GNS3 2.2 with the **GNS3 VM running** — the recorder is a Docker node and
  Docker nodes run on the VM, not on the Windows side
- The `c3725` dynamips image, and templates for `c3725` and
  `Switch (c3725 NM-16ESW)`
- Python 3 on the host (`py` on Windows)

GNS3 server credentials are read from your local `gns3_server.ini`. Nothing
secret is stored in this repository.

## Getting the recorder onto the GNS3 VM

The Docker node runs an image built from
[`deploy/gns3/Dockerfile`](../../deploy/gns3/Dockerfile) — a lab variant that
starts the recorder behind a console shell, so you can open the node and ask it
what it saw. It is never released.

```bash
docker build -f deploy/gns3/Dockerfile -t netrewind-lab:latest .
docker save netrewind-lab:latest | gzip -1 > nrlab.tar.gz
scp nrlab.tar.gz gns3@<gns3-vm>:/tmp/
ssh gns3@<gns3-vm> 'gunzip -c /tmp/nrlab.tar.gz | docker load'
```

> **After rebuilding the image, re-provision.** GNS3 keeps a node's container
> across stop and start, so a node that already exists goes on running the old
> image however many times you restart it. `provision_lab.py` deletes and
> recreates the nodes, which is the only thing that picks up a new build.

## Running it

```bash
py lab/gns3/provision_lab.py --start     # build and power on (about 3 minutes)
py lab/gns3/inject.py --list             # what can be broken
py lab/gns3/inject.py gateway-moved      # break something
py lab/gns3/inject.py gateway-moved --undo
py lab/gns3/provision_lab.py --teardown  # empty the project
```

Then open the **NetRewind** node's console in GNS3 and ask it:

```bash
netrewind timeline --last 15m       # what happened, narrated
netrewind incidents --last 1h       # what it concluded, and on what evidence
netrewind events --family system    # whether it had any blind spots
```

## The scenarios

| | What it does | What the recorder should say |
|---|---|---|
| `gateway-moved` | Takes `10.1.10.1` off R1 and gives it to R3 | `l2.arp_binding_changed` with `is_gateway=true`, at **error** severity, and the gateway-hijack incident |
| `vlan-misconfig` | Moves the recorder's access port to VLAN 20 | Loses its gateway with every link still up: `l2.neighbor_failed`, `metric.anomaly`, reachability-lost |
| `trunk-pruned` | Removes VLAN 10 from the switch-to-switch trunk | One broadcast domain becomes two that each believe they are whole |
| `core-link-down` | Shuts R1 Fa0/0, forcing OSPF around it | Path change under traffic that never stopped; latency shifts on the probe |
| `access-port-down` | Shuts the recorder's own switchport | `link.down` — the control case |

`gateway-moved` is the one worth having a lab for. The address the segment uses
as its way out is unchanged; the hardware answering for it is not. That is
indistinguishable from a planned failover and indistinguishable from an attack,
which is why the incident tells you to go and look at the switch port rather
than handing you a verdict it cannot support.

## Known limits

**The recorder sees its own segment.** Not R1's routing table, not the other
VLAN. A route change appears here as reachability changing, not as `l3.route_*`,
because those events describe the recorder's own stack. This is a property of
where it is plugged in rather than a gap in the software, and moving it changes
what it can answer — see [the runbook](../../docs/runbook.md#where-to-put-it).

**Connection observation needs tracefs**, which a container does not get in its
own mount namespace. The node mounts it at start; if that is refused, the
recorder records `system.collector_down` rather than quietly missing every
`flow.*` event.
