//go:build windows

package process_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	. "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"golang.org/x/sys/windows"
)

// The tests start the test binary itself as the managed process. The
// environment variable below selects what it does, so the tests need no
// program that may be missing on a Windows host.
const (
	helperEnv     = "GOPM_TEST_HELPER"
	helperCodeEnv = "GOPM_TEST_EXIT_CODE"
	helperPIDEnv  = "GOPM_TEST_PID_FILE"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		runHelper(mode)
		return
	}
	os.Exit(m.Run())
}

func runHelper(mode string) {
	switch mode {
	case "sleep":
		fmt.Println("ready")
		time.Sleep(time.Minute)
	case "exit":
		// Give the test time to open a handle to this process first.
		time.Sleep(2 * time.Second)
		code, _ := strconv.Atoi(os.Getenv(helperCodeEnv))
		os.Exit(code)
	case "tree", "tree-exit":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := child.Start(); err != nil {
			fmt.Println("start child:", err)
			os.Exit(1)
		}
		pid := strconv.Itoa(child.Process.Pid)
		if err := os.WriteFile(os.Getenv(helperPIDEnv), []byte(pid), 0o644); err != nil {
			os.Exit(1)
		}
		fmt.Println("ready")
		if mode == "tree-exit" {
			time.Sleep(2 * time.Second)
			os.Exit(0)
		}
		time.Sleep(time.Minute)
	case "break":
		ch := make(chan os.Signal, 1)
		// Go delivers CTRL_BREAK_EVENT as os.Interrupt.
		signal.Notify(ch, os.Interrupt)
		fmt.Println("ready")
		select {
		case <-ch:
			fmt.Println("graceful")
			os.Exit(0)
		case <-time.After(time.Minute):
			os.Exit(1)
		}
	}
	os.Exit(2)
}

// helper returns the options that start this test binary in the given mode.
func helper(mode string, env ...string) []Option {
	self, err := os.Executable()
	Expect(err).ToNot(HaveOccurred())
	return []Option{
		WithName(self),
		WithEnvironment(append(append(os.Environ(), helperEnv+"="+mode), env...)...),
	}
}

func newHelper(dir, mode string, opts []Option, env ...string) *Process {
	return New(append(append(helper(mode, env...), WithStateDir(dir)), opts...)...)
}

func stdoutOf(p *Process) func() string {
	return func() string {
		b, _ := os.ReadFile(p.StdoutPath())
		return string(b)
	}
}

// openProcess opens pid for waiting, which also keeps the PID from being
// reused while the test looks at it.
func openProcess(pid int) windows.Handle {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, uint32(pid))
	Expect(err).ToNot(HaveOccurred())
	return h
}

// exitsWithin reports whether the process behind h exits within d.
func exitsWithin(h windows.Handle, d time.Duration) bool {
	ev, err := windows.WaitForSingleObject(h, uint32(d/time.Millisecond))
	return err == nil && ev == windows.WAIT_OBJECT_0
}

func readChildPID(path string) int {
	var pid int
	Eventually(func() error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err
	}, "10s").Should(Succeed())
	return pid
}

// hasConsole reports whether the test process is attached to a console,
// which a console control event needs.
func hasConsole() bool {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	var list [8]uint32
	n, _, _ := proc.Call(uintptr(unsafe.Pointer(&list[0])), uintptr(len(list)))
	return n != 0
}

func pidOf(p *Process) int {
	pid, err := strconv.Atoi(p.CurrentPID())
	Expect(err).ToNot(HaveOccurred())
	return pid
}

