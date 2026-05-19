//go:build (!linux && !darwin) || !cgo

package shell

import (
	"errors"
	"os"
	"syscall"
)

func openPty() (*os.File, *os.File, error) {
	return nil, nil, errors.New("persistent_shell requires PTY support on Linux or macOS with cgo enabled")
}

func shellSysProcAttr() *syscall.SysProcAttr { return nil }
