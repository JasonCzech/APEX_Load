//go:build freebsd || openbsd || netbsd || dragonfly

package main

import "golang.org/x/sys/unix"

// The supported BSD targets do not expose a portable descriptor-relative
// exchange primitive through x/sys. Existing-file publication therefore fails
// closed instead of falling back to a check-then-replace race.
func nativeRenameExchange(_ int, _ string, _ int, _ string) error {
	return unix.EOPNOTSUPP
}
