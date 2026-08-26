"""
provision_lab.py
================
Builds the NetRewind GNS3 lab from code, over the GNS3 v2 REST API.

The synthetic lab (lab/inject.sh) proves the recorder against network
namespaces, which is enough for everything that happens on one segment. This
topology exists for what namespaces cannot produce: real VLANs, a real dot1q
trunk, real OSPF reconvergence, and a gateway address that moves between two
physical routers. Those are the faults an operator actually meets, and a
recorder that has only ever seen veth pairs has not been tested against them.

Topology
    R1 Fa0/0 --10.0.12.0/30-- Fa0/0 R2 Fa0/1 --10.0.23.0/30-- Fa0/0 R3
       | Fa0/1 (trunk)                                          Fa0/1 |
      SW1 Fa1/0                                              Fa1/0 SW2
       | Fa1/1 ------------- trunk ------------------------ Fa1/1 |
       |Fa1/2      |Fa1/3                                        |Fa1/2
    NetRewind      PC1                                           PC2
    10.1.10.50     10.1.10.100                                   10.1.20.100
    (VLAN 10)      (VLAN 10)                                     (VLAN 20)

R1 holds 10.1.10.1, the gateway NetRewind probes. R3 holds 10.1.10.2, a standby
path. Moving .1 from R1 to R3 is the scenario worth having a lab for.

Usage
    py lab/gns3/provision_lab.py            # build (wipes and rebuilds)
    py lab/gns3/provision_lab.py --start    # build, then power everything on
    py lab/gns3/provision_lab.py --teardown # delete the nodes and links

Credentials for the GNS3 server are read from the local GNS3 configuration
file, so nothing secret is stored in this repository. The recorder itself runs
as a Docker node on the GNS3 VM, built from deploy/gns3/Dockerfile.
"""

from __future__ import annotations

import argparse
import base64
import configparser
import json
import os
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
CONFIGS = HERE / "configs"

PROJECT_NAME = "NetRewind Causal Lab"
PROJECT_PREFIX = "NetRewind"

# Dynamips templates registered in this GNS3 installation. Both are the c3725
# image; the switch is the same router with an NM-16ESW module, which is how a
# lab gets real switchports without a real switch.
TPL_ROUTER = "954d4a9f-de03-4e3e-b61b-ec48946640c2"
TPL_SWITCH = "c919bf3e-6fcf-4cbc-9e95-148fc90ed8da"

# The recorder's own node. Created on first run if it is not registered yet.
NR_IMAGE = "netrewind-lab:latest"
NR_TEMPLATE_NAME = "NetRewind recorder"

NR_ADDRESS = "10.1.10.50/24"
NR_GATEWAY = "10.1.10.1"


def gns3_server_ini() -> Path:
    return Path(os.environ.get("APPDATA", "")) / "GNS3" / "2.2" / "gns3_server.ini"


class GNS3:
    """Thin GNS3 v2 REST client."""

    def __init__(self) -> None:
        ini = gns3_server_ini()
        if not ini.exists():
            sys.exit(f"GNS3 server configuration not found at {ini}")
        cfg = configparser.ConfigParser()
        cfg.read(ini)
        srv = cfg["Server"]
        self.base = (
            f"{srv.get('protocol', 'http')}://{srv.get('host', 'localhost')}"
            f":{srv.get('port', '3080')}/v2"
        )
        token = f"{srv.get('user', '')}:{srv.get('password', '')}".encode()
        self.auth = base64.b64encode(token).decode()

    def _request(self, method: str, path: str, body=None, raw: bool = False):
        data = None
        if raw:
            data = body.encode() if isinstance(body, str) else body
        elif body is not None:
            data = json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Authorization", "Basic " + self.auth)
        if data is not None:
            req.add_header(
                "Content-Type",
                "application/octet-stream" if raw else "application/json",
            )
        try:
            with urllib.request.urlopen(req, timeout=90) as resp:
                payload = resp.read().decode()
                return resp.status, (payload if raw else (json.loads(payload) if payload else None))
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode()[:300]
            raise RuntimeError(f"{method} {path} -> HTTP {exc.code}: {detail}") from None
        except urllib.error.URLError as exc:
            raise RuntimeError(
                f"cannot reach the GNS3 server at {self.base}: {exc.reason}. Is GNS3 running?"
            ) from None

    def get(self, path):
        return self._request("GET", path)[1]

    def post(self, path, body=None):
        return self._request("POST", path, body)[1]

    def put(self, path, body):
        return self._request("PUT", path, body)[1]

    def delete(self, path):
        return self._request("DELETE", path)[1]

    def post_raw(self, path, text):
        return self._request("POST", path, text, raw=True)[0]


