package process_test

import (
	"os"

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
})
