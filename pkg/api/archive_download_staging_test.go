package api

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func configureArchiveDownloadStagingTest(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv(envArchiveDownloadStagingDir, root)
	t.Setenv(envArchiveDownloadMaxBytes, "1048576")
	t.Setenv(envArchiveDownloadMinFreeBytes, "1")
	t.Setenv(envArchiveDownloadMaxConcurrent, "1")
	return root
}

func assertArchiveDownloadStagingEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("archive download staging leaked %d entries: %+v", len(entries), entries)
	}
}

func TestStageArchiveDownloadEnforcesByteAndFreeSpaceLimits(t *testing.T) {
	t.Run("byte cap", func(t *testing.T) {
		root := configureArchiveDownloadStagingTest(t)
		t.Setenv(envArchiveDownloadMaxBytes, "32")
		staged, err := stageArchiveDownloadWithLimiter(context.Background(), newArchiveDownloadLimiter(), func(destination io.Writer) error {
			_, err := destination.Write(make([]byte, 64))
			return err
		})
		if staged != nil {
			removeStagedArchiveDownload(staged)
		}
		if err == nil || !strings.Contains(err.Error(), "byte limit 32") {
			t.Fatalf("stage error = %v, want byte limit", err)
		}
		assertArchiveDownloadStagingEmpty(t, root)
	})

	t.Run("free-space reserve", func(t *testing.T) {
		root := configureArchiveDownloadStagingTest(t)
		t.Setenv(envArchiveDownloadMinFreeBytes, "9223372036854775807")
		staged, err := stageArchiveDownloadWithLimiter(context.Background(), newArchiveDownloadLimiter(), func(destination io.Writer) error {
			_, err := destination.Write([]byte("bounded"))
			return err
		})
		if staged != nil {
			removeStagedArchiveDownload(staged)
		}
		if err == nil || !strings.Contains(err.Error(), "free space") {
			t.Fatalf("stage error = %v, want free-space reserve", err)
		}
		assertArchiveDownloadStagingEmpty(t, root)
	})
}

func TestStageArchiveDownloadHonorsCancellationAndCleansPartialFile(t *testing.T) {
	root := configureArchiveDownloadStagingTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	staged, err := stageArchiveDownloadWithLimiter(ctx, newArchiveDownloadLimiter(), func(destination io.Writer) error {
		if _, err := destination.Write([]byte("partial")); err != nil {
			return err
		}
		cancel()
		_, err := destination.Write([]byte("after-cancel"))
		return err
	})
	if staged != nil {
		removeStagedArchiveDownload(staged)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stage error = %v, want context cancellation", err)
	}
	assertArchiveDownloadStagingEmpty(t, root)
}

func TestStageArchiveDownloadConcurrencyLeaseCoversStagedFileLifetime(t *testing.T) {
	root := configureArchiveDownloadStagingTest(t)
	limiter := newArchiveDownloadLimiter()
	first, err := stageArchiveDownloadWithLimiter(context.Background(), limiter, func(destination io.Writer) error {
		_, err := destination.Write([]byte("first"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer removeStagedArchiveDownload(first)

	secondWriterCalled := false
	waitCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	second, err := stageArchiveDownloadWithLimiter(waitCtx, limiter, func(destination io.Writer) error {
		secondWriterCalled = true
		return nil
	})
	if second != nil {
		removeStagedArchiveDownload(second)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second stage error = %v, want concurrency wait deadline", err)
	}
	if secondWriterCalled {
		t.Fatal("second download entered staging while the first staged file was still leased")
	}

	removeStagedArchiveDownload(first)
	assertArchiveDownloadStagingEmpty(t, root)
}
