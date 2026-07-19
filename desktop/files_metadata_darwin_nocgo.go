//go:build darwin && !cgo

package main

import (
	"fmt"
	"os"
)

func preserveReplacementMetadata(_, _ int, _ os.FileInfo) error {
	return fmt.Errorf("overwriting an existing file requires macOS ACL preservation support")
}

func prepareNewUnixSaveStagingMetadata(_ int) error {
	return nil
}

func resetUnixSaveTombstoneMetadata(_ int) error {
	return fmt.Errorf("scrubbing a recovery tombstone requires macOS ACL support")
}

func unixSaveTombstoneMetadataClean(_ int) error {
	return fmt.Errorf("reusing a recovery tombstone requires macOS ACL support")
}
