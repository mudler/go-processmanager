//go:build windows

package process

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// stillActive is the STILL_ACTIVE exit code that GetExitCodeProcess reports
// for a running process. A process can also exit with this code, so it is
// only used when the process handle cannot be waited on.
const stillActive = 259

// terminateWait is how long Stop waits for a terminated process to go away.
// TerminateProcess only starts the termination, and the process keeps its
// files open until it is gone.
const terminateWait = 5 * time.Second

// errNoCtrlBreak means that Stop cannot send a console break to the process.
var errNoCtrlBreak = errors.New("console break not available for this process")

// platformState holds the Windows handles of a process that this handle
// started. mu guards every field. It is separate from Process.mu because it is
// held across system calls.
type platformState struct {
	mu sync.Mutex

	// pid is the process ID that handle and job belong to.
	pid int
	// handle is a process handle held from start until the monitor sees the
	// process exit. While it is open, Windows does not reuse pid.
	handle windows.Handle
	// job is the kill-on-close job object, or 0.
	job windows.Handle
	// ctrlBreak is set when the process was created in its own process group
	// on the console of the calling program.
	ctrlBreak bool
	// resume is set when Run added CREATE_SUSPENDED and must resume the
	// process after it is in the job object.
	resume bool
}

// sysProcAttr returns the attributes for process creation: a copy of the
// caller's SysProcAttr, with the flags that the selected options need.
func (p *Process) sysProcAttr() (*syscall.SysProcAttr, error) {
	attr := &syscall.SysProcAttr{}
	if p.config.SysProcAttr != nil {
		c := *p.config.SysProcAttr
		attr = &c
	}
	callerFlags := attr.CreationFlags

	p.plat.mu.Lock()
	defer p.plat.mu.Unlock()
	p.plat.ctrlBreak = false
	p.plat.resume = false

	if p.config.ConsoleCtrlBreak {
		attr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
		// A console event reaches only processes on the console of the
		// sender. These flags give the child no console or another one.
		const otherConsole = windows.DETACHED_PROCESS | windows.CREATE_NEW_CONSOLE | windows.CREATE_NO_WINDOW
		p.plat.ctrlBreak = callerFlags&otherConsole == 0
	}
	if p.config.JobObject && callerFlags&windows.CREATE_SUSPENDED == 0 {
		// Keep the child from running, and so from starting children of its
		// own, until it is in the job.
		attr.CreationFlags |= windows.CREATE_SUSPENDED
		p.plat.resume = true
	}
	return attr, nil
}

// afterStart opens a handle to the new process, puts it in a job object if
// asked, and resumes it if Run created it suspended.
func (p *Process) afterStart(proc *os.Process) error {
	pid := proc.Pid
	access := uint32(windows.SYNCHRONIZE | windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.PROCESS_TERMINATE)
	if p.config.JobObject {
		access |= windows.PROCESS_SET_QUOTA
	}
	// proc keeps its own handle open, so pid cannot name another process yet.
	h, err := windows.OpenProcess(access, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("opening process %d: %w", pid, err)
	}

	var job windows.Handle
	if p.config.JobObject {
		job, err = newKillOnCloseJob()
		if err != nil {
			windows.CloseHandle(h)
			return err
		}
		if err := windows.AssignProcessToJobObject(job, h); err != nil {
			windows.CloseHandle(job)
			windows.CloseHandle(h)
			return fmt.Errorf("assigning process %d to job object: %w", pid, err)
		}
	}

	p.plat.mu.Lock()
	resume := p.plat.resume
	p.plat.mu.Unlock()
	if resume {
		if err := resumeProcess(pid); err != nil {
			if job != 0 {
				windows.CloseHandle(job)
			}
			windows.CloseHandle(h)
			return fmt.Errorf("resuming process %d: %w", pid, err)
		}
	}

	p.plat.mu.Lock()
	p.plat.pid = pid
	p.plat.handle = h
	p.plat.job = job
	p.plat.mu.Unlock()
	return nil
}

// afterExit closes the handles of the process. Closing the job object kills
// every process that is still in it.
func (p *Process) afterExit() {
	p.plat.mu.Lock()
	defer p.plat.mu.Unlock()
	if p.plat.job != 0 {
		windows.CloseHandle(p.plat.job)
		p.plat.job = 0
	}
	if p.plat.handle != 0 {
		windows.CloseHandle(p.plat.handle)
		p.plat.handle = 0
	}
	p.plat.ctrlBreak = false
}

// newKillOnCloseJob creates a job object that kills its processes when its
// last handle is closed. The handle is not inheritable, so a child cannot keep
// the job open.
func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("creating job object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("setting job object limits: %w", err)
	}
	return job, nil
}

