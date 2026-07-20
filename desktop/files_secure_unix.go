//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const maxRetainedUnixSaveRecoveryFiles = 32

// secureWriteGrantedFile writes relative to a verified directory descriptor.
// The temporary file and final publication therefore stay in the directory the
// user actually selected even if an attacker retargets a symlink in the
// original pathname while the write is in progress.
func secureWriteGrantedFile(grant writePathGrant, data []byte) error {
	return secureWriteGrantedFileFrom(grant, bytes.NewReader(data), int64(len(data)))
}

func secureWriteGrantedFileFrom(grant writePathGrant, src io.Reader, maxBytes int64) error {
	if src == nil {
		return fmt.Errorf("saved file source is required")
	}
	dirFD, err := unix.Open(grant.canonicalParent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open granted save directory: %w", err)
	}
	dir := os.NewFile(uintptr(dirFD), grant.canonicalParent)
	if dir == nil {
		_ = unix.Close(dirFD)
		return fmt.Errorf("open granted save directory: invalid descriptor")
	}
	defer dir.Close()
	parentInfo, err := dir.Stat()
	if err != nil {
		return fmt.Errorf("verify granted save directory: %w", err)
	}
	if !os.SameFile(grant.parentIdentity, parentInfo) {
		return fmt.Errorf("refusing to write because the selected save directory was replaced")
	}
	tempName, tempFD, err := acquireTempAt(dirFD, dir)
	if err != nil {
		return err
	}
	// Never delete this pathname by name. A same-user process can replace a
	// directory entry between an identity check and unlinkat, which would turn
	// best-effort cleanup into deletion of an unrelated file. Successful native
	// renames consume the name. Existing-file exchange intentionally retains the
	// displaced version as a private recovery file; failed saves retain their
	// owner-only staging file for the same fail-closed reason.
	temp := os.NewFile(uintptr(tempFD), tempName)
	if temp == nil {
		_ = unix.Close(tempFD)
		return fmt.Errorf("create atomic save file: invalid descriptor")
	}
	tempClosed := false
	stagingPublishedOrUncertain := false
	defer func() {
		if !tempClosed {
			if !stagingPublishedOrUncertain {
				// Scrub through the still-open descriptor. Never unlink the
				// pathname: another process may have replaced that directory entry.
				_ = temp.Truncate(0)
				_ = temp.Sync()
			}
			_ = temp.Close()
		}
	}()
	targetMode := os.FileMode(0o600)
	if grant.targetExisted && grant.targetIdentity != nil {
		targetMode = grant.targetIdentity.Mode().Perm()
	}
	if err := writeTempContents(temp, src, maxBytes, targetMode); err != nil {
		return err
	}
	stagingIdentity, err := temp.Stat()
	if err != nil {
		return fmt.Errorf("identify saved file staging inode: %w", err)
	}

	if grant.targetExisted {
		// O_NONBLOCK is essential here: an attacker must not be able to swap a
		// previously regular destination for a FIFO and indefinitely stall the
		// native save before the descriptor type check below.
		targetFD, openErr := unix.Openat(dirFD, grant.name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			return fmt.Errorf("verify existing save destination: %w", openErr)
		}
		target := os.NewFile(uintptr(targetFD), grant.name)
		if target == nil {
			_ = unix.Close(targetFD)
			return fmt.Errorf("verify existing save destination: invalid descriptor")
		}
		targetInfo, statErr := target.Stat()
		if statErr == nil && (!targetInfo.Mode().IsRegular() || !os.SameFile(grant.targetIdentity, targetInfo)) {
			statErr = fmt.Errorf("selected save destination was replaced")
		}
		if statErr != nil {
			_ = target.Close()
			return fmt.Errorf("refusing to overwrite: %w", statErr)
		}
		// Preserve ownership, ACLs, extended attributes, and other
		// platform security metadata on the staging inode. Publication is
		// refused when the platform cannot preserve that metadata safely.
		if metadataErr := preserveReplacementMetadata(targetFD, tempFD, targetInfo); metadataErr != nil {
			_ = target.Close()
			return fmt.Errorf("preserve existing save destination metadata: %w", metadataErr)
		}
		if closeErr := target.Close(); closeErr != nil {
			return fmt.Errorf("close existing save destination: %w", closeErr)
		}
	}

	if err := syncTemp(temp); err != nil {
		return err
	}

	if grant.targetExisted {
		published, err := publishExistingFileIfMatches(dirFD, tempName, grant.name, grant.targetIdentity, stagingIdentity)
		stagingPublishedOrUncertain = published
		if err != nil {
			return fmt.Errorf("publish saved file: %w", err)
		}
	} else {
		// Platform-native exclusive rename supports filesystems that do not
		// implement hard links (for example FAT-family removable media) while
		// still refusing to replace a destination created after the save grant.
		// Publication fails closed on kernels/filesystems that do not implement
		// the native primitive. A hard-link fallback would require unlinking the
		// staging pathname after publication, and POSIX has no conditional unlink
		// that can prove the directory entry still names our inode.
		published, err := publishNewFileNoReplace(dirFD, tempName, grant.name, stagingIdentity)
		stagingPublishedOrUncertain = published
		if err != nil {
			return fmt.Errorf("publish new saved file without clobbering: %w", err)
		}
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close published saved file: %w", err)
	}
	tempClosed = true
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync saved file directory: %w", err)
	}
	return nil
}

