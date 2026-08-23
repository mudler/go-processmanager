package process_test

import (
	"os"
	"path/filepath"

	. "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Option errors", func() {
	Context("when an option fails to apply", func() {
		It("refuses to run instead of starting a misconfigured process", func() {
			// Apply stops at the first failing option, so every option after it
			// is silently dropped. Running anyway would start a process without
			// its arguments, environment or working directory.
			missing := filepath.Join(os.TempDir(), "go-processmanager-absent", "nested")
			Expect(os.Setenv("TMPDIR", missing)).To(Succeed())
			defer os.Unsetenv("TMPDIR")

			p := New(
				WithTemporaryStateDir(),
				WithName("/bin/echo"),
				WithArgs("hello"),
			)

			err := p.Run()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("process options"))
			Expect(p.IsAlive()).To(BeFalse())
		})

		It("still runs normally when every option applies", func() {
			dir, err := os.MkdirTemp("", "gopm-ok-*")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(dir)

			p := New(
				WithStateDir(dir),
				WithName("/bin/echo"),
				WithArgs("hello"),
			)
			Expect(p.Run()).To(Succeed())
		})
	})
})
