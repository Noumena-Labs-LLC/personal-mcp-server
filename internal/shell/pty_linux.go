//go:build linux && cgo

package shell

/*
#include <pty.h>
*/
import "C"

import (
	"fmt"
	"os"
	"syscall"
)

func openPty() (*os.File, *os.File, error) {
	var master C.int
	var slave C.int
	if _, err := C.openpty(&master, &slave, nil, nil, nil); err != nil {
		return nil, nil, fmt.Errorf("openpty: %w", err)
	}
	return os.NewFile(uintptr(master), "personal-mcp-server-pty-master"), os.NewFile(uintptr(slave), "personal-mcp-server-pty-slave"), nil
}

func shellSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
}
