//go:build linux

package process_test

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	. "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// openFiles returns the targets of the file descriptors of this process.
func openFiles() []string {
	entries, err := os.ReadDir("/proc/self/fd")
	Expect(err).ToNot(HaveOccurred())
	var targets []string
	for _, e := range entries {
		t, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err == nil {
			targets = append(targets, t)
		}
	}
	return targets
}

func sleeper(dir string, opts ...Option) *Process {
	return New(append([]Option{
		WithStateDir(dir),
		WithName("/bin/sleep"),
		WithArgs("60"),
	}, opts...)...)
}

func pgidOf(p *Process) int {
	pid, err := strconv.Atoi(p.CurrentPID())
	Expect(err).ToNot(HaveOccurred())
	pgid, err := syscall.Getpgid(pid)
	Expect(err).ToNot(HaveOccurred())
	Expect(pgid).To(Equal(pid))
	return pgid
}

var _ = Describe("Linux", func() {
	var dir string

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "gopm")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(dir)
	})

	It("does not keep the log files open in the calling process", func() {
		p := sleeper(dir)
		Expect(p.Run()).To(Succeed())
		defer p.Stop()

		Expect(openFiles()).ToNot(ContainElement(p.StdoutPath()))
		Expect(openFiles()).ToNot(ContainElement(p.StderrPath()))
	})

	Context("WithSysProcAttr", func() {
		It("keeps the process in its own process group", func() {
			attr := &syscall.SysProcAttr{}
			p := sleeper(dir, WithSysProcAttr(attr))
			Expect(p.Run()).To(Succeed())
			defer p.Stop()

			pgidOf(p)
			// Run works on a copy.
			Expect(attr.Setpgid).To(BeFalse())
		})

		It("accepts Setsid, which also makes a new process group", func() {
			p := sleeper(dir, WithSysProcAttr(&syscall.SysProcAttr{Setsid: true}))
			Expect(p.Run()).To(Succeed())

			pid, err := strconv.Atoi(p.CurrentPID())
			Expect(err).ToNot(HaveOccurred())
			pgidOf(p)
			sid, _, errno := syscall.RawSyscall(syscall.SYS_GETSID, uintptr(pid), 0, 0)
			Expect(errno).To(BeZero())
			Expect(int(sid)).To(Equal(pid))

			Expect(p.Stop()).To(Succeed())
			Eventually(p.Done(), "10s").Should(BeClosed())
		})
	})

	It("ignores the Windows-only options", func() {
		p := sleeper(dir, WithJobObject(true), WithConsoleCtrlBreak(true))
		Expect(p.Run()).To(Succeed())
		Expect(p.IsAlive()).To(BeTrue())
		Expect(p.Stop()).To(Succeed())
		Eventually(p.Done(), "10s").Should(BeClosed())
	})
})
