//go:build windows

package process

import (
	"os"
	"syscall"
)

// getSysProcAttr returns the platform-specific syscall attributes for process creation
func getSysProcAttr() *syscall.SysProcAttr {
	// Windows doesn't support process groups in the same way as Unix
	return &syscall.SysProcAttr{}
}

// killProcess sends a signal to a process on Windows
func killProcess(pid int, signal syscall.Signal, killProcessGroup bool) error {
	// On Windows, we need to use os.FindProcess and Kill
	// Process groups are not supported in the same way
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

// reapChildren is a no-op on Windows
// Windows doesn't have the same zombie process concept as Unix
func (p *Process) reapChildren() {
	// No-op on Windows
}
