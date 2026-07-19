package process_test

import (
	"os"
	"sync"
	"time"

	. "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ProcessManager", func() {
	Context("smoke tests", func() {
		It("starts", func() {
			p := New(
				WithName("/bin/bash"),
				WithArgs("-c", `
while true;
do sleep 1 && echo "test";
done`),
				WithTemporaryStateDir(),
			)
			dir := p.StateDir()
			defer os.RemoveAll(dir)
			Expect(p.Run()).ToNot(HaveOccurred())
			Eventually(func() string {
				c, _ := os.ReadFile(p.StdoutPath())
				return string(c)
			}, "2m").Should(ContainSubstring("test"))

			Expect(p.Stop()).ToNot(HaveOccurred())
		})

		It("stops by reading pid dir", func() {
			dir, err := os.MkdirTemp(os.TempDir(), "")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(dir)

			p := New(
				WithStateDir(dir),
				WithName("/bin/bash"),
				WithArgs("-c", `
while true;
do sleep 1 && echo "test";
done`),
			)
			Expect(p.Run()).ToNot(HaveOccurred())

			Eventually(func() string {
				c, _ := os.ReadFile(p.StdoutPath())
				return string(c)
			}, "2m").Should(ContainSubstring("test"))

			Eventually(func() bool {
				return New(
					WithStateDir(dir),
				).IsAlive()
			}, "2m").Should(BeTrue())

			Expect(New(
				WithStateDir(dir)).Stop()).ToNot(HaveOccurred())
		})

	})

	Context("config", func() {
		It("handles working directory", func() {
			dir1, err := os.MkdirTemp(os.TempDir(), "")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(dir1)
			dir2, err := os.MkdirTemp(os.TempDir(), "")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(dir2)

			p := New(
				WithStateDir(dir1),
				WithName("/bin/bash"),
				WithArgs("-c", `
pwd
`),
				WithWorkDir(dir2),
			)

			Expect(p.Run()).ToNot(HaveOccurred())

			Eventually(func() string {
				c, _ := os.ReadFile(p.StdoutPath())
				return string(c)
			}, "2m").Should(ContainSubstring(dir2))

		})
	})

	Context("exit codes", func() {
		It("correctly returns 0", func() {
			dir, err := os.MkdirTemp(os.TempDir(), "")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(dir)

			p := New(
				WithStateDir(dir),
				WithName("/bin/bash"),
				WithArgs("-c", `
echo "ok"
`),
			)

			Expect(p.Run()).ToNot(HaveOccurred())
			Eventually(func() bool {
				return New(
					WithStateDir(dir),
				).IsAlive()
			}, "2m").Should(BeFalse())
			e, err := New(WithStateDir(dir)).ExitCode()
			Expect(err).ToNot(HaveOccurred())
			Expect(e).To(Equal("0"))
		})
		It("correctly returns 2", func() {
			dir, err := os.MkdirTemp(os.TempDir(), "")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(dir)

			p := New(
				WithStateDir(dir),
				WithName("/bin/bash"),
				WithArgs("-c", `
exit 2
`),
			)

			Expect(p.Run()).ToNot(HaveOccurred())
			Eventually(func() bool {
				return New(
					WithStateDir(dir),
				).IsAlive()
			}, "2m").Should(BeFalse())
			e, err := New(WithStateDir(dir)).ExitCode()
			Expect(err).ToNot(HaveOccurred())
			Expect(e).To(Equal("2"))
		})
	})

	Context("concurrency", func() {
		// A bare Run/Stop cycle is enough to trip the race detector: the
		// library's own monitor goroutine polls IsAlive while the caller is in
		// Stop. This spec only asserts the normal lifecycle - its real
		// assertion is made by -race, so it must be run with it enabled.
		It("does not race between Stop, IsAlive and the monitor goroutine", func() {
			dir, err := os.MkdirTemp(os.TempDir(), "")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(dir)

			p := New(
				WithStateDir(dir),
				WithName("/bin/bash"),
				WithArgs("-c", `
echo "starting"
while true; do
  sleep 1
done
`),
				WithGracefulTimeout(2*time.Second),
			)
			Expect(p.Run()).ToNot(HaveOccurred())

			Eventually(func() string {
				c, _ := os.ReadFile(p.StdoutPath())
				return string(c)
			}, "10s").Should(ContainSubstring("starting"))

			// Widen the window by having callers read process state while the
			// monitor goroutine and Stop are both running.
			stopReaders := make(chan struct{})
			var readers sync.WaitGroup
			for i := 0; i < 4; i++ {
				readers.Add(1)
				go func() {
					defer readers.Done()
					for {
						select {
						case <-stopReaders:
							return
						default:
							p.IsAlive()
							p.CurrentPID()
						}
					}
				}()
			}

			Expect(p.Stop()).ToNot(HaveOccurred())
			close(stopReaders)
			readers.Wait()

			Eventually(func() bool {
				return p.IsAlive()
			}, "10s").Should(BeFalse())
			Expect(p.CurrentPID()).To(Equal(""))
		})
	})

	Context("process group termination", func() {
		It("kills child processes when parent is stopped", func() {
			dir, err := os.MkdirTemp(os.TempDir(), "")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(dir)

			// Start a parent process that spawns a child process
			p := New(
				WithStateDir(dir),
				WithName("/bin/bash"),
				WithArgs("-c", `
echo "starting"

# Start a child process that runs indefinitely
(
  while true; do
    sleep 1
  done
) &

# Parent also runs indefinitely
while true; do
  sleep 1
done
`),
			)
			Expect(p.Run()).ToNot(HaveOccurred())

			// Wait for the process to start
			Eventually(func() string {
				c, _ := os.ReadFile(p.StdoutPath())
				return string(c)
			}, "5s").Should(ContainSubstring("starting"))

			// Verify process is alive
			Expect(p.IsAlive()).To(BeTrue())

			// Stop the parent process (should kill entire process group)
			Expect(p.Stop()).ToNot(HaveOccurred())

			// Verify the process is no longer alive
			Eventually(func() bool {
				return p.IsAlive()
			}, "10s").Should(BeFalse())
		})
	})
})
