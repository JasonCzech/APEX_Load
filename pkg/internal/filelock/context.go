package filelock

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// LockContext exclusively locks an already-open regular file. The caller owns
// and must keep the file open until release. This lets security-sensitive callers
// open through a validated directory descriptor instead of following a new path.
// Cancellation bounds waiting; it does not release a successfully acquired lock.
func LockContext(ctx context.Context, file *os.File) (func() error, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if file == nil {
		return nil, fmt.Errorf("lock file is required")
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("lock file must be regular")
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		unlock, acquired, err := tryLockFile(file)
		if err != nil {
			return nil, err
		}
		if acquired {
			if err := ctx.Err(); err != nil {
				_ = unlock()
				return nil, err
			}
			var once sync.Once
			var releaseErr error
			return func() error { once.Do(func() { releaseErr = unlock() }); return releaseErr }, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
