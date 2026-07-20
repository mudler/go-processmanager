package process

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type Process struct {
	config Config
	proc   *os.Process

	// PID holds the process ID last written to the pidfile.
	//
	// Deprecated: read it through CurrentPID instead. The monitor goroutine
	// clears this field when the process exits, so a direct field read races
	// with the library's own bookkeeping. It is kept for backwards
	// compatibility and will be unexported in a future release.
	PID string

	// mu guards PID, proc and started. Writes come from both the caller (Run,
	// Stop) and the monitor goroutine (release, on the Wait-error path), so
	// every access has to be synchronized. It is never held across a syscall or
	// a file operation, so no path holding it can re-enter it.
	mu sync.RWMutex

	// started records that this handle has claimed its single run. It is what
	// makes a Process single-use: once set it is only ever cleared if the start
	// itself failed, so done is closed by at most one monitor goroutine over
	// the lifetime of the handle.
	started bool

	done chan struct{}
}

// ErrProcessAlreadyRun is returned by Run when the handle has already started a
// process. A Process is single-use: it owns exactly one done channel and one
// monitor goroutine, and neither is recreated, so restarting through the same
// handle is not supported. Construct a new Process to start another one.
//
// The message keeps the historical "command already started" prefix so callers
// matching on that text keep working.
var ErrProcessAlreadyRun = errors.New("command already started: a Process handle is single-use, construct a new one to start another process")

// New builds up a new process with options
func New(p ...Option) *Process {
	c := DefaultConfig()
	c.Apply(p...)

	return &Process{
		config: *c,
		done:   make(chan struct{}),
	}
}

// Done returns a channel that is closed once the process has exited
// (whether cleanly or because Wait returned an error). It is safe to
// receive from before Run is called; the channel will not be closed
// until a process is actually started and exits.
//
// After Done is closed, ExitCode reports the exit status. If the
// process exited because Wait returned an error, the "error" file
// in StateDir holds the message and ExitCode will return an error.
func (p *Process) Done() <-chan struct{} {
	return p.done
}

func (p *Process) path(pa string) string {
	return filepath.Join(p.config.StateDir, pa)
}

// StateDir returns the process state directory
func (p *Process) StateDir() string {
	return p.config.StateDir
}

// StdoutPath returns the file where the stdout is appended to
func (p *Process) StdoutPath() string {
	return p.path("stdout")
}

// StderrPath returns the file where the stderr of the process is appended to
func (p *Process) StderrPath() string {
	return p.path("stderr")
}

// CurrentPID returns the process ID currently tracked by this handle, or an
// empty string if no process has been started or the process has exited.
func (p *Process) CurrentPID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PID
}

func (p *Process) setPID(pid string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.PID = pid
}

// currentProc returns the started process, or nil. Callers use the returned
// value outside the lock: os.Process is safe for concurrent use, and blocking
// on Wait while holding mu would deadlock every other accessor.
func (p *Process) currentProc() *os.Process {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.proc
}

func (p *Process) setProc(proc *os.Process) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.proc = proc
}

// claimRun reserves this handle's single run. Checking and marking under one
// lock is what makes concurrent Run calls safe: without it every caller can
// observe an unstarted handle, start its own process and launch its own monitor
// goroutine, and the second monitor to finish panics closing an already-closed
// done channel.
func (p *Process) claimRun() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return ErrProcessAlreadyRun
	}
	p.started = true
	return nil
}

// releaseClaim gives the claim back after a start that never produced a
// process, so that a caller can retry a Run that failed on, say, a missing
// binary. No monitor goroutine exists on that path.
func (p *Process) releaseClaim() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started = false
}

