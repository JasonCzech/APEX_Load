//go:build !windows

package main

import "os"

func setOwnerOnlyPermissions(path string, directory bool) error {
	mode := os.FileMode(privateFileMode)
	if directory {
		mode = privateDirectoryMode
	}
	return os.Chmod(path, mode)
}

func replacePrivateFile(from, to string) error {
	return os.Rename(from, to)
}
