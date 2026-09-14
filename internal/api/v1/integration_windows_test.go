//go:build windows

package v1

import (
	"net"

	winio "github.com/Microsoft/go-winio"
)

const usesFilesystemIPCPath = false

func dialForTest(path string) (net.Conn, error) {
	return winio.DialPipe(path, nil)
}
