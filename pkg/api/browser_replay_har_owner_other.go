//go:build !darwin && !linux && !windows

package api

import (
	"errors"
	"os"
)

// ensurePrivateReplayHARRoot cannot prove ownership here, so the managed root
// is never adopted (native replay HARs need the browser's HAR ownership,
// which this platform does not provide).
func ensurePrivateReplayHARRoot(string) error {
	return errors.New("the managed replay HAR root is unavailable on this platform")
}

func privateReplayHARTempDir() (string, error) { return os.MkdirTemp("", "apexload-replay-har-") }
