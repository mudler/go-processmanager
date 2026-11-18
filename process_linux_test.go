//go:build linux

package process_test

import (
	"os"
	"path/filepath"

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

})