var _ = Describe("Windows", func() {
	var dir string

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "gopm")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		// The state directory can only be removed when no handle to the log
		// files is left open.
		Eventually(func() error { return os.RemoveAll(dir) }, "10s").Should(Succeed())
	})

	It("reports a running process as alive and stops it", func() {
		p := newHelper(dir, "sleep", nil)
		Expect(p.Run()).To(Succeed())
		Eventually(stdoutOf(p), "10s").Should(ContainSubstring("ready"))

		Expect(p.IsAlive()).To(BeTrue())
		// A handle that did not start the process checks it by PID.
		Expect(New(WithStateDir(dir)).IsAlive()).To(BeTrue())

		Expect(p.Stop()).To(Succeed())
		Eventually(p.Done(), "10s").Should(BeClosed())
		Expect(p.IsAlive()).To(BeFalse())
		Expect(os.RemoveAll(dir)).To(Succeed())
	})

	It("reports a process that exited with code 259 as dead", func() {
		p := newHelper(dir, "exit", nil, helperCodeEnv+"=259")
		Expect(p.Run()).To(Succeed())

		// Keep the process object alive after it exits, so the PID check
		// finds it and must not trust the STILL_ACTIVE exit code.
		h := openProcess(pidOf(p))
		defer windows.CloseHandle(h)

		Eventually(p.Done(), "10s").Should(BeClosed())
		Expect(p.ExitCode()).To(Equal("259"))
		// The pidfile is still there after a natural exit.
		Expect(New(WithStateDir(dir)).IsAlive()).To(BeFalse())
		Expect(p.IsAlive()).To(BeFalse())
	})

	It("records the exit code", func() {
		p := newHelper(dir, "exit", nil, helperCodeEnv+"=3")
		Expect(p.Run()).To(Succeed())
		Eventually(p.Done(), "10s").Should(BeClosed())
		Expect(p.ExitCode()).To(Equal("3"))
	})

	It("returns ErrProcessDone when stopping a process that has exited", func() {
		p := newHelper(dir, "exit", nil, helperCodeEnv+"=0")
		Expect(p.Run()).To(Succeed())
		Eventually(p.Done(), "10s").Should(BeClosed())
		err := p.Stop()
		Expect(errors.Is(err, os.ErrProcessDone)).To(BeTrue(), "got %v", err)
	})

	It("stops a process found through the state directory", func() {
		p := newHelper(dir, "sleep", nil)
		Expect(p.Run()).To(Succeed())
		Eventually(stdoutOf(p), "10s").Should(ContainSubstring("ready"))

		Expect(New(WithStateDir(dir)).Stop()).To(Succeed())
		Eventually(p.Done(), "10s").Should(BeClosed())
	})

	It("ignores the subreaper option", func() {
		p := newHelper(dir, "sleep", []Option{WithSubreaper(true)})
		Expect(p.Run()).To(Succeed())
		Eventually(stdoutOf(p), "10s").Should(ContainSubstring("ready"))
		Expect(p.Stop()).To(Succeed())
		Eventually(p.Done(), "10s").Should(BeClosed())
	})

	Context("without a job object", func() {
		It("stops only the main process", func() {
			pidFile := filepath.Join(dir, "child.pid")
			p := newHelper(dir, "tree", nil, helperPIDEnv+"="+pidFile)
			Expect(p.Run()).To(Succeed())
			Eventually(stdoutOf(p), "10s").Should(ContainSubstring("ready"))
			child := openProcess(readChildPID(pidFile))
			defer windows.CloseHandle(child)
			defer windows.TerminateProcess(child, 1)

			Expect(p.Stop()).To(Succeed())
			Eventually(p.Done(), "10s").Should(BeClosed())
			// The documented limit: a descendant survives.
			Expect(exitsWithin(child, time.Second)).To(BeFalse())
		})
	})

	Context("with a job object", func() {
		It("starts the process and stops the whole tree", func() {
			pidFile := filepath.Join(dir, "child.pid")
			p := newHelper(dir, "tree", []Option{WithJobObject(true)}, helperPIDEnv+"="+pidFile)
			Expect(p.Run()).To(Succeed())
			// The process was created suspended: it only prints once resumed.
			Eventually(stdoutOf(p), "10s").Should(ContainSubstring("ready"))
			child := openProcess(readChildPID(pidFile))
			defer windows.CloseHandle(child)
			defer windows.TerminateProcess(child, 1)

			Expect(p.Stop()).To(Succeed())
			Eventually(p.Done(), "10s").Should(BeClosed())
			Expect(exitsWithin(child, 10*time.Second)).To(BeTrue())
		})

		It("kills the remaining descendants when the main process exits", func() {
			pidFile := filepath.Join(dir, "child.pid")
			p := newHelper(dir, "tree-exit", []Option{WithJobObject(true)}, helperPIDEnv+"="+pidFile)
			Expect(p.Run()).To(Succeed())
			child := openProcess(readChildPID(pidFile))
			defer windows.CloseHandle(child)
			defer windows.TerminateProcess(child, 1)

			Eventually(p.Done(), "10s").Should(BeClosed())
			Expect(p.ExitCode()).To(Equal("0"))
			Expect(exitsWithin(child, 10*time.Second)).To(BeTrue())
		})

		It("leaves the caller's SysProcAttr unchanged", func() {
			attr := &syscall.SysProcAttr{}
			p := newHelper(dir, "sleep", []Option{WithJobObject(true), WithSysProcAttr(attr)})
			Expect(p.Run()).To(Succeed())
			Eventually(stdoutOf(p), "10s").Should(ContainSubstring("ready"))
			Expect(attr.CreationFlags).To(BeZero())
			Expect(p.Stop()).To(Succeed())
			Eventually(p.Done(), "10s").Should(BeClosed())
		})
	})

	Context("with a console break", func() {
		It("lets a console process exit gracefully", func() {
			if !hasConsole() {
				Skip("the test process has no console, so it cannot send a console event")
			}
			p := newHelper(dir, "break", []Option{
				WithConsoleCtrlBreak(true),
				WithGracefulTimeout(20 * time.Second),
			})
			Expect(p.Run()).To(Succeed())
			Eventually(stdoutOf(p), "10s").Should(ContainSubstring("ready"))

			start := time.Now()
			Expect(p.Stop()).To(Succeed())
			Expect(time.Since(start)).To(BeNumerically("<", 15*time.Second))
			Eventually(p.Done(), "10s").Should(BeClosed())
			Expect(stdoutOf(p)()).To(ContainSubstring("graceful"))
			Expect(p.ExitCode()).To(Equal("0"))
		})

		It("terminates at once a process without the caller's console", func() {
			p := newHelper(dir, "break", []Option{
				WithConsoleCtrlBreak(true),
				WithGracefulTimeout(30 * time.Second),
				WithSysProcAttr(&syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}),
			})
			Expect(p.Run()).To(Succeed())
			Eventually(stdoutOf(p), "10s").Should(ContainSubstring("ready"))

			start := time.Now()
			Expect(p.Stop()).To(Succeed())
			Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second))
			Eventually(p.Done(), "10s").Should(BeClosed())
			Expect(stdoutOf(p)()).ToNot(ContainSubstring("graceful"))
		})
	})
})
