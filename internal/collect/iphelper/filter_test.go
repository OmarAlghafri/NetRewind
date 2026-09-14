package iphelper

import "testing"

func TestIsObservable(t *testing.T) {
	const (
		ethernet   = 6
		loopback   = 24
		notPresent = 6
		up         = 1
	)
	cases := []struct {
		name        string
		description string
		ifType      uint32
		mtu         uint32
		operStatus  uint32
		want        bool
	}{
		{"physical adapter, up", "Realtek PCIe GbE Family Controller", ethernet, 1500, up, true},
		{"physical adapter, cable unplugged", "Realtek PCIe GbE Family Controller", ethernet, 1500, 2, true},
		{"virtual adapter", "VirtualBox Host-Only Ethernet Adapter", ethernet, 1500, up, true},
		{"wi-fi", "Intel(R) Dual Band Wireless-AC 3165", 71, 1500, up, true},
		{"WFP shim on a real adapter", "Realtek PCIe GbE Family Controller-WFP Native MAC Layer LightWeight Filter-0000", ethernet, 1500, up, false},
		{"Npcap shim", "Intel(R) Dual Band Wireless-AC 3165-Npcap Packet Driver (NPCAP)-0000", 71, 1500, up, false},
		{"QoS shim", "VirtualBox Host-Only Ethernet Adapter-QoS Packet Scheduler-0000", ethernet, 1500, up, false},
		{"never-present tunnel scaffolding", "Microsoft Teredo Tunneling Adapter", 131, 0, notPresent, false},
		{"tunnel that is present is kept", "Microsoft Teredo Tunneling Adapter", 131, 1280, up, true},
		{"loopback", "Software Loopback Interface 1", loopback, 1500, up, false},
		{"MTU 0 but present is kept (not scaffolding)", "Local Area Connection* 3", 71, 0, 2, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsObservable(c.description, c.ifType, c.mtu, c.operStatus); got != c.want {
				t.Errorf("IsObservable(%q, type=%d, mtu=%d, oper=%d) = %v, want %v",
					c.description, c.ifType, c.mtu, c.operStatus, got, c.want)
			}
		})
	}
}
