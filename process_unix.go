//go:build unix

package process

import (
	"syscall"
	"time"
)

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

// reapChildren waits for and reaps orphaned child processes
// This is called when the process manager is acting as a subreaper
func (p *Process) reapChildren() {
	for {
		// Wait for any child process with WNOHANG (non-blocking)
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		
		// If we successfully reaped a child, continue the loop immediately
		// to check for more children
		if err == nil && pid > 0 {
			continue
		}
		
		// No children to reap right now (ECHILD means no children, pid == 0 means no children ready)
		if err == syscall.ECHILD || pid == 0 {
			// Check if the main process is still alive
			// If not, we can exit the reaper
			if p.proc == nil || !p.IsAlive() {
				return
			}
			// Sleep briefly before checking again to prevent busy-waiting
			time.Sleep(100 * time.Millisecond)
			continue
		}
		
		// For other errors, exit the reaper
		if err != nil {
			return
		}
	}
}
