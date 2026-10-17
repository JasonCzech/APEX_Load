//go:build !darwin && !linux && !windows

package browser

// nativeHARPlatformSupported is false until this platform's private ACL,
// handle identity, lock and durable publication behavior is qualified.
func nativeHARPlatformSupported() bool { return false }
