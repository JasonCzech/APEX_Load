//go:build darwin

package localfs

import (
	"os"

	"golang.org/x/sys/unix"
)

var localTypes = map[string]bool{"apfs": true, "hfs": true}

// localDarwin reports whether a mount's flags and type name are an
// allowlisted local filesystem: APFS or HFS, mounted local.
func localDarwin(flags uint32, typeName string) bool {
	return flags&unix.MNT_LOCAL != 0 && localTypes[typeName]
}

func isLocal(path string) (bool, error) {
	dir, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer dir.Close()
	var stat unix.Statfs_t
	if err = unix.Fstatfs(int(dir.Fd()), &stat); err != nil {
		return false, err
	}
	return localDarwin(stat.Flags, unix.ByteSliceToString(stat.Fstypename[:])), nil
}
