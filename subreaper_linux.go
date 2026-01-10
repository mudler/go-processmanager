//go:build linux

package process

import "golang.org/x/sys/unix"

// SetSubreaper configures the calling process to be a subreaper.
// A subreaper fulfills the role of init(1) for its descendant processes.
// When a process becomes orphaned (its immediate parent terminates), it will be
// reparented to the nearest still living ancestor subreaper.
// This is useful in containerized environments to ensure proper cleanup of
// orphaned child processes.
func SetSubreaper() error {
	return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}
