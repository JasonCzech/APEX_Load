package filelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

var ErrReleaseUnproven = errors.New("file lock release outcome remains unproven")

// LockContext exclusively locks an already-open regular file. The caller owns
// and must keep the file open until release. This lets security-sensitive callers
// open through a validated directory descriptor instead of following a new path.
// Cancellation bounds waiting; it does not release a successfully acquired lock.
func LockContext(ctx context.Context, file *os.File) (func() error, error) {
	release, err := LockContextOwned(ctx, file)
	if err != nil && release != nil {
		// Compatibility callers have no returned release on failure. Preserve
		// the actual unlock error instead of silently treating cancellation as
		// evidence of release. Descriptor ownership stays with the caller.
		return nil, errors.Join(err, release())
	}
	return release, err
}

// LockContextOwned preserves a release callback even when authority expires
// just after acquisition. An exact descriptor owner must invoke that callback
// once and close its file; neither error return nor cancellation closes it.
func LockContextOwned(ctx context.Context, file *os.File) (func() error, error) {
	return lockContextOwned(ctx, file, tryLockFile)
}

type lockAttempt func(*os.File) (func() error, bool, error)

func lockContextOwned(ctx context.Context, file *os.File, attempt lockAttempt) (func() error, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if file == nil || attempt == nil {
		return nil, fmt.Errorf("lock file is required")
	}
	if err := lockContextAuthorityError(ctx); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("lock file must be regular")
	}
	for {
		if err := lockContextAuthorityError(ctx); err != nil {
			return nil, err
		}
		unlock, acquired, err := attempt(file)
		if err != nil {
			// A platform adapter can report acquisition and an error together;
			// do not lose its exact release authority in that ambiguous case.
			if acquired && unlock != nil {
				return onceLockRelease(unlock), err
			}
			return nil, err
		}
		if acquired {
			if unlock == nil {
				return nil, fmt.Errorf("acquired lock omitted release authority")
			}
			release := onceLockRelease(unlock)
			if err := lockContextAuthorityError(ctx); err != nil {
				return release, err
			}
			return release, nil
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

func onceLockRelease(unlock func() error) func() error {
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			defer func() {
				if recover() != nil {
					releaseErr = ErrReleaseUnproven
				}
			}()
			releaseErr = unlock()
		})
		return releaseErr
	}
}

func lockContextAuthorityError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !deadline.After(time.Now()) {
		return context.DeadlineExceeded
	}
	return nil
}
