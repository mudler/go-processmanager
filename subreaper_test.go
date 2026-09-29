//go:build linux

package process_test

import (
	"os"
	"os/exec"
	"time"

	. "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// waitAfterExit starts a short-lived command with os/exec, gives it long enough
// to exit and be reaped by anything else that may be reaping, and returns what
// Wait reports. The sleep is several times the reaper's 100ms poll, so a
// running reaper has certainly claimed the child by the time Wait runs. That
// makes the check deterministic rather than a race.
func waitAfterExit() error {
	cmd := exec.Command("/bin/true")
	Expect(cmd.Start()).ToNot(HaveOccurred())
	time.Sleep(500 * time.Millisecond)
	return cmd.Wait()
}

var _ = Describe("Subreaper", func() {
	// A managed process that outlives each example, so the monitor goroutine
	// (and its reaper, when enabled) is running while we exec.
	run := func(opts ...Option) *Process {
		args := append([]Option{
			WithName("/bin/sleep"),
			WithArgs("60"),
			WithTemporaryStateDir(),
		}, opts...)
		p := New(args...)
		Expect(p.Run()).ToNot(HaveOccurred())
		return p
	}

	Context("by default", func() {
		It("leaves os/exec in the host program alone", func() {
			p := run()
			defer os.RemoveAll(p.StateDir())
			defer p.Stop()

			// Without the reaper this is the ordinary case: our child is ours
			// to wait for, and Wait reports its exit status.
			Expect(waitAfterExit()).ToNot(HaveOccurred())
		})
	})

	Context("when the caller asks for it", func() {
		It("reaps orphans, and so also claims the host program's children", func() {
			p := run(WithSubreaper(true))
			defer os.RemoveAll(p.StateDir())
			defer p.Stop()

			// This is the documented cost of opting in: the reaper's wait(-1)
			// takes the child before os/exec can, so Wait loses the exit
			// status. Asserting it keeps the trade-off visible, and pins that
			// the option is actually wired to the reaper.
			err := waitAfterExit()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no child processes"))
		})
	})
})
