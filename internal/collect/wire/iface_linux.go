//go:build linux

package wire

import (
	"fmt"
	"net"
)

// interfaceIndex resolves a name to its kernel index, with an error that names
// what is actually available rather than leaving the operator to guess.
func interfaceIndex(name string) (int, error) {
	iface, err := net.InterfaceByName(name)
	if err == nil {
		return iface.Index, nil
	}
	all, listErr := net.Interfaces()
	if listErr != nil {
		return 0, fmt.Errorf("wire: no interface %q: %w", name, err)
	}
	names := make([]string, 0, len(all))
	for _, i := range all {
		names = append(names, i.Name)
	}
	return 0, fmt.Errorf("wire: no interface %q; this host has %v", name, names)
}