// publishExistingFileIfMatches atomically exchanges the staged file with the
// current destination, then verifies that the displaced inode is exactly the
// file selected by the save dialog. This turns the publication into a
// compare-and-swap: a destination replaced after the earlier descriptor check
// is atomically restored and the save fails closed.
//
// preserveTemp is true only when tempName may contain a displaced destination
// that cleanup must retain for recovery.
type renameExchangeFunc func(oldDirFD int, oldName string, newDirFD int, newName string) error

func publishExistingFileIfMatches(dirFD int, tempName, targetName string, expected, stagingIdentity os.FileInfo) (preserveTemp bool, err error) {
	return publishExistingFileIfMatchesWith(dirFD, tempName, targetName, expected, stagingIdentity, nativeRenameExchange)
}

func publishExistingFileIfMatchesWith(
	dirFD int,
	tempName, targetName string,
	expected, stagingIdentity os.FileInfo,
	exchange renameExchangeFunc,
) (preserveTemp bool, err error) {
	if expected == nil {
		return false, fmt.Errorf("existing save destination identity is required")
	}
	if stagingIdentity == nil {
		return false, fmt.Errorf("saved file staging identity is required")
	}
	if exchange == nil {
		return false, fmt.Errorf("atomic destination exchange is required")
	}
	if err := exchange(dirFD, tempName, dirFD, targetName); err != nil {
		return false, fmt.Errorf("atomic destination exchange: %w", err)
	}

	displacedErr := verifyRegularFileIdentityAt(dirFD, tempName, expected)
	publishedErr := verifyRegularFileIdentityAt(dirFD, targetName, stagingIdentity)
	if displacedErr != nil || publishedErr != nil {
		if rollbackErr := exchange(dirFD, tempName, dirFD, targetName); rollbackErr != nil {
			return true, fmt.Errorf(
				"save source or destination changed during publication (destination=%v, source=%v); atomic rollback failed and the displaced file was retained as %q: %w",
				displacedErr,
				publishedErr,
				tempName,
				rollbackErr,
			)
		}
		// A second racer can replace targetName between the first inspection and
		// the rollback exchange. Only allow caller cleanup when the private name
		// is proven to contain our staging inode again; otherwise retain it so an
		// unrelated file is never deleted by the deferred cleanup.
		if stagingErr := verifyRegularFileIdentityAt(dirFD, tempName, stagingIdentity); stagingErr != nil {
			return true, fmt.Errorf(
				"save source or destination changed during publication and rollback left an unverified file retained as %q: %w",
				tempName,
				stagingErr,
			)
		}
		return false, fmt.Errorf("save source or destination changed during publication (destination=%v, source=%v)", displacedErr, publishedErr)
	}

	// The private staging name now refers to the verified old destination.
	// Scrub that exact inode through a descriptor, but retain its zero-length
	// directory entry as a bounded recovery tombstone: POSIX unlinkat is
	// pathname-only, so deleting it after verification would reintroduce a
	// check/delete race.
	if scrubErr := scrubRegularFileIdentityAt(dirFD, tempName, expected); scrubErr != nil {
		return true, fmt.Errorf("saved file was published but the prior version was retained as %q because it could not be descriptor-scrubbed: %w", tempName, scrubErr)
	}
	return true, nil
}