# --------------------------------------------------------------------------
# Project
# --------------------------------------------------------------------------


def nodes_by_name(gns3: GNS3, pid: str) -> dict:
    return {n["name"]: n for n in gns3.get(f"/projects/{pid}/nodes")}


def find_or_create_project(gns3: GNS3) -> str:
    for proj in gns3.get("/projects"):
        if proj["name"].startswith(PROJECT_PREFIX):
            if proj["status"] != "opened":
                gns3.post(f"/projects/{proj['project_id']}/open")
            return proj["project_id"]
    proj = gns3.post("/projects", {"name": PROJECT_NAME})
    print(f"[*] created GNS3 project {PROJECT_NAME!r}")
    return proj["project_id"]


def wipe(gns3: GNS3, pid: str) -> None:
    """Stop and remove everything, so a rebuild is identical rather than layered."""
    for node in gns3.get(f"/projects/{pid}/nodes"):
        if node.get("status") == "started":
            try:
                gns3.post(f"/projects/{pid}/nodes/{node['node_id']}/stop")
            except RuntimeError:
                pass
    for link_obj in gns3.get(f"/projects/{pid}/links"):
        gns3.delete(f"/projects/{pid}/links/{link_obj['link_id']}")
    for node in gns3.get(f"/projects/{pid}/nodes"):
        gns3.delete(f"/projects/{pid}/nodes/{node['node_id']}")


# --------------------------------------------------------------------------
# Templates and nodes
# --------------------------------------------------------------------------


def ensure_recorder_template(gns3: GNS3) -> str:
    """Register the recorder as a Docker template, once.

    The image has to be loaded on the GNS3 VM already; see lab/gns3/README.md.
    Two adapters: eth0 onto the segment it watches, and a spare, because a
    recorder with nowhere to put a second interface cannot be moved to a mirror
    port later without rebuilding it.
    """
    for tpl in gns3.get("/templates"):
        if tpl.get("name") == NR_TEMPLATE_NAME:
            return tpl["template_id"]

    tpl = gns3.post(
        "/templates",
        {
            "name": NR_TEMPLATE_NAME,
            "template_type": "docker",
            "compute_id": "vm",
            "image": NR_IMAGE,
            "adapters": 2,
            "console_type": "telnet",
            "start_command": "",
            "environment": f"NETREWIND_ADDR={NR_ADDRESS}\nNETREWIND_GATEWAY={NR_GATEWAY}",
            "category": "guest",
            "symbol": ":/symbols/docker_guest.svg",
        },
    )
    print(f"[*] registered Docker template {NR_TEMPLATE_NAME!r} for {NR_IMAGE}")
    return tpl["template_id"]


def add_from_template(gns3: GNS3, pid: str, template_id: str, name: str, x: int, y: int) -> dict:
    node = gns3.post(f"/projects/{pid}/templates/{template_id}", {"x": x, "y": y})
    gns3.put(f"/projects/{pid}/nodes/{node['node_id']}", {"name": name})
    node["name"] = name
    return node


def push_startup_config(gns3: GNS3, pid: str, node: dict, cfg_file: Path) -> None:
    dynamips_id = node["properties"]["dynamips_id"]
    text = cfg_file.read_text(encoding="utf-8")
    path = (
        f"/projects/{pid}/nodes/{node['node_id']}"
        f"/files/configs/i{dynamips_id}_startup-config.cfg"
    )
    status = gns3.post_raw(path, text)
    if status not in (200, 201, 204):
        raise RuntimeError(f"failed to push config to {node['name']}: HTTP {status}")


def console(node: dict, lines: list[str], settle: float = 0.8) -> str:
    """Type into a device's console and return what it said back.

    Consoles rather than SSH throughout: half of what this lab does is break
    reachability, and a tool that needs the network working cannot configure a
    network that is not.
    """
    import socket

    host = node.get("console_host") or "127.0.0.1"
    if host in ("0.0.0.0", "::"):
        host = "127.0.0.1"

    transcript = []
    with socket.create_connection((host, node["console"]), timeout=20) as sock:
        sock.settimeout(4)
        for text in lines:
            sock.sendall((text + "\r\n").encode())
            time.sleep(settle)
            try:
                transcript.append(sock.recv(200000).decode(errors="replace"))
            except OSError:
                pass
    return "".join(transcript)


