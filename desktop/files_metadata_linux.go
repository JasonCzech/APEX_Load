//go:build linux

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const maxReplacementMetadataBytes = 16 << 20

const (
	linuxFSImmutableFlag = 0x00000010
	linuxFSAppendFlag    = 0x00000020
	linuxFSNoDumpFlag    = 0x00000040
	linuxFSVerityFlag    = 0x00100000
	linuxFSNoCOWFlag     = 0x00800000
	linuxFSDAXFlag       = 0x02000000
	linuxFSProjectFlag   = 0x20000000
	linuxFSCasefoldFlag  = 0x40000000

	// These masks are the stable Linux FS_IOC_GETFLAGS userspace contract.
	// Structural flags (such as EXTENTS and INLINE_DATA) describe filesystem
	// implementation details and must not be copied to a new inode.
	linuxFSUserVisibleFlags    = 0x0003dfff
	linuxFSUserModifiableFlags = 0x000380ff
	linuxFSRelevantFlags       = linuxFSUserVisibleFlags | linuxFSVerityFlag | linuxFSNoCOWFlag | linuxFSDAXFlag | linuxFSProjectFlag | linuxFSCasefoldFlag
)

// Linux applies the requested 0600 mode to an inherited default POSIX ACL's
// effective mask at inode creation. Existing-destination metadata is replaced
// descriptor-to-descriptor before publication, so a fresh staging inode needs
// no additional mutation here. In particular, do not strip a filesystem's
// automatically assigned SELinux label.
func prepareNewUnixSaveStagingMetadata(_ int) error {
	return nil
}

// resetUnixSaveTombstoneMetadata removes metadata that belongs to the
// displaced destination rather than to a private atomic-save staging inode.
// POSIX ACLs, file capabilities, SELinux labels, and user attributes are all
// represented as xattrs on Linux, so retaining any of them would either carry
// stale data into a later new-file save or reintroduce access beyond mode 0600.
func resetUnixSaveTombstoneMetadata(fd int) error {
	flags, flagsSupported, err := readLinuxFileFlags(fd)
	if err != nil {
		return fmt.Errorf("read file flags: %w", err)
	}
	if flagsSupported {
		if flags&(linuxFSImmutableFlag|linuxFSAppendFlag|linuxFSVerityFlag) != 0 {
			return fmt.Errorf("file has immutable, append-only, or verity flags")
		}
		desiredFlags := flags &^ linuxFSUserModifiableFlags
		if desiredFlags != flags {
			if err := unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, desiredFlags); err != nil {
				return fmt.Errorf("clear user-modifiable file flags: %w", err)
			}
		}
	}

	attributes, supported, err := readLinuxFDXattrs(fd)
	if err != nil {
		return fmt.Errorf("read extended attributes: %w", err)
	}
	if supported {
		for name := range attributes {
			if err := unix.Fremovexattr(fd, name); err != nil {
				return fmt.Errorf("remove extended attribute %q: %w", name, err)
			}
		}
	}
	return unixSaveTombstoneMetadataClean(fd)
}

func unixSaveTombstoneMetadataClean(fd int) error {
	flags, flagsSupported, err := readLinuxFileFlags(fd)
	if err != nil {
		return fmt.Errorf("read file flags: %w", err)
	}
	if flagsSupported && flags&linuxFSUserModifiableFlags != 0 {
		return fmt.Errorf("file retains user-modifiable inode flags")
	}
	attributes, supported, err := readLinuxFDXattrs(fd)
	if err != nil {
		return fmt.Errorf("read extended attributes: %w", err)
	}
	if supported && len(attributes) != 0 {
		return fmt.Errorf("file retains extended attributes")
	}
	return nil
}

