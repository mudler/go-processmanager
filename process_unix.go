//go:build unix

package process

import "syscall"

// getSysProcAttr returns the platform-specific syscall attributes for process creation
func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true, // Create new process group on Unix
	}
}

// killProcess sends a signal to a process or process group
func killProcess(pid int, signal syscall.Signal, killProcessGroup bool) error {
	target := pid
	if killProcessGroup {
		// Use negative PID to target the entire process group on Unix
		target = -pid
	}
	return syscall.Kill(target, signal)
}
