package event

// EntityKind classifies what an EntityRef points at. These are the six things
// NetRewind knows how to talk about; every event is anchored to one of them.
type EntityKind string

const (
	// EntityHost is a logical machine on the network. It is deliberately not an
	// IP and not a MAC: both move, and both are forged. A host is whatever the
	// temporal identity table currently says binds together.
	EntityHost EntityKind = "host"
	// EntityIface is a network interface on the observer itself.
	EntityIface EntityKind = "iface"
	// EntityDevice is a switch, router or firewall discovered through LLDP/SNMP.
	EntityDevice EntityKind = "device"
	// EntitySubnet is an IP prefix.
	EntitySubnet EntityKind = "subnet"
	// EntityVLAN is a layer-2 broadcast domain.
	EntityVLAN EntityKind = "vlan"
	// EntityService is an (ip, port, proto) triple.
	EntityService EntityKind = "service"
	// EntityFlow is a single connection.
	EntityFlow EntityKind = "flow"
	// EntityObserver is a NetRewind node. Used by the system.* family, which
	// reports on the recorder rather than on the network.
	EntityObserver EntityKind = "observer"
)

// EntityRef points at the thing an event is about.
//
// ID is the stable identifier assigned by the identity resolver (host_id and
// friends) and is empty until resolution has run. Label is what a human should
// read. Attrs carries the raw observations the resolver works from - mac, ip,
// ifindex - so that an event stays interpretable even if resolution later
// turns out to have been wrong.
type EntityRef struct {
	Kind  EntityKind        `json:"kind"`
	ID    string            `json:"id,omitempty"`
	Label string            `json:"label"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

// Iface builds a reference to one of the observer's own interfaces.
func Iface(name string, ifindex int) EntityRef {
	return EntityRef{
		Kind:  EntityIface,
		Label: name,
		Attrs: map[string]string{"ifname": name, "ifindex": itoa(ifindex)},
	}
}

// Observer builds a reference to a NetRewind node itself.
func Observer(id string) EntityRef {
	return EntityRef{Kind: EntityObserver, ID: id, Label: id}
}

// Host builds a reference to a machine on the network. Either identifier may be
// empty; the label prefers the address, because that is what an operator knows
// a machine by when something breaks. ID stays empty until the identity
// resolver fills it in.
func Host(ip, mac string) EntityRef {
	attrs := make(map[string]string, 2)
	if ip != "" {
		attrs["ip"] = ip
	}
	if mac != "" {
		attrs["mac"] = mac
	}
	label := ip
	if label == "" {
		label = mac
	}
	return EntityRef{Kind: EntityHost, Label: label, Attrs: attrs}
}

// Subnet builds a reference to an IP prefix, or to "default" for the route that
// covers everything else.
func Subnet(prefix string) EntityRef {
	return EntityRef{
		Kind:  EntitySubnet,
		Label: prefix,
		Attrs: map[string]string{"prefix": prefix},
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
