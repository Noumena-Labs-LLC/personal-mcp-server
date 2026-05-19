//go:build linux && cgo

package shell

/*
#include <errno.h>
#include <pty.h>
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
	if rc := C.openpty(&master, &slave, nil, nil, nil); rc != 0 {
		return nil, nil, fmt.Errorf("openpty: %w", syscall.Errno(C.errno))
	}
	return os.NewFile(uintptr(master), "personal-mcp-server-pty-master"), os.NewFile(uintptr(slave), "personal-mcp-server-pty-slave"), nil
}

func shellSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
}