// Linux exposes POSIX ACLs, file capabilities, SELinux labels, and ordinary
// attributes through the xattr interface; inode security flags are copied or
// explicitly checked through FS_IOC_GETFLAGS. Any uncopyable label or flag
// aborts the replacement rather than weakening the saved file.
func preserveReplacementMetadata(sourceFD, destinationFD int, _ os.FileInfo) error {
	var before unix.Stat_t
	if err := unix.Fstat(sourceFD, &before); err != nil {
		return fmt.Errorf("inspect existing file metadata: %w", err)
	}
	var destinationBefore unix.Stat_t
	if err := unix.Fstat(destinationFD, &destinationBefore); err != nil {
		return fmt.Errorf("inspect replacement metadata: %w", err)
	}
	if before.Uid != destinationBefore.Uid || before.Gid != destinationBefore.Gid {
		if err := unix.Fchown(destinationFD, int(before.Uid), int(before.Gid)); err != nil {
			return fmt.Errorf("preserve file ownership: %w", err)
		}
	}
	if err := unix.Fchmod(destinationFD, before.Mode&0o7777); err != nil {
		return fmt.Errorf("preserve file permissions: %w", err)
	}
	sourceFlags, flagsSupported, err := readLinuxFileFlags(sourceFD)
	if err != nil {
		return fmt.Errorf("read existing file flags: %w", err)
	}
	if flagsSupported {
		destinationFlags, destinationFlagsSupported, flagsErr := readLinuxFileFlags(destinationFD)
		if flagsErr != nil {
			return fmt.Errorf("read replacement file flags: %w", flagsErr)
		}
		if !destinationFlagsSupported {
			return fmt.Errorf("replacement filesystem does not support the existing file's inode flags")
		}
		if sourceFlags&(linuxFSImmutableFlag|linuxFSAppendFlag|linuxFSVerityFlag) != 0 {
			return fmt.Errorf("existing file has immutable, append-only, or verity flags that cannot be transferred atomically")
		}
		// Flags outside the kernel's user-modifiable mask (for example
		// encryption and no-COW state) must already match through directory
		// inheritance. Changing them after data is written would not preserve
		// their security or storage semantics.
		nonModifiable := linuxFSRelevantFlags &^ linuxFSUserModifiableFlags
		if sourceFlags&nonModifiable != destinationFlags&nonModifiable {
			return fmt.Errorf("replacement did not inherit the existing file's non-modifiable inode flags")
		}
		desiredFlags := (destinationFlags &^ linuxFSUserModifiableFlags) | (sourceFlags & linuxFSUserModifiableFlags)
		if desiredFlags != destinationFlags {
			if flagsErr := unix.IoctlSetPointerInt(destinationFD, unix.FS_IOC_SETFLAGS, desiredFlags); flagsErr != nil {
				return fmt.Errorf("preserve file flags: %w", flagsErr)
			}
		}
	}

	sourceAttributes, supported, err := readLinuxFDXattrs(sourceFD)
	if err != nil {
		return fmt.Errorf("read existing file extended attributes: %w", err)
	}
	if supported {
		destinationAttributes, destinationSupported, readErr := readLinuxFDXattrs(destinationFD)
		if readErr != nil {
			return fmt.Errorf("read replacement extended attributes: %w", readErr)
		}
		if !destinationSupported {
			return fmt.Errorf("replacement filesystem does not support the existing file's extended attributes")
		}
		for name := range destinationAttributes {
			if _, present := sourceAttributes[name]; present {
				continue
			}
			if removeErr := unix.Fremovexattr(destinationFD, name); removeErr != nil {
				return fmt.Errorf("remove inherited extended attribute %q: %w", name, removeErr)
			}
		}
		for name, value := range sourceAttributes {
			if inherited, present := destinationAttributes[name]; present && bytes.Equal(value, inherited) {
				continue
			}
			if setErr := unix.Fsetxattr(destinationFD, name, value, 0); setErr != nil {
				return fmt.Errorf("copy extended attribute %q: %w", name, setErr)
			}
		}
	}

	var sourceAfter, destinationAfter unix.Stat_t
	if err := unix.Fstat(sourceFD, &sourceAfter); err != nil {
		return fmt.Errorf("recheck existing file metadata: %w", err)
	}
	if err := unix.Fstat(destinationFD, &destinationAfter); err != nil {
		return fmt.Errorf("verify replacement metadata: %w", err)
	}
	if before.Uid != sourceAfter.Uid || before.Gid != sourceAfter.Gid ||
		(before.Mode&0o7777) != (sourceAfter.Mode&0o7777) || before.Ctim != sourceAfter.Ctim {
		return fmt.Errorf("existing file metadata changed while it was being preserved")
	}
	if before.Uid != destinationAfter.Uid || before.Gid != destinationAfter.Gid ||
		(before.Mode&0o7777) != (destinationAfter.Mode&0o7777) {
		return fmt.Errorf("replacement ownership or permissions do not match the existing file")
	}
	if flagsSupported {
		sourceAfterFlags, _, flagsErr := readLinuxFileFlags(sourceFD)
		if flagsErr != nil {
			return fmt.Errorf("recheck existing file flags: %w", flagsErr)
		}
		destinationAfterFlags, _, flagsErr := readLinuxFileFlags(destinationFD)
		if flagsErr != nil {
			return fmt.Errorf("verify replacement file flags: %w", flagsErr)
		}
		if sourceFlags&linuxFSRelevantFlags != sourceAfterFlags&linuxFSRelevantFlags {
			return fmt.Errorf("existing file flags changed while they were being preserved")
		}
		if sourceFlags&linuxFSRelevantFlags != destinationAfterFlags&linuxFSRelevantFlags {
			return fmt.Errorf("replacement inode flags do not match the existing file")
		}
	}
	if supported {
		sourceAfterAttributes, _, readErr := readLinuxFDXattrs(sourceFD)
		if readErr != nil {
			return fmt.Errorf("recheck existing file extended attributes: %w", readErr)
		}
		destinationAfterAttributes, _, readErr := readLinuxFDXattrs(destinationFD)
		if readErr != nil {
			return fmt.Errorf("verify replacement extended attributes: %w", readErr)
		}
		if !equalLinuxXattrs(sourceAttributes, sourceAfterAttributes) {
			return fmt.Errorf("existing file extended attributes changed while they were being preserved")
		}
		if !equalLinuxXattrs(sourceAttributes, destinationAfterAttributes) {
			return fmt.Errorf("replacement extended attributes do not match the existing file")
		}
	}
	return nil
}

