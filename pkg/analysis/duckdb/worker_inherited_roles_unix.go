//go:build linux || darwin

package duckdb

import (
	"os"

	"golang.org/x/sys/unix"
)

const workerRawReadPipe = uint32(unix.S_IFIFO)
const workerRawPrivateDirectory = uint32(unix.S_IFDIR)

// Raw role inspection owns nothing: it cannot allocate a Go file wrapper,
// change flags, register a poller or close a number that may belong to runtime.
func workerInspectRawInheritedDescriptor(descriptor uintptr, kind uint32, private bool) (unix.Stat_t, error) {
	var info unix.Stat_t
	if descriptor > uintptr(^uint(0)>>1) || unix.Fstat(int(descriptor), &info) != nil || widenStatMode(info.Mode)&unix.S_IFMT != kind {
		return info, errWorkerProtocol
	}
	flags, err := unix.FcntlInt(descriptor, unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || private && (info.Uid != uint32(os.Geteuid()) || widenStatMode(info.Mode)&0077 != 0) {
		return info, errWorkerProtocol
	}
	return info, nil
}

// widenStatMode reads Stat_t.Mode, which is 16 bits on Darwin and 32 on Linux.
func widenStatMode[T ~uint16 | ~uint32](mode T) uint32 { return uint32(mode) }

func workerValidateRawInheritedChannels() error {
	bootstrap, err := workerInspectRawInheritedDescriptor(3, workerRawReadPipe, false)
	if err != nil {
		return err
	}
	if _, err = workerInspectRawInheritedDescriptor(4, workerRawPrivateDirectory, true); err != nil {
		return err
	}
	commands, err := workerInspectRawInheritedDescriptor(5, workerRawReadPipe, false)
	if err != nil || bootstrap.Dev == commands.Dev && bootstrap.Ino == commands.Ino {
		return errWorkerProtocol
	}
	return nil
}
