//go:build darwin && cgo

package main

/*
#include <copyfile.h>
#include <errno.h>
#include <sys/acl.h>

static int apexload_copy_security_metadata(int source_fd, int destination_fd) {
	if (fcopyfile(source_fd, destination_fd, NULL, COPYFILE_ACL | COPYFILE_XATTR) == 0) {
		return 0;
	}
	return errno;
}

static int apexload_clear_extended_acl(int fd) {
	acl_t empty = acl_init(0);
	if (empty == NULL) {
		return errno;
	}
	int result = acl_set_fd_np(fd, empty, ACL_TYPE_EXTENDED);
	int saved_errno = result == 0 ? 0 : errno;
	acl_free(empty);
	if (saved_errno == EOPNOTSUPP) {
		return 0;
	}
	return saved_errno;
}

static int apexload_extended_acl_is_empty(int fd, int *is_empty) {
	acl_t acl = acl_get_fd_np(fd, ACL_TYPE_EXTENDED);
	if (acl == NULL) {
		if (errno == EOPNOTSUPP || errno == ENOENT) {
			*is_empty = 1;
			return 0;
		}
		return errno;
	}
	acl_entry_t entry;
	int result = acl_get_entry(acl, ACL_FIRST_ENTRY, &entry);
	int saved_errno = result < 0 ? errno : 0;
	*is_empty = result == 0;
	acl_free(acl);
	return saved_errno;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const maxDarwinSaveMetadataBytes = 16 << 20

func prepareNewUnixSaveStagingMetadata(fd int) error {
	return resetUnixSaveTombstoneMetadata(fd)
}

// resetUnixSaveTombstoneMetadata removes every metadata channel that can
// carry stale content or access grants into a later save. On macOS this must
// include the extended ACL and all xattrs, including com.apple.ResourceFork.
func resetUnixSaveTombstoneMetadata(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect file flags: %w", err)
	}
	immutableFlags := uint32(unix.UF_IMMUTABLE | unix.UF_APPEND | unix.SF_IMMUTABLE | unix.SF_APPEND)
	if stat.Flags&immutableFlags != 0 {
		return fmt.Errorf("file has append-only or immutable flags")
	}
	if stat.Flags != 0 {
		if err := unix.Fchflags(fd, 0); err != nil {
			return fmt.Errorf("clear file flags: %w", err)
		}
	}

	names, supported, err := readDarwinFDXattrNames(fd)
	if err != nil {
		return fmt.Errorf("list extended attributes: %w", err)
	}
	if supported {
		for _, name := range names {
			if err := unix.Fremovexattr(fd, name); err != nil && !errors.Is(err, unix.ENOATTR) {
				return fmt.Errorf("remove extended attribute %q: %w", name, err)
			}
		}
	}
	if errno := C.apexload_clear_extended_acl(C.int(fd)); errno != 0 {
		return fmt.Errorf("clear extended ACL: %w", syscall.Errno(errno))
	}
	return unixSaveTombstoneMetadataClean(fd)
}

func unixSaveTombstoneMetadataClean(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect file flags: %w", err)
	}
	if stat.Flags != 0 {
		return fmt.Errorf("file retains inode flags")
	}
	names, supported, err := readDarwinFDXattrNames(fd)
	if err != nil {
		return fmt.Errorf("list extended attributes: %w", err)
	}
	if supported {
		unsafeNames := make([]string, 0, len(names))
		for _, name := range names {
			// macOS may immediately recreate this kernel-managed provenance
			// marker after it is removed. It is not an ACL, resource fork, or
			// caller-controlled metadata channel; every other xattr must be gone.
			if name != "com.apple.provenance" {
				unsafeNames = append(unsafeNames, name)
			}
		}
		if len(unsafeNames) != 0 {
			return fmt.Errorf("file retains extended attributes %q", unsafeNames)
		}
	}
	var empty C.int
	if errno := C.apexload_extended_acl_is_empty(C.int(fd), &empty); errno != 0 {
		return fmt.Errorf("inspect extended ACL: %w", syscall.Errno(errno))
	}
	if empty == 0 {
		return fmt.Errorf("file retains an extended ACL")
	}
	return nil
}

func readDarwinFDXattrNames(fd int) ([]string, bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		size, err := unix.Flistxattr(fd, nil)
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if size > maxDarwinSaveMetadataBytes {
			return nil, true, fmt.Errorf("extended attribute name list exceeds %d bytes", maxDarwinSaveMetadataBytes)
		}
		if size == 0 {
			return nil, true, nil
		}
		list := make([]byte, size)
		read, err := unix.Flistxattr(fd, list)
		if errors.Is(err, unix.ERANGE) {
			continue
		}
		if err != nil {
			return nil, true, err
		}
		names := make([]string, 0)
		for _, name := range strings.Split(string(list[:read]), "\x00") {
			if name != "" {
				names = append(names, name)
			}
		}
		return names, true, nil
	}
	return nil, true, fmt.Errorf("extended attribute list changed repeatedly while being read")
}

// preserveReplacementMetadata copies the security-bearing inode metadata that
// rename(2) would otherwise discard. fcopyfile is the macOS-supported API for
// preserving both extended ACLs and extended attributes (including resource
// forks and quarantine metadata) between already-verified descriptors.
func preserveReplacementMetadata(sourceFD, destinationFD int, _ os.FileInfo) error {
	var before unix.Stat_t
	if err := unix.Fstat(sourceFD, &before); err != nil {
		return fmt.Errorf("inspect existing file metadata: %w", err)
	}
	immutableFlags := uint32(unix.UF_IMMUTABLE | unix.UF_APPEND | unix.SF_IMMUTABLE | unix.SF_APPEND)
	if before.Flags&immutableFlags != 0 {
		return fmt.Errorf("existing file has append-only or immutable flags that cannot be transferred atomically")
	}
	if err := unix.Fchown(destinationFD, int(before.Uid), int(before.Gid)); err != nil {
		return fmt.Errorf("preserve file ownership: %w", err)
	}
	if err := unix.Fchmod(destinationFD, uint32(before.Mode)&0o7777); err != nil {
		return fmt.Errorf("preserve file permissions: %w", err)
	}
	if errno := C.apexload_copy_security_metadata(C.int(sourceFD), C.int(destinationFD)); errno != 0 {
		return fmt.Errorf("copy ACLs and extended attributes: %w", syscall.Errno(errno))
	}
	if err := unix.Fchflags(destinationFD, int(before.Flags)); err != nil {
		return fmt.Errorf("preserve file flags: %w", err)
	}

	var sourceAfter, destinationAfter unix.Stat_t
	if err := unix.Fstat(sourceFD, &sourceAfter); err != nil {
		return fmt.Errorf("recheck existing file metadata: %w", err)
	}
	if err := unix.Fstat(destinationFD, &destinationAfter); err != nil {
		return fmt.Errorf("verify replacement metadata: %w", err)
	}
	if !sameDarwinSecurityMetadata(before, sourceAfter) {
		return fmt.Errorf("existing file metadata changed while it was being preserved")
	}
	if before.Uid != destinationAfter.Uid || before.Gid != destinationAfter.Gid ||
		(uint32(before.Mode)&0o7777) != (uint32(destinationAfter.Mode)&0o7777) ||
		before.Flags != destinationAfter.Flags {
		return fmt.Errorf("replacement ownership, permissions, or flags do not match the existing file")
	}
	return nil
}

func sameDarwinSecurityMetadata(left, right unix.Stat_t) bool {
	return left.Uid == right.Uid && left.Gid == right.Gid &&
		(uint32(left.Mode)&0o7777) == (uint32(right.Mode)&0o7777) &&
		left.Flags == right.Flags && left.Ctim == right.Ctim
}
