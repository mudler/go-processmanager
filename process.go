package process

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type Process struct {
	config Config
	// configErr holds the first error returned while applying this process's
	// options. Run reports it rather than starting a half-configured process.
	configErr error
	proc      *os.Process

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

	// plat holds the platform-specific state of a started process, such as
	// the Windows process and job object handles. It has its own lock.
	plat platformState
}

// ErrProcessAlreadyRun is returned by Run when the handle has already started a
// process. A Process is single-use: it owns exactly one done channel and one
// monitor goroutine, and neither is recreated, so restarting through the same
// handle is not supported. Construct a new Process to start another one.
//
// The message keeps the historical "command already started" prefix so callers
// matching on that text keep working.
var ErrProcessAlreadyRun = errors.New("command already started: a Process handle is single-use, construct a new one to start another process")

// New builds up a new process with options.
//
// An option that fails to apply is remembered and returned by Run, which
// refuses to start. New keeps returning only *Process so existing callers
// still compile, but the failure is no longer lost: Apply stops at the first
// failing option, so the ones after it never reach the config, and a process
// started in that state would run without its arguments, environment or
// working directory.
func New(p ...Option) *Process {
	c := DefaultConfig()
	err := c.Apply(p...)

	return &Process{
		config:    *c,
		configErr: err,
		done:      make(chan struct{}),
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
	// Checked before the run is claimed: a handle that can never start should
	// not burn its single use.
	if p.configErr != nil {
		return fmt.Errorf("applying process options: %w", p.configErr)
	}

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

	// Optionally set the current process as a subreaper, so that orphaned
	// descendants are reparented to us instead of init and can be cleaned up.
	// Off by default: this is a process-wide setting, and the reaper it turns
	// on claims any child of this process, including ones the Go runtime owns.
	if p.config.Subreaper {
		if err := SetSubreaper(); err != nil {
			// Non-fatal error - log and continue
			// This will fail on non-Linux systems but that's expected
		}
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

	sys, err := p.sysProcAttr()
	if err != nil {
		p.releaseClaim()
		return err
	}

	stdout := NewLog(p.StdoutPath())
	stderr := NewLog(p.StderrPath())
	proc := &os.ProcAttr{
		Dir: wd,
		Env: p.config.Environment,
		Files: []*os.File{
			p.config.Stdin,
			stdout,
			stderr,
		},
		Sys: sys,
	}
	args := append([]string{p.config.Name}, p.config.Args...)
	process, err := os.StartProcess(p.config.Name, args, proc)
	// The child has its own copies of the log files now. Keeping ours open
	// leaks two descriptors per Run and, on Windows, keeps the state
	// directory from being removed while the handle is alive.
	closeLog(stdout)
	closeLog(stderr)
	if err != nil {
		p.releaseClaim()
		return err
	}

	if err := p.afterStart(process); err != nil {
		// The process exists but could not be set up as asked. Do not leave
		// it running unmanaged.
		process.Kill()
		process.Wait()
		p.releaseClaim()
		return err
	}

	p.setProc(process)
	p.writePID(process)
	go p.monitor()
	return nil
}

// IsAlive checks if the process recorded in the pidfile is running.
//
// The check is by PID. A pidfile left behind by a process that has exited can
// name a PID that the operating system has since given to another process, and
// IsAlive then reports that process. See the README for the details on each
// platform.
func (p *Process) IsAlive() bool {
	pidStr, err := p.readPID()
	if err != nil {
		return false
	}
	pid, err := strconv.ParseInt(pidStr, 10, 64)
	if err != nil {
		return false
	}
	return p.processAlive(int(pid))
}

// Stop stops the process annotated in the pidfile.
//
// On Unix it sends KillSignal (SIGTERM by default) to the process, or to its
// process group, waits up to GracefulTimeout and then sends SIGKILL. On
// Windows, see the README: KillSignal is ignored, and the process is
// terminated unless a console break was requested with WithConsoleCtrlBreak.
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

	return p.stop(int(pidInt))
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

	// Start a goroutine to reap orphaned child processes, but only when we were
	// asked to act as a subreaper. It reaps with wait(-1), which claims any
	// child of the calling process, so running it unasked breaks os/exec for
	// the whole program.
	if p.config.Subreaper {
		go p.reapChildren()
	}

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

	var (
		state   *os.ProcessState
		waitErr error
	)
	select {
	case state = <-status:
	case waitErr = <-died:
	}

	// Release the platform resources tied to this process. On Windows this
	// closes the job object, if any.
	p.afterExit()

	if waitErr != nil {
		os.WriteFile(
			p.path("error"),
			[]byte(waitErr.Error()),
			os.ModePerm,
		)
		p.release()
		return
	}
	os.WriteFile(
		p.path("exitcode"),
		[]byte(fmt.Sprint(state.ExitCode())),
		os.ModePerm,
	)
}