// resumeProcess resumes the threads of a process that was created suspended.
// os.StartProcess does not return the thread handle, so the threads are found
// in a snapshot. A suspended new process has one thread.
func resumeProcess(pid int) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("thread snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	resumed := 0
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != uint32(pid) {
			continue
		}
		th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if err != nil {
			return fmt.Errorf("opening thread %d: %w", te.ThreadID, err)
		}
		_, err = windows.ResumeThread(th)
		windows.CloseHandle(th)
		if err != nil {
			return fmt.Errorf("resuming thread %d: %w", te.ThreadID, err)
		}
		resumed++
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return fmt.Errorf("walking thread snapshot: %w", err)
	}
	if resumed == 0 {
		return errors.New("no thread found")
	}
	return nil
}

// handleAlive reports whether the process behind h is running. A process
// handle is signaled when the process exits, so a zero-timeout wait is the
// authority. The exit code is only a fallback, because a process can exit
// with code STILL_ACTIVE (259).
func handleAlive(h windows.Handle) bool {
	ev, err := windows.WaitForSingleObject(h, 0)
	if err == nil {
		switch ev {
		case uint32(windows.WAIT_TIMEOUT):
			return true
		case windows.WAIT_OBJECT_0:
			return false
		}
	}
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

// processAlive reports whether pid names a running process.
//
// For the process that this handle started, it uses the handle that Run kept
// open, so the answer is about that process and not about a reused PID. For
// any other PID it opens the process: a PID that cannot be opened because
// access is denied belongs to a running process of another user, like EPERM
// on Unix.
func (p *Process) processAlive(pid int) bool {
	p.plat.mu.Lock()
	if p.plat.handle != 0 && p.plat.pid == pid {
		alive := handleAlive(p.plat.handle)
		p.plat.mu.Unlock()
		return alive
	}
	p.plat.mu.Unlock()
	return pidAlive(pid)
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == nil {
		defer windows.CloseHandle(h)
		return handleAlive(h)
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// ERROR_INVALID_PARAMETER: there is no process with this PID.
		return false
	}
	// Some processes allow a limited query but not SYNCHRONIZE.
	h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return true
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}

// stop stops the process on Windows. KillSignal is ignored: Windows has no
// signals. When WithConsoleCtrlBreak is set and the process can receive it,
// stop sends CTRL_BREAK_EVENT and waits up to GracefulTimeout. Then, or at
// once, it terminates the process, or the whole job when there is one.
func (p *Process) stop(pid int) error {
	if !p.processAlive(pid) {
		return fmt.Errorf("failed to stop process %d: %w", pid, os.ErrProcessDone)
	}

	if p.config.GracefulTimeout > 0 && p.sendCtrlBreak(pid) == nil {
		deadline := time.Now().Add(p.config.GracefulTimeout)
		for time.Now().Before(deadline) {
			if !p.IsAlive() {
				p.release()
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	if err := p.terminate(pid); err != nil {
		return fmt.Errorf("failed to terminate process %d: %w", pid, err)
	}
	p.release()
	return nil
}

// sendCtrlBreak sends CTRL_BREAK_EVENT to the process group of pid. It only
// does so for a process that this handle started in its own process group and
// that is still running: Windows can deliver an event for a group that does
// not exist to every process on the console, the caller included.
func (p *Process) sendCtrlBreak(pid int) error {
	p.plat.mu.Lock()
	defer p.plat.mu.Unlock()
	if !p.plat.ctrlBreak || p.plat.handle == 0 || p.plat.pid != pid {
		return errNoCtrlBreak
	}
	if !handleAlive(p.plat.handle) {
		return errNoCtrlBreak
	}
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}

// terminate kills the process, or every process in its job object, with exit
// code 1 (as os.Process.Kill does), and waits up to terminateWait for it to go
// away.
func (p *Process) terminate(pid int) error {
	p.plat.mu.Lock()
	defer p.plat.mu.Unlock()

	if p.plat.handle != 0 && p.plat.pid == pid {
		h := p.plat.handle
		var err error
		if p.plat.job != 0 {
			err = windows.TerminateJobObject(p.plat.job, 1)
		} else {
			err = windows.TerminateProcess(h, 1)
		}
		if err != nil && handleAlive(h) {
			return err
		}
		windows.WaitForSingleObject(h, uint32(terminateWait/time.Millisecond))
		return nil
	}

	// A process that this handle did not start, for example one found
	// through the pidfile of a state directory.
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil && handleAlive(h) {
		return err
	}
	windows.WaitForSingleObject(h, uint32(terminateWait/time.Millisecond))
	return nil
}

// reapChildren is a no-op on Windows, which has no zombie processes.
func (p *Process) reapChildren() {}
