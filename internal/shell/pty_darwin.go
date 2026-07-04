//go:build darwin && cgo

package shell

/*
#include <util.h>
*/
import "C"

import (
	"fmt"
	"os"
	"syscall"
)

func openPty() (masterFile, slaveFile *os.File, err error) {
	var master C.int
	var slave C.int
	rc, errno := C.openpty(&master, &slave, nil, nil, nil)
	if rc != 0 {
		if errno != nil {
			return nil, nil, fmt.Errorf("openpty: %w", errno)
		}
		return nil, nil, fmt.Errorf("openpty: return code %d", int(rc))
	}
	return os.NewFile(uintptr(master), "personal-mcp-server-pty-master"), os.NewFile(uintptr(slave), "personal-mcp-server-pty-slave"), nil
}

func shellSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
}
