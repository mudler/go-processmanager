package process

import (
	"os"
	"time"
)

// WithKillSignal sets the given signal while attemping to stop. Defaults to 9
func WithKillSignal(i int) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.KillSignal = &i
		return nil
	}
}

func WithEnvironment(s ...string) Option {
	return func(cfg *Config) error {
		cfg.Environment = s
		return nil
	}
}

func WithTemporaryStateDir() func(cfg *Config) error {
	return func(cfg *Config) error {
		dir, err := os.MkdirTemp(os.TempDir(), "go-processmanager")
		cfg.StateDir = dir
		return err
	}
}

func WithStateDir(s string) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.StateDir = s
		return nil
	}
}

func WithSTDIN(f *os.File) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.Stdin = f
		return nil
	}
}

func WithName(s string) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.Name = s
		return nil
	}
}

func WithArgs(s ...string) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.Args = append(cfg.Args, s...)
		return nil
	}
}

func WithWorkDir(s string) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.WorkDir = s
		return nil
	}
}

// WithGracefulTimeout sets the duration to wait after SIGTERM before SIGKILL
func WithGracefulTimeout(d time.Duration) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.GracefulTimeout = d
		return nil
	}
}

// WithKillProcessGroup enables or disables killing the entire process group
func WithKillProcessGroup(b bool) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.KillProcessGroup = b
		return nil
	}
}

// WithSubreaper makes the calling process a child subreaper for the lifetime of
// this process, and reaps any orphan reparented to it.
//
// It is off by default, and should only be enabled by a program that is the
// init of its own PID namespace or container. Reaping orphans means calling
// wait(-1), which claims *any* child of the calling process, including children
// the Go runtime is waiting on. With this enabled, an unrelated
// exec.Command(...).Run() elsewhere in the program can lose its exit status and
// fail with "waitid: no child processes".
func WithSubreaper(b bool) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.Subreaper = b
		return nil
	}
}
