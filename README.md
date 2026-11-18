# go-processmanager

Updated version for 2024

* Updated gopsutils to v4
  * bumps module target to golang 1.18+
* Allows setting working directory
* _Breaking Change_ `config.KillSignal` is now an `*int` - avoids the need to parse at runtime. `options.WithKillSignal` will need to be updated.

## Windows

The library builds and runs on Windows. Some parts work differently from Unix,
because Windows has no signals and no process groups in the Unix sense.

### Options

| Option | Unix | Windows |
|---|---|---|
| `WithSysProcAttr(attr)` | Used as given, but `Setpgid` is always set unless `Setsid` is set. | Used as given. Set `CreationFlags` (for example `CREATE_NO_WINDOW`) or `HideWindow` here. |
| `WithJobObject(true)` | Ignored. | Puts the process in a job object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. |
| `WithConsoleCtrlBreak(true)` | Ignored. | `Stop` sends `CTRL_BREAK_EVENT` first and waits up to `GracefulTimeout`. |
| `WithKillSignal(n)` | Signal that `Stop` sends first. | Ignored. |
| `WithKillProcessGroup(b)` | `Stop` signals the whole process group. | No effect. Use `WithJobObject` to stop a process tree. |
| `WithSubreaper(true)` | Linux only. | Ignored. |

`Run` copies the `SysProcAttr` value and never changes the caller's value.

### Stop

- By default, `Stop` terminates the process at once with exit code 1, as
  `os.Process.Kill` does. It then waits up to 5 seconds for the process to go
  away, so that its log files are closed when `Stop` returns.
- With `WithConsoleCtrlBreak(true)`, `Stop` first sends `CTRL_BREAK_EVENT` to
  the process group of the child. Go programs receive it as `os.Interrupt`. If
  the process does not exit within `GracefulTimeout`, `Stop` terminates it.
  The event is skipped, and the process is terminated at once, when:
  - `GracefulTimeout` is zero,
  - the creation flags have `DETACHED_PROCESS`, `CREATE_NEW_CONSOLE` or
    `CREATE_NO_WINDOW`, so the child is not on the console of the caller,
  - the `Process` value did not start the process itself (for example
    `New(WithStateDir(dir)).Stop()`).
  This option is off by default. Windows can deliver a console event for a
  process group that no longer exists to every process on the console,
  including the caller. `Stop` checks that the child is running just before it
  sends the event, but a child that exits at that moment leaves a short race.
- `Stop` on a process that has already exited returns an error that wraps
  `os.ErrProcessDone`.

### Process trees

Without a job object, `Stop` terminates only the main process. Processes that
it started keep running.

With `WithJobObject(true)`:

- the process is created suspended, put in the job, and then resumed, so it
  cannot start a child outside the job,
- `Stop` terminates every process in the job,
- when the main process exits, the job is closed and its remaining processes
  are killed,
- if the calling program exits or crashes, Windows closes the job and kills
  the tree.

If the job cannot be set up, `Run` kills the process and returns an error.

### Liveness and PIDs

`IsAlive` opens the process and checks if its handle is signaled. It does not
rely on the exit code alone, because a process can exit with code 259
(`STILL_ACTIVE`). A process that exists but cannot be opened because access is
denied counts as alive.

While the `Process` value that started the process is tracking it, it keeps a
handle open, and Windows does not reuse the PID. After the process exits, the
pidfile stays in the state directory, and on any platform its PID can later
belong to another process. A check through the pidfile, for example
`New(WithStateDir(dir)).IsAlive()`, can then report that other process.

### State directory

The parent closes its copies of the `stdout` and `stderr` files after the
process starts. The state directory can be removed once the process and any
children that inherited those files have exited.
