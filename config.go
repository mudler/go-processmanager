package process

import (
	"os"
	"syscall"
	"time"
)

type Option func(cfg *Config) error

type Config struct {
	Name             string
	Args             []string
	Combined         bool
	StateDir         string
	KillSignal       *int
	Environment      []string
	Stdin            *os.File
	WorkDir          string
	GracefulTimeout  time.Duration // Time to wait after SIGTERM before SIGKILL
	KillProcessGroup bool          // Whether to kill entire process group (default true)
	Subreaper        bool          // Whether to become a child subreaper and reap orphans (default false)

	// SysProcAttr holds caller-supplied attributes for process creation. Run
	// copies it and never changes the caller's value. On Unix, Setpgid is
	// always set (unless Setsid is set) so that Stop can signal the group.
	SysProcAttr *syscall.SysProcAttr
	// JobObject puts the process in a Windows job object that kills every
	// process in it when the job is closed. Ignored on other platforms.
	JobObject bool
	// ConsoleCtrlBreak makes Stop send CTRL_BREAK_EVENT before it terminates
	// the process. Windows only. Ignored on other platforms.
	ConsoleCtrlBreak bool
}

func DefaultConfig() *Config {
	return &Config{
		Environment:      os.Environ(),
		KillProcessGroup: true,         // Default to killing process group
		GracefulTimeout:  15 * time.Second, // Default 15 second grace period
	}
}

// Apply applies the given options to the config, returning the first error
// encountered (if any).
func (cfg *Config) Apply(opts ...Option) error {
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(cfg); err != nil {
			return err
		}
	}
	return nil
}
