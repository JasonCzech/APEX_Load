//go:build !windows

package importartifact

import "os"

func validateDurableFilesystem(string) error              { return nil }
func mkdirAllDurably(path string, mode os.FileMode) error { return os.MkdirAll(path, mode) }
func publishCreateOnly(existingPath, targetPath string) error {
	return os.Link(existingPath, targetPath)
}
func renameWriteThrough(oldPath, newPath string) error     { return os.Rename(oldPath, newPath) }
func removeWriteThrough(_ string, targetPath string) error { return os.Remove(targetPath) }
func cleanupDurableTombstones(string) error                { return nil }