// acquireTempAt reuses a verified scrubbed tombstone before allocating another
// directory entry. Existing-file publication necessarily leaves the displaced
// inode under the staging name because POSIX has no identity-conditional
// unlink. Reusing that same private, empty, single-link inode on the next save
// keeps successful overwrites at a constant number of tombstones without ever
// deleting a path that a same-user process could race-replace.
func acquireTempAt(dirFD int, dir *os.File) (string, int, error) {
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return "", -1, fmt.Errorf("inspect retained atomic-save recovery files: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if isUnixSaveRecoveryName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		fd, openErr := unix.Openat(dirFD, name, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			continue
		}
		if lockErr := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); lockErr != nil {
			_ = unix.Close(fd)
			continue
		}
		file := os.NewFile(uintptr(fd), name)
		if file == nil {
			_ = unix.Close(fd)
			continue
		}
		info, statErr := file.Stat()
		var stat unix.Stat_t
		fstatErr := unix.Fstat(fd, &stat)
		reusable := statErr == nil && fstatErr == nil && info.Mode().IsRegular() && info.Size() == 0 &&
			unixSaveFileModeIsPrivateRegular(stat.Mode) && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
		if reusable {
			reusable = verifyRegularFileIdentityAt(dirFD, name, info) == nil
		}
		if reusable {
			reusable = unixSaveTombstoneMetadataClean(fd) == nil
		}
		if reusable {
			if _, seekErr := file.Seek(0, io.SeekStart); seekErr == nil {
				return name, fd, nil
			}
		}
		_ = file.Close()
	}
	if len(names) >= maxRetainedUnixSaveRecoveryFiles {
		return "", -1, fmt.Errorf(
			"refusing to create another atomic-save staging file because %d non-reusable recovery files already exist in the selected directory; inspect and remove .apexload-save-*.tmp files before retrying",
			len(names),
		)
	}
	return createTempAt(dirFD)
}

func isUnixSaveRecoveryName(name string) bool {
	const prefix = ".apexload-save-"
	const suffix = ".tmp"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	hexPart := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if len(hexPart) != 32 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}

