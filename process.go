package process

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

type Process struct {
	config Config
	proc   *os.Process
	PID    string
}

// New builds up a new process with options
func New(p ...Option) *Process {
	c := DefaultConfig()
	c.Apply(p...)

	return &Process{
		config: *c,
	}
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

func (p *Process) readPID() (string, error) {
	b, err := ioutil.ReadFile(
		p.path("pid"),
	)
	if err != nil {
		return "", err
	}
	p.PID = string(b)
	return p.PID, nil
}

func (p *Process) writePID() error {
	p.PID = fmt.Sprint(p.proc.Pid)
	return ioutil.WriteFile(
		p.path("pid"),
		[]byte(p.PID),
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

// Run starts the process and returns any error
func (p *Process) Run() error {

	p.readPID()
	if p.proc != nil || p.PID != "" {
		return errors.New("command already started")
	}

	if !exists(p.config.StateDir) {
		err := os.MkdirAll(p.config.StateDir, os.ModePerm)
		if err != nil {
			return err
		}
	}

	wd, _ := os.Getwd()
	proc := &os.ProcAttr{
		Dir: wd,
		Env: p.config.Environment,
		Files: []*os.File{
			os.Stdin,
			NewLog(p.StdoutPath()),
			NewLog(p.StderrPath()),
		},
	}
	args := append([]string{p.config.Name}, p.config.Args...)
	process, err := os.StartProcess(p.config.Name, args, proc)
	if err != nil {
		return err
	}

	p.proc = process
	p.writePID()
	go p.monitor()
	return nil
}

// IsAlive checks if the process is running or not
func (p *Process) IsAlive() bool {
	p.readPID()
	pid, err := strconv.ParseInt(p.PID, 10, 64)
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

// Stop stops the running process by senging KillSignal to the PID annotated in the pidfile
func (p *Process) Stop() error {
	pid, err := p.readPID()
	if err != nil || pid == "" {
		return errors.New("no pid")
	}

	sig := "-9"
	if p.config.KillSignal != "" {
		sig = fmt.Sprintf("-%s", p.config.KillSignal)
	}
	cmd := exec.Command("kill", sig, pid)
	_, err = cmd.CombinedOutput()
	if err != nil {
		return err
	}
	p.release()
	return nil
}

//Release process and remove pidfile
func (p *Process) release() {
	if p.proc != nil {
		p.proc.Release()
	}
	p.PID = ""
	os.RemoveAll(p.path("pid"))
}

// ExitCode returns the exitcode associated with the process
func (p *Process) ExitCode() (string, error) {
	b, err := ioutil.ReadFile(
		p.path("exitcode"),
	)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Watch the process
func (p *Process) monitor() {
	if p.proc == nil {
		return
	}
	status := make(chan *os.ProcessState)
	died := make(chan error)
	go func() {
		state, err := p.proc.Wait()
		if err != nil {
			died <- err
			return
		}

		status <- state
	}()

	select {
	case s := <-status:
		ioutil.WriteFile(
			p.path("exitcode"),
			[]byte(fmt.Sprint(s.ExitCode())),
			os.ModePerm,
		)
	case err := <-died:
		ioutil.WriteFile(
			p.path("error"),
			[]byte(err.Error()),
			os.ModePerm,
		)
		p.release()
	}
}
