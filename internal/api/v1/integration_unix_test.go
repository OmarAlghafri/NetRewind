//go:build !windows

package v1

import "net"

const usesFilesystemIPCPath = true

func dialForTest(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}
