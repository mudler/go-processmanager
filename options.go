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