func readLinuxFileFlags(fd int) (int, bool, error) {
	flags, err := unix.IoctlGetInt(fd, unix.FS_IOC_GETFLAGS)
	if errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) {
		return 0, false, nil
	}
	return flags, true, err
}

func readLinuxFDXattrs(fd int) (map[string][]byte, bool, error) {
	listSize, err := unix.Flistxattr(fd, nil)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		return map[string][]byte{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if listSize > maxReplacementMetadataBytes {
		return nil, true, fmt.Errorf("extended attribute name list exceeds %d bytes", maxReplacementMetadataBytes)
	}
	if listSize == 0 {
		return map[string][]byte{}, true, nil
	}
	list := make([]byte, listSize)
	read, err := unix.Flistxattr(fd, list)
	if err != nil {
		return nil, true, err
	}
	attributes := make(map[string][]byte)
	total := 0
	for _, name := range strings.Split(string(list[:read]), "\x00") {
		if name == "" {
			continue
		}
		value, valueErr := readLinuxFDXattr(fd, name)
		if valueErr != nil {
			return nil, true, fmt.Errorf("read %q: %w", name, valueErr)
		}
		total += len(name) + len(value)
		if total > maxReplacementMetadataBytes {
			return nil, true, fmt.Errorf("extended attributes exceed %d bytes", maxReplacementMetadataBytes)
		}
		attributes[name] = value
	}
	return attributes, true, nil
}

func readLinuxFDXattr(fd int, name string) ([]byte, error) {
	for attempt := 0; attempt < 3; attempt++ {
		size, err := unix.Fgetxattr(fd, name, nil)
		if err != nil {
			return nil, err
		}
		if size > maxReplacementMetadataBytes {
			return nil, fmt.Errorf("value exceeds %d bytes", maxReplacementMetadataBytes)
		}
		value := make([]byte, size)
		read, err := unix.Fgetxattr(fd, name, value)
		if errors.Is(err, unix.ERANGE) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return value[:read], nil
	}
	return nil, fmt.Errorf("value changed repeatedly while being read")
}

func equalLinuxXattrs(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for name, value := range left {
		other, ok := right[name]
		if !ok || !bytes.Equal(value, other) {
			return false
		}
	}
	return true
}
