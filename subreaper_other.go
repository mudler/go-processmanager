//go:build !linux

package process

// SetSubreaper is a no-op on non-Linux platforms.
// On Linux, it configures the calling process to be a subreaper.
func SetSubreaper() error {
	return nil
}