def create_vlans(nodes: dict) -> None:
    """Create the VLANs in the switch's VLAN database.

    This cannot be done from the startup configuration, which is the trap in
    this topology. On an NM-16ESW the VLAN database is a separate exec mode and
    lives in vlan.dat, not in the configuration file - so a switch can boot with
    `switchport access vlan 10` on an interface while VLAN 10 does not exist.
    The port then belongs to nothing, every light stays green, and the host
    behind it is off the network for no visible reason.
    """
    for name in ("SW1", "SW2"):
        # And the flash it goes on has to be formatted first. A dynamips node
        # boots with flash present but with no filesystem on it, so the write
        # fails with "Invalid DOS media or no media in slot" - which reads like
        # a hardware fault and is really an empty disk.
        # "abort" first, unconditionally. A failed apply leaves the session in
        # vlan-database mode, where every subsequent command is rejected as
        # invalid input - and because a console session swallows the rejection,
        # the next run reports success while doing nothing at all. That is worth
        # more care than usual here: this whole lab exists to study faults that
        # look like nothing is wrong.
        console(nodes[name], ["abort", "", "end", ""], settle=1.0)
        console(
            nodes[name],
            ["", "enable", "NetRewindLab", "terminal length 0", "format flash:", "", "", ""],
            settle=2.5,
        )
        console(
            nodes[name],
            [
                "",
                "vlan database",
                "vlan 10 name USERS",
                "vlan 20 name SERVERS",
                "apply",
                "exit",
            ],
            settle=1.2,
        )
        # Confirm rather than assume: ask the switch what VLANs it has, and say
        # so plainly if the answer is not the one that was asked for.
        seen = console(nodes[name], ["", "show vlan-switch brief"], settle=1.8)
        if "USERS" not in seen or "SERVERS" not in seen:
            raise RuntimeError(
                f"{name} did not take VLANs 10 and 20. Check its flash: "
                f"a dynamips node boots with an unformatted disk and the write fails silently."
            )
        print(f"[*] formatted flash and created VLANs 10 and 20 on {name}")


def link(gns3: GNS3, pid: str, a: dict, an: int, ap: int, b: dict, bn: int, bp: int) -> None:
    gns3.post(
        f"/projects/{pid}/links",
        {
            "nodes": [
                {"node_id": a["node_id"], "adapter_number": an, "port_number": ap},
                {"node_id": b["node_id"], "adapter_number": bn, "port_number": bp},
            ]
        },
    )


# --------------------------------------------------------------------------
# Build
# --------------------------------------------------------------------------


