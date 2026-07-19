//go:build freebsd || openbsd || netbsd || dragonfly

package main

import (
	"fmt"
	"os"
)

// The BSDs use platform-specific ACL APIs that are not available through the
// portable runtime used by this desktop build. Existing files must not be
// atomically replaced when doing so would silently discard those ACLs.
func preserveReplacementMetadata(_, _ int, _ os.FileInfo) error {
	return fmt.Errorf("overwriting an existing file requires platform ACL preservation support")
}

// New files remain supported with their exclusive 0600 creation mode. Retained
// tombstones are never reused on these targets because the portable runtime
// cannot prove that platform ACL metadata is absent.
func prepareNewUnixSaveStagingMetadata(_ int) error {
	return nil
}

func resetUnixSaveTombstoneMetadata(_ int) error {
	return fmt.Errorf("scrubbing a recovery tombstone requires platform ACL support")
}

func unixSaveTombstoneMetadataClean(_ int) error {
	return fmt.Errorf("reusing a recovery tombstone requires platform ACL support")
}
