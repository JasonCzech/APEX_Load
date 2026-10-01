package api

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// Jobs have a 15-minute deadline. A one-hour grace also covers cancellation and
// cleanup, including another replica using the same staging volume. Only our
// cache directories are eligible; immutable source artifacts live elsewhere.
func reclaimAnalysisScratch(path string, now time.Time) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	for visited := 0; visited < 1000; {
		entries, err := dir.ReadDir(100)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, entry := range entries {
			visited++
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "job-") {
				continue
			}
			info, err := entry.Info()
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if now.Sub(info.ModTime()) > time.Hour {
				if err = root.RemoveAll(entry.Name()); err != nil {
					return err
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return nil
}
