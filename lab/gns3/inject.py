"""
inject.py
=========
Injects faults into the running GNS3 topology, over the device consoles.

These are the faults the network-namespace lab cannot produce, which is the
only reason this topology exists:

  gateway-moved     10.1.10.1 is taken off R1 and given to R3. The address the
                    segment uses as its way out is unchanged; the hardware
                    answering for it is not. Indistinguishable from a failover,
                    and indistinguishable from an attack, which is exactly why
                    the recorder reports the switch port rather than a verdict.

  vlan-misconfig    The recorder's access port is moved from VLAN 10 to VLAN 20.
                    A one-word change on one interface that cuts a host off
                    from its gateway while every link stays up and every light
                    stays green.

  trunk-pruned      VLAN 10 is removed from the trunk between the switches,
                    splitting one broadcast domain into two that cannot see
                    each other but both believe they are whole.

  core-link-down    The R1-R2 link is shut, forcing OSPF to reconverge around
                    it. The path changes underneath traffic that never stopped.

  access-port-down  The recorder's own switchport is shut. The simplest fault
                    there is, and the control for all the others.

Each scenario has an --undo, because a fault you cannot reverse is a lab you
can only run once.

Usage
    py lab/gns3/inject.py --list
    py lab/gns3/inject.py gateway-moved
    py lab/gns3/inject.py gateway-moved --undo
"""

from __future__ import annotations

import argparse
import socket
import sys
import time

from provision_lab import GNS3, PROJECT_PREFIX

# Each scenario is (device, commands-to-break, commands-to-restore).
SCENARIOS = {
    "gateway-moved": (
        None,  # touches two devices; handled specially
        None,
        None,
    ),
    "vlan-misconfig": (
        "SW1",
        ["interface FastEthernet1/2", "switchport access vlan 20"],
        ["interface FastEthernet1/2", "switchport access vlan 10"],
    ),
    "trunk-pruned": (
        "SW1",
        ["interface FastEthernet1/1", "switchport trunk allowed vlan 20"],
        ["interface FastEthernet1/1", "no switchport trunk allowed vlan"],
    ),
    "core-link-down": (
        "R1",
        ["interface FastEthernet0/0", "shutdown"],
        ["interface FastEthernet0/0", "no shutdown"],
    ),
    "access-port-down": (
        "SW1",
        ["interface FastEthernet1/2", "shutdown"],
        ["interface FastEthernet1/2", "no shutdown"],
    ),
}


def nodes_by_name(gns3: GNS3, pid: str) -> dict:
    return {n["name"]: n for n in gns3.get(f"/projects/{pid}/nodes")}


def project_id(gns3: GNS3) -> str:
    for proj in gns3.get("/projects"):
        if proj["name"].startswith(PROJECT_PREFIX):
            return proj["project_id"]
    sys.exit("no NetRewind project in GNS3. Run provision_lab.py first.")


def send(node: dict, lines: list[str]) -> None:
    """Push configuration into a device over its console.

    Consoles rather than SSH on purpose: the point of most of these scenarios is
    to break reachability, and a fault injector that needs the network to be
    working cannot inject the interesting faults.
    """

    host = node.get("console_host") or "127.0.0.1"
    if host in ("0.0.0.0", "::"):
        host = "127.0.0.1"
    port = node["console"]

    with socket.create_connection((host, port), timeout=15) as sock:
        sock.settimeout(3)

        def push(text: str) -> None:
            sock.sendall((text + "\r\n").encode())
            time.sleep(0.7)
            try:
                sock.recv(65535)
            except socket.timeout:
                pass

        push("")
        push("enable")
        push("NetRewindLab")
        push("terminal length 0")
        push("configure terminal")
        for line in lines:
            push(line)
        push("end")


def run(name: str, undo: bool) -> None:
    gns3 = GNS3()
    pid = project_id(gns3)
    nodes = nodes_by_name(gns3, pid)

    if name == "gateway-moved":
        # The address leaves R1 and appears on R3. Order matters: taking it off
        # first means the segment is never presented with two machines claiming
        # the gateway, which is a different fault with a different rule.
        if not undo:
            print("[*] removing 10.1.10.1 from R1")
            send(nodes["R1"], ["interface FastEthernet0/1.10", "ip address 10.1.10.3 255.255.255.0"])
            time.sleep(3)
            print("[*] giving 10.1.10.1 to R3")
            send(nodes["R3"], ["interface FastEthernet0/1.10", "ip address 10.1.10.1 255.255.255.0"])
        else:
            send(nodes["R3"], ["interface FastEthernet0/1.10", "ip address 10.1.10.2 255.255.255.0"])
            time.sleep(3)
            send(nodes["R1"], ["interface FastEthernet0/1.10", "ip address 10.1.10.1 255.255.255.0"])
            print("[*] gateway restored to R1")
        return

    device, break_cmds, fix_cmds = SCENARIOS[name]
    cmds = fix_cmds if undo else break_cmds
    if device not in nodes:
        sys.exit(f"{device} is not in the project. Is the topology built and started?")
    print(f"[*] {'restoring' if undo else 'injecting'} on {device}: {' / '.join(cmds)}")
    send(nodes[device], cmds)


def main() -> None:
    ap = argparse.ArgumentParser(description="Inject a fault into the NetRewind GNS3 lab.")
    ap.add_argument("scenario", nargs="?", help="which fault to inject")
    ap.add_argument("--undo", action="store_true", help="put it back")
    ap.add_argument("--list", action="store_true", help="list the scenarios")
    args = ap.parse_args()

    if args.list or not args.scenario:
        print("scenarios:")
        for name in SCENARIOS:
            print(f"  {name}")
        print("\nAfter injecting, ask the recorder on the NetRewind node console:")
        print("  netrewind timeline --last 15m")
        print("  netrewind incidents --last 1h")
        return

    if args.scenario not in SCENARIOS:
        sys.exit(f"unknown scenario {args.scenario!r}. Try --list.")

    try:
        run(args.scenario, args.undo)
    except (RuntimeError, OSError) as exc:
        sys.exit(f"error: {exc}")


if __name__ == "__main__":
    main()
