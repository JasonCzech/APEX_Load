//go:build freebsd || openbsd || netbsd || dragonfly

package main

import "golang.org/x/sys/unix"

// The supported BSD targets do not expose a portable descriptor-relative
// exclusive-rename API through x/sys. Report unsupported so the common code
// uses its atomic hard-link fallback and otherwise fails closed.
func nativeRenameNoReplace(_ int, _ string, _ int, _ string) error {
	return unix.EOPNOTSUPP
}
