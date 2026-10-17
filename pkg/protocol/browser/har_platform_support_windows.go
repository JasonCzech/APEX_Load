//go:build windows

package browser

// nativeHARPlatformSupported: private creation and validation through
// security descriptors, handle-based identity, the descriptor lease and
// handle-relative retirement are qualified on local NTFS.
func nativeHARPlatformSupported() bool { return true }
