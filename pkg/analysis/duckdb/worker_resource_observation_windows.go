//go:build windows

package duckdb

import (
	"errors"

	"golang.org/x/sys/windows"
)

func workerPlatformProcessAbsent(err error) bool {
	// OpenProcess can return invalid-parameter for a gone PID. It remains only a
	// provisional absence observation; access denied/invalid handle/other errors
	// never become successful inspection or process-closure proof.
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
}
