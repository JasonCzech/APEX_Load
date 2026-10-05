//go:build !windows

package duckdb

import "os"

func workerCreateScratchDirectory(parent, prefix string, owner workerScratchOwner) (string, error) {
	return os.MkdirTemp(parent, prefix)
}
func workerPrivateDescriptor(file *os.File) error {
	info, err := file.Stat()
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return errWorkerProtocol
	}
	return nil
}
