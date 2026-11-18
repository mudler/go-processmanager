//go:build unix

package process

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// platformState holds no extra state on Unix.
type platformState struct{}

// sysProcAttr returns the attributes for process creation: a copy of the
// caller's SysProcAttr, with the child in a new process group so that Stop can
// signal the group.
func (p *Process) sysProcAttr() (*syscall.SysProcAttr, error) {
	attr := &syscall.SysProcAttr{}
	if p.config.SysProcAttr != nil {
		c := *p.config.SysProcAttr
		attr = &c
	}
	// setsid already makes the child the leader of a new process group, and
	// setpgid on a session leader fails with EPERM.
	if !attr.Setsid {
		attr.Setpgid = true
	}
	return attr, nil
}

// afterStart has nothing to do on Unix.
func (p *Process) afterStart(*os.Process) error { return nil }

// afterExit has nothing to do on Unix.
func (p *Process) afterExit() {}

// killProcess sends a signal to a process or process group
func killProcess(pid int, signal syscall.Signal, killProcessGroup bool) error {
	target := pid
	if killProcessGroup {
		// Use negative PID to target the entire process group on Unix
		target = -pid
	}
	return syscall.Kill(target, signal)
}

// processAlive reports whether pid names a running process, by sending it
// signal 0.
func (p *Process) processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if err.Error() == "os: process already finished" {
		return false
	}
	errno, ok := err.(syscall.Errno)
	if !ok {
		return false
	}
	switch errno {
	case syscall.ESRCH:
		return false
	case syscall.EPERM:
		return true
	}
	return false
}

// stop sends KillSignal (SIGTERM by default), waits up to GracefulTimeout, and
// then sends SIGKILL if the process is still alive.
func (p *Process) stop(pid int) error {
	// Determine which signal to send
	signal := syscall.SIGTERM
	if p.config.KillSignal != nil {
		signal = syscall.Signal(*p.config.KillSignal)
	}

	// Send the initial signal (SIGTERM or custom KillSignal)
	if err := killProcess(pid, signal, p.config.KillProcessGroup); err != nil {
		return fmt.Errorf("failed to send signal %v to process %d: %w", signal, pid, err)
	}

	// Wait for graceful timeout then send SIGKILL if the process is still alive
	if p.config.GracefulTimeout > 0 {
		// Wait for the graceful timeout period
		deadline := time.Now().Add(p.config.GracefulTimeout)
		for time.Now().Before(deadline) {
			if !p.IsAlive() {
				// Process has terminated, no need to force kill
				p.release()
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}

		// If still alive after grace period, send SIGKILL
		if p.IsAlive() {
			if err := killProcess(pid, syscall.SIGKILL, p.config.KillProcessGroup); err != nil {
				return fmt.Errorf("failed to send SIGKILL to process %d: %w", pid, err)
			}
		}
	}

	p.release()
	return nil
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
			if p.currentProc() == nil || !p.IsAlive() {
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
