package api

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestRunArchiveWriterFinalizeClosesAllFilesAfterFlushFailure(t *testing.T) {
	files := make([]*os.File, 3)
	for index := range files {
		file, err := os.CreateTemp(t.TempDir(), "archive-writer-*")
		if err != nil {
			t.Fatal(err)
		}
		files[index] = file
	}
	flushErr := errors.New("injected archive flush failure")
	writer := &runArchiveWriter{
		txFile: files[0], resourceFile: files[1], summaryFile: files[2],
		flushErr: flushErr,
	}
	if _, err := writer.Finalize("FAILED", time.Now().UTC(), "", false, nil); !errors.Is(err, flushErr) {
		t.Fatalf("Finalize error = %v, want injected flush failure", err)
	}
	assertArchiveWriterFilesClosed(t, files)
}

func TestRunArchiveWriterFinalizeClosesRemainingFilesAfterSyncFailure(t *testing.T) {
	files := make([]*os.File, 3)
	for index := range files {
		file, err := os.CreateTemp(t.TempDir(), "archive-writer-*")
		if err != nil {
			t.Fatal(err)
		}
		files[index] = file
	}
	if err := files[0].Close(); err != nil {
		t.Fatal(err)
	}
	writer := &runArchiveWriter{txFile: files[0], resourceFile: files[1], summaryFile: files[2]}
	if _, err := writer.Finalize("FAILED", time.Now().UTC(), "", false, nil); err == nil {
		t.Fatal("Finalize succeeded with an already-closed archive file")
	}
	assertArchiveWriterFilesClosed(t, files)
}

func assertArchiveWriterFilesClosed(t *testing.T, files []*os.File) {
	t.Helper()
	for _, file := range files {
		if _, err := file.Stat(); !errors.Is(err, os.ErrInvalid) && !errors.Is(err, os.ErrClosed) {
			t.Fatalf("archive file %q remains open; Stat error = %v", file.Name(), err)
		}
	}
}
