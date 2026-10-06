package contentdecode

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

// A reader that cannot get workspace waits for another reader to finish
// instead of failing a valid response, and the wait is reported.
func TestReaderAdmissionWaitsForReleasedWorkspace(t *testing.T) {
	body := encoded(t, []byte("sample"), "gzip")
	budget := NewMemoryBudget(chunkBytes + flateWorkspaceBytes)
	limits := DefaultLimits(1024)
	limits.MemoryBudget = budget
	first, err := NewReader(context.Background(), io.NopCloser(bytes.NewReader(body)), []string{"gzip"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	one := make([]byte, 1)
	if _, err := first.Read(one); err != nil {
		t.Fatal(err)
	}
	second, err := NewReader(context.Background(), io.NopCloser(bytes.NewReader(body)), []string{"gzip"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, second)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("second reader did not wait for workspace: %v", err)
	default:
	}
	_ = first.Close()
	if err := <-done; err != nil || !second.Complete() {
		t.Fatalf("waiting reader failed after release: %v", err)
	}
	if second.AdmissionWait() <= 0 {
		t.Fatal("admission wait was not reported")
	}
	_ = second.Close()
	stats := budget.Stats()
	if stats.UsedBytes != 0 || stats.Waits != 1 || stats.Rejections != 0 || stats.PeakBytes != chunkBytes+flateWorkspaceBytes {
		t.Fatalf("stats=%+v", stats)
	}
}

// Waiting is bounded by AdmissionWait and by the request context; neither
// leaks workspace.
func TestReaderAdmissionWaitIsBounded(t *testing.T) {
	body := encoded(t, []byte("sample"), "gzip")
	for _, tc := range []struct {
		name   string
		wait   time.Duration
		cancel bool
		want   ErrorKind
	}{
		{"timeout", 30 * time.Millisecond, false, MemoryLimit},
		{"canceled", time.Minute, true, Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := NewMemoryBudget(chunkBytes + flateWorkspaceBytes)
			limits := DefaultLimits(1024)
			limits.MemoryBudget = budget
			limits.AdmissionWait = tc.wait
			holder, _ := NewReader(context.Background(), io.NopCloser(bytes.NewReader(body)), []string{"gzip"}, limits)
			if _, err := holder.Read(make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if tc.cancel {
				time.AfterFunc(30*time.Millisecond, cancel)
			}
			defer cancel()
			waiter, _ := NewReader(ctx, io.NopCloser(bytes.NewReader(body)), []string{"gzip"}, limits)
			if _, err := waiter.Read(make([]byte, 1)); Kind(err) != tc.want {
				t.Fatalf("bounded admission error=%v want %s", err, tc.want)
			}
			_ = waiter.Close()
			_ = holder.Close()
			if budget.Used() != 0 {
				t.Fatalf("bounded admission leaked %d bytes", budget.Used())
			}
		})
	}
}

// Brotli admission assumes the largest window and returns the excess once the
// stream header names its real window.
func TestBrotliAdmissionShrinksToHeaderWindow(t *testing.T) {
	body := encoded(t, bytes.Repeat([]byte("sample"), 1000), "br")
	budget := NewMemoryBudget(MaxMemoryBytes)
	limits := DefaultLimits(1 << 20)
	limits.MemoryBudget = budget
	reader, _ := NewReader(context.Background(), io.NopCloser(bytes.NewReader(body)), []string{"br"}, limits)
	if _, err := reader.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	window, _ := brotliWindow(body[0])
	if want := chunkBytes + int64(window) + brotliOverheadBytes; budget.Used() != want {
		t.Fatalf("brotli workspace=%d want %d", budget.Used(), want)
	}
	_ = reader.Close()
	if budget.Used() != 0 {
		t.Fatal("brotli workspace leaked")
	}
}

// Close from another goroutine ends a Read that is waiting for workspace
// (and holding the read lock) instead of stalling for the whole wait.
func TestReaderCloseEndsAdmissionWait(t *testing.T) {
	body := encoded(t, []byte("sample"), "gzip")
	budget := NewMemoryBudget(chunkBytes + flateWorkspaceBytes)
	limits := DefaultLimits(1024)
	limits.MemoryBudget = budget
	limits.AdmissionWait = 30 * time.Second
	holder, err := NewReader(context.Background(), io.NopCloser(bytes.NewReader(body)), []string{"gzip"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	waiter, err := NewReader(context.Background(), io.NopCloser(bytes.NewReader(body)), []string{"gzip"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := waiter.Read(make([]byte, 1))
		readDone <- err
	}()
	for budget.Stats().Waits == 0 {
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() {
		_ = waiter.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close stalled behind the admission wait")
	}
	if err := <-readDone; Kind(err) != Canceled {
		t.Fatalf("waiting Read error = %v, want Canceled", err)
	}
	if used := budget.Used(); used != chunkBytes+flateWorkspaceBytes {
		t.Fatalf("budget used = %d, want only the holder's workspace", used)
	}
}