func scrubRegularFileIdentityAt(dirFD int, name string, expected os.FileInfo) error {
	if expected == nil {
		return fmt.Errorf("expected file identity is required")
	}
	fd, err := unix.Openat(
		dirFD,
		name,
		unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("invalid file descriptor")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		return fmt.Errorf("file identity does not match")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	// Truncating this inode would also destroy content reachable through every
	// other hard link. It is likewise unsafe to scrub a displaced file owned by
	// another user: the save may have been authorized by directory permissions,
	// but that does not authorize destruction of the prior inode.
	if stat.Nlink != 1 {
		return fmt.Errorf("refusing to scrub a displaced file with multiple hard links")
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("refusing to scrub a displaced file owned by another user")
	}
	if err := resetUnixSaveTombstoneMetadata(fd); err != nil {
		return fmt.Errorf("clear displaced file security metadata: %w", err)
	}
	if err := unix.Fchown(fd, os.Geteuid(), os.Getegid()); err != nil {
		return fmt.Errorf("make recovery tombstone owner-only: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if !unixSaveFileModeIsPrivateRegular(stat.Mode) || stat.Uid != uint32(os.Geteuid()) ||
		stat.Nlink != 1 || stat.Size != 0 {
		return fmt.Errorf("recovery tombstone metadata did not normalize safely")
	}
	if err := unixSaveTombstoneMetadataClean(fd); err != nil {
		return fmt.Errorf("verify cleared recovery tombstone metadata: %w", err)
	}
	return nil
}

func unixSaveFileModeIsPrivateRegular[T ~uint16 | ~uint32](mode T) bool {
	normalized := uint32(mode)
	return normalized&(uint32(unix.S_IFMT)|0o7777) == uint32(unix.S_IFREG)|0o600
}

func verifyRegularFileIdentityAt(dirFD int, name string, expected os.FileInfo) error {
	if expected == nil {
		return fmt.Errorf("expected file identity is required")
	}
	fd, err := unix.Openat(
		dirFD,
		name,
		unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("invalid file descriptor")
	}
	info, statErr := file.Stat()
	if closeErr := file.Close(); statErr == nil && closeErr != nil {
		statErr = closeErr
	}
	if statErr != nil {
		return statErr
	}
	if !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		return fmt.Errorf("file identity does not match")
	}
	return nil
}

type renameNoReplaceFunc func(oldDirFD int, oldName string, newDirFD int, newName string) error

func publishNewFileNoReplace(dirFD int, tempName, targetName string, stagingIdentity os.FileInfo) (bool, error) {
	return publishNewFileNoReplaceWith(dirFD, tempName, targetName, stagingIdentity, nativeRenameNoReplace)
}

// publishNewFileNoReplaceWith atomically moves a staging file to a previously
// absent destination. No check/rename or hard-link/unlink fallback is safe:
// POSIX has no identity-conditional pathname deletion, so unsupported native
// publication must fail closed and retain the private staging file.
func publishNewFileNoReplaceWith(
	dirFD int,
	tempName, targetName string,
	stagingIdentity os.FileInfo,
	renameNoReplace renameNoReplaceFunc,
) (bool, error) {
	if stagingIdentity == nil {
		return false, fmt.Errorf("saved file staging identity is required")
	}
	renameErr := renameNoReplace(dirFD, tempName, dirFD, targetName)
	if renameErr != nil {
		return false, fmt.Errorf("atomic no-replace rename: %w", renameErr)
	}
	if err := verifyRegularFileIdentityAt(dirFD, targetName, stagingIdentity); err != nil {
		// The selected destination did not exist, so there is no prior user file
		// to restore. Preserve every directory entry and fail rather than deleting
		// an unverified file that raced into either pathname.
		return false, fmt.Errorf("published file identity does not match the staged save; files were retained for recovery: %w", err)
	}
	return true, nil
}

func noReplaceRenameUnsupported(err error) bool {
	return errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.EOPNOTSUPP)
}

func createTempAt(dirFD int) (string, int, error) {
	for attempt := 0; attempt < 128; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", -1, fmt.Errorf("generate atomic save filename: %w", err)
		}
		name := ".apexload-save-" + hex.EncodeToString(random[:]) + ".tmp"
		fd, err := unix.Openat(dirFD, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
		if err == nil {
			if lockErr := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); lockErr != nil {
				_ = unix.Close(fd)
				return "", -1, fmt.Errorf("lock atomic save file: %w", lockErr)
			}
			// O_EXCL establishes a fresh inode, but directory-default ACLs,
			// extended attributes, and flags can still be inherited. Clear them
			// before any save payload is written to this descriptor.
			if metadataErr := prepareNewUnixSaveStagingMetadata(fd); metadataErr != nil {
				_ = unix.Close(fd)
				return "", -1, fmt.Errorf("secure atomic save file metadata (empty staging file retained as %q): %w", name, metadataErr)
			}
			return name, fd, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return "", -1, fmt.Errorf("create atomic save file: %w", err)
		}
	}
	return "", -1, fmt.Errorf("create atomic save file: too many name collisions")
}

func writeTempContents(temp *os.File, src io.Reader, maxBytes int64, mode os.FileMode) error {
	reader := src
	if maxBytes > 0 && maxBytes < math.MaxInt64 {
		reader = io.LimitReader(src, maxBytes+1)
	}
	written, err := io.Copy(temp, reader)
	if err != nil {
		return fmt.Errorf("write saved file: %w", err)
	}
	if maxBytes > 0 && written > maxBytes {
		return fmt.Errorf("saved file exceeds the %d-byte limit", maxBytes)
	}
	if err := temp.Chmod(mode.Perm()); err != nil {
		return fmt.Errorf("set saved file permissions: %w", err)
	}
	return nil
}

func syncTemp(temp *os.File) error {
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync saved file: %w", err)
	}
	return nil
}
