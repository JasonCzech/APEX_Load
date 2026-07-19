package main

import (
	"errors"
	"io"
	"runtime"
	"time"
)

const desktopProcessLockFile = ".desktop-instance.lock"

var errDesktopProcessLockHeld = errors.New("desktop process lock is already held")

const desktopProcessLockRetryInterval = 25 * time.Millisecond

type desktopProcessLock interface {
	io.Closer
}

type desktopProcessLockAcquirer func(string) (desktopProcessLock, error)

// desktopProcessLockStartupWait covers a Linux-only ordering gap in Wails:
// OnStartup can begin before its D-Bus single-instance name is claimed. During
// simultaneous launches, the process that will shortly exit as the secondary
// instance can therefore hold our storage lock for a brief period. Other Wails
// backends claim their single-instance primitive before OnStartup.
func desktopProcessLockStartupWait() time.Duration {
	if runtime.GOOS == "linux" {
		return 2 * time.Second
	}
	return 0
}

func acquireDesktopProcessLockWithRetry(
	appDataPath string,
	maxWait time.Duration,
	interval time.Duration,
	acquire desktopProcessLockAcquirer,
) (desktopProcessLock, error) {
	if interval <= 0 {
		interval = desktopProcessLockRetryInterval
	}
	deadline := time.Now().Add(maxWait)
	for {
		lock, err := acquire(appDataPath)
		if err == nil {
			return lock, nil
		}
		if maxWait <= 0 || !errors.Is(err, errDesktopProcessLockHeld) {
			return nil, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, err
		}
		if interval > remaining {
			interval = remaining
		}
		timer := time.NewTimer(interval)
		<-timer.C
	}
}
