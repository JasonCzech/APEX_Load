//go:build darwin

package main

import "golang.org/x/sys/unix"

func nativeRenameExchange(oldDirFD int, oldName string, newDirFD int, newName string) error {
	return unix.RenameatxNp(oldDirFD, oldName, newDirFD, newName, unix.RENAME_SWAP)
}