def build(start: bool) -> None:
    gns3 = GNS3()
    pid = find_or_create_project(gns3)
    print(f"[*] project {pid}")

    wipe(gns3, pid)
    print("[*] wiped previous topology")

    nr_template = ensure_recorder_template(gns3)

    # Routed core across the top, access layer below it, hosts at the bottom.
    r1 = add_from_template(gns3, pid, TPL_ROUTER, "R1", -300, -250)
    r2 = add_from_template(gns3, pid, TPL_ROUTER, "R2", 0, -350)
    r3 = add_from_template(gns3, pid, TPL_ROUTER, "R3", 300, -250)
    sw1 = add_from_template(gns3, pid, TPL_SWITCH, "SW1", -300, -50)
    sw2 = add_from_template(gns3, pid, TPL_SWITCH, "SW2", 300, -50)
    print("[*] created R1 R2 R3 SW1 SW2")

    # The switches need somewhere to keep vlan.dat, and the template ships with
    # no PCMCIA disk at all. Without one, creating a VLAN fails with "error
    # squeezing flash - (No device available)" and the switch comes up with
    # `switchport access vlan 10` on a port whose VLAN does not exist. The port
    # then belongs to nothing while every light stays green, which is a very
    # good fault to be able to reproduce and a very bad one to have by accident.
    for sw in (sw1, sw2):
        gns3.put(f"/projects/{pid}/nodes/{sw['node_id']}", {"properties": {"disk0": 16}})
    print("[*] gave both switches a 16 MB flash for their VLAN database")

    for node, name in ((r1, "R1"), (r2, "R2"), (r3, "R3"), (sw1, "SW1"), (sw2, "SW2")):
        push_startup_config(gns3, pid, node, CONFIGS / f"{name}.cfg")
    print("[*] pushed startup configuration to all five")

    nr = add_from_template(gns3, pid, nr_template, "NetRewind", -420, 120)
    pc1 = add_from_template(gns3, pid, nr_template, "PC1", -180, 120)
    pc2 = add_from_template(gns3, pid, nr_template, "PC2", 300, 120)
    print("[*] created NetRewind, PC1, PC2")

    # PC1 and PC2 are the same image with the recorder left off: they exist to
    # put ordinary traffic and ARP on the segment, and reusing one image keeps
    # the lab to a single artefact.
    for node, addr, gw in (
        (pc1, "10.1.10.100/24", "10.1.10.1"),
        (pc2, "10.1.20.100/24", "10.1.20.1"),
    ):
        gns3.put(
            f"/projects/{pid}/nodes/{node['node_id']}",
            {
                "properties": {
                    "environment": f"NETREWIND_ADDR={addr}\nNETREWIND_GATEWAY={gw}\nNETREWIND_RECORD=off"
                }
            },
        )

    # Core: point-to-point links between the routers.
    link(gns3, pid, r1, 0, 0, r2, 0, 0)   # R1 Fa0/0 <-> R2 Fa0/0
    link(gns3, pid, r2, 0, 1, r3, 0, 0)   # R2 Fa0/1 <-> R3 Fa0/0

    # Access: each router trunks into its switch, and the switches trunk to
    # each other, so both VLANs span the whole topology.
    link(gns3, pid, r1, 0, 1, sw1, 1, 0)  # R1 Fa0/1 <-> SW1 Fa1/0
    link(gns3, pid, r3, 0, 1, sw2, 1, 0)  # R3 Fa0/1 <-> SW2 Fa1/0
    link(gns3, pid, sw1, 1, 1, sw2, 1, 1) # SW1 Fa1/1 <-> SW2 Fa1/1

    # The recorder and the hosts hang off access ports.
    link(gns3, pid, nr, 0, 0, sw1, 1, 2)   # NetRewind eth0 <-> SW1 Fa1/2
    link(gns3, pid, pc1, 0, 0, sw1, 1, 3)  # PC1 eth0 <-> SW1 Fa1/3
    link(gns3, pid, pc2, 0, 0, sw2, 1, 2)  # PC2 eth0 <-> SW2 Fa1/2
    print("[*] wired 8 links")

    if not start:
        print("\nBuilt. Start it with --start, or from the GNS3 GUI.")
        return

    print("[*] starting nodes")
    gns3.post(f"/projects/{pid}/nodes/start")

    # IOS has to be up before its console will take commands.
    print("[*] waiting for IOS to boot (about 2 minutes)")
    time.sleep(120)

    live = nodes_by_name(gns3, pid)
    create_vlans(live)

    # OSPF then needs a moment, and the access ports need to leave spanning-tree
    # listening. There is no point returning before the topology is a network.
    print("[*] waiting for OSPF to converge")
    time.sleep(60)

    print(
        "\nRunning. Open the NetRewind node's console and ask it what it saw:\n"
        "\n    netrewind timeline --last 15m"
        "\n    netrewind incidents --last 1h\n"
        "\nThen inject a fault:  py lab/gns3/inject.py --list\n"
    )


def teardown() -> None:
    gns3 = GNS3()
    for proj in gns3.get("/projects"):
        if proj["name"].startswith(PROJECT_PREFIX):
            if proj["status"] != "opened":
                gns3.post(f"/projects/{proj['project_id']}/open")
            wipe(gns3, proj["project_id"])
            print(f"[*] emptied {proj['name']!r}")
            return
    print("no NetRewind project found")


def main() -> None:
    ap = argparse.ArgumentParser(description="Build the NetRewind GNS3 lab.")
    ap.add_argument("--start", action="store_true", help="power the topology on after building")
    ap.add_argument("--teardown", action="store_true", help="remove the nodes and links")
    args = ap.parse_args()

    try:
        if args.teardown:
            teardown()
        else:
            build(args.start)
    except RuntimeError as exc:
        sys.exit(f"error: {exc}")


if __name__ == "__main__":
    main()
