//go:build darwin

package main

import "golang.org/x/sys/unix"

func nativeRenameNoReplace(oldDirFD int, oldName string, newDirFD int, newName string) error {
	return unix.RenameatxNp(oldDirFD, oldName, newDirFD, newName, unix.RENAME_EXCL)
}
