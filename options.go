package process

import (
	"os"
	"syscall"
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

// WithSysProcAttr sets the operating-system attributes used to create the
// process, for example the Windows CreationFlags (CREATE_NO_WINDOW,
// CREATE_NEW_PROCESS_GROUP and others) or HideWindow.
//
// Run uses a copy of attr, so the caller can reuse it. The library adds its
// own attributes to the copy:
//
//   - On Unix, Setpgid is set unless Setsid is set, so that Stop can signal
//     the process group. This is the behaviour without this option.
//   - On Windows, CREATE_NEW_PROCESS_GROUP is added when WithConsoleCtrlBreak
//     is enabled, and CREATE_SUSPENDED is added for a short time when
//     WithJobObject is enabled (see that option).
//
// If attr itself has CREATE_SUSPENDED on Windows, the library does not resume
// the process.
func WithSysProcAttr(attr *syscall.SysProcAttr) Option {
	return func(cfg *Config) error {
		cfg.SysProcAttr = attr
		return nil
	}
}

// WithJobObject puts the process in a Windows job object that has the
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE limit. Every process the child starts is
// in the same job, so:
//
//   - Stop terminates the whole process tree. KillProcessGroup has no effect
//     on Windows.
//   - If the calling program exits or crashes, Windows closes the job and
//     kills the tree.
//   - When the main process exits, the job is closed and any descendant that
//     is still running is killed.
//
// The process is created suspended and resumed after it is in the job, so it
// cannot start a child outside the job. Run returns an error, and the process
// is killed, if the job cannot be set up. The option is ignored on platforms
// other than Windows.
func WithJobObject(b bool) Option {
	return func(cfg *Config) error {
		cfg.JobObject = b
		return nil
	}
}

// WithConsoleCtrlBreak makes Stop on Windows try a graceful stop first: it
// sends CTRL_BREAK_EVENT to the process group of the child, waits up to
// GracefulTimeout for it to exit, and then terminates it.
//
// The option adds CREATE_NEW_PROCESS_GROUP to the creation flags. The event
// reaches only console processes that share the console of the calling
// program, so Stop skips it, and terminates at once, when the creation flags
// have DETACHED_PROCESS, CREATE_NEW_CONSOLE or CREATE_NO_WINDOW, when the
// handle did not start the process itself, or when GracefulTimeout is zero.
//
// It is off by default. Windows can deliver a console event for a process
// group that no longer exists to every process on the console, which includes
// the calling program. Stop checks that the child is alive just before it sends
// the event, but a child that exits at that moment leaves a short race. Enable
// it only if the calling program handles or tolerates CTRL_BREAK_EVENT.
//
// The option is ignored on platforms other than Windows.
func WithConsoleCtrlBreak(b bool) Option {
	return func(cfg *Config) error {
		cfg.ConsoleCtrlBreak = b
		return nil
	}
}