// readPID returns the contents of the pidfile. It deliberately does not cache
// the result on the Process: it is called from the caller's goroutine (Run,
// Stop, IsAlive) and from the monitor's reaper goroutine at the same time, and
// caching turned every one of those call sites into an unsynchronized write.
func (p *Process) readPID() (string, error) {
	b, err := os.ReadFile(
		p.path("pid"),
	)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (p *Process) writePID(proc *os.Process) error {
	pid := fmt.Sprint(proc.Pid)
	p.setPID(pid)
	return os.WriteFile(
		p.path("pid"),
		[]byte(pid),
		os.ModePerm,
	)
}

// Exists reports whether the named file or directory exists.
func exists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// Run starts the process and returns any error.
//
// A Process handle is single-use. Once Run has started a process, every later
// Run on the same handle returns ErrProcessAlreadyRun, whether or not that
// process has since exited or been stopped: the done channel and the monitor
// goroutine are created once and are not recreated. To start another process,
// build a new handle with New. A Run that fails before starting anything can be
// retried.
func (p *Process) Run() error {

	if err := p.claimRun(); err != nil {
		return err
	}

	// From here on every failure has to hand the claim back, otherwise a start
	// that never happened would burn the handle's single run.
	if pid, _ := p.readPID(); pid != "" && p.IsAlive() {
		p.releaseClaim()
		return errors.New("command already started")
	}

	if !exists(p.config.StateDir) {
		err := os.MkdirAll(p.config.StateDir, os.ModePerm)
		if err != nil {
			p.releaseClaim()
			return err
		}
	}

	// Set the current process as a subreaper to manage orphaned child processes
	// This ensures that when child processes terminate, their zombie processes
	// are reparented to us instead of init, allowing proper cleanup
	if err := SetSubreaper(); err != nil {
		// Non-fatal error - log and continue
		// This will fail on non-Linux systems but that's expected
	}

	wd := p.config.WorkDir
	if wd == "" {
		var err error
		wd, err = os.Getwd()
		if err != nil {
			p.releaseClaim()
			return err
		}
	}

	proc := &os.ProcAttr{
		Dir: wd,
		Env: p.config.Environment,
		Files: []*os.File{
			p.config.Stdin,
			NewLog(p.StdoutPath()),
			NewLog(p.StderrPath()),
		},
		Sys: getSysProcAttr(),
	}
	args := append([]string{p.config.Name}, p.config.Args...)
	process, err := os.StartProcess(p.config.Name, args, proc)
	if err != nil {
		p.releaseClaim()
		return err
	}

	p.setProc(process)
	p.writePID(process)
	go p.monitor()
	return nil
}

// IsAlive checks if the process is running or not
func (p *Process) IsAlive() bool {
	pidStr, err := p.readPID()
	if err != nil {
		return false
	}
	pid, err := strconv.ParseInt(pidStr, 10, 64)
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(int(pid))
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

// Stop stops the running process by sending KillSignal to the PID annotated in the pidfile
func (p *Process) Stop() error {
	pid, err := p.readPID()
	if err != nil {
		return fmt.Errorf("failed to read PID: %w", err)
	}
	if pid == "" {
		return errors.New("stop failed: PID is empty")
	}
	
	// convert pid string to int
	pidInt, err := strconv.ParseInt(pid, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse PID %q: %w", pid, err)
	}

	// Determine which signal to send
	signal := syscall.SIGTERM
	if p.config.KillSignal != nil {
		signal = syscall.Signal(*p.config.KillSignal)
	}

	// Send the initial signal (SIGTERM or custom KillSignal)
	if err := killProcess(int(pidInt), signal, p.config.KillProcessGroup); err != nil {
		return fmt.Errorf("failed to send signal %v to process %d: %w", signal, pidInt, err)
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
			if err := killProcess(int(pidInt), syscall.SIGKILL, p.config.KillProcessGroup); err != nil {
				return fmt.Errorf("failed to send SIGKILL to process %d: %w", pidInt, err)
			}
		}
	}

	p.release()
	return nil
}

// Release process and remove pidfile
func (p *Process) release() {
	if proc := p.currentProc(); proc != nil {
		proc.Release()
	}
	p.setPID("")
	os.RemoveAll(p.path("pid"))
}

// ExitCode returns the exitcode associated with the process
func (p *Process) ExitCode() (string, error) {
	b, err := os.ReadFile(
		p.path("exitcode"),
	)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Watch the process
func (p *Process) monitor() {
	proc := p.currentProc()
	if proc == nil {
		return
	}
	defer close(p.done)

	// Start a goroutine to reap orphaned child processes
	// This is needed when we're acting as a subreaper
	go p.reapChildren()

	status := make(chan *os.ProcessState)
	died := make(chan error)
	go func() {
		state, err := proc.Wait()
		if err != nil {
			died <- err
			return
		}

		status <- state
	}()

	select {
	case s := <-status:
		os.WriteFile(
			p.path("exitcode"),
			[]byte(fmt.Sprint(s.ExitCode())),
			os.ModePerm,
		)
	case err := <-died:
		os.WriteFile(
			p.path("error"),
			[]byte(err.Error()),
			os.ModePerm,
		)
		p.release()
	}
}
