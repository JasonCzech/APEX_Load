package http

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestResponseCaptureBudgetRejectsAggregateGrowthAndReleases(t *testing.T) {
	budget := &responseCaptureBudget{limit: 8}
	lease := budget.lease()
	var buffer bytes.Buffer
	writer := &responseCaptureWriter{buffer: &buffer, lease: lease}
	if _, err := writer.Write([]byte("12345678")); err != nil {
		t.Fatalf("write within budget: %v", err)
	}
	if _, err := writer.Write([]byte("9")); err == nil || !strings.Contains(err.Error(), "memory budget") {
		t.Fatalf("write beyond budget error = %v", err)
	}
	if got := budget.used.Load(); got != 8 {
		t.Fatalf("used bytes = %d, want 8", got)
	}
	lease.release()
	if got := budget.used.Load(); got != 0 {
		t.Fatalf("used bytes after release = %d, want 0", got)
	}

	next := budget.lease()
	if !next.reserve(8) {
		t.Fatal("released budget was not reusable")
	}
	next.release()
}

func TestReadAllLimitedAccountsDecodedBytes(t *testing.T) {
	budget := &responseCaptureBudget{limit: 4}
	lease := budget.lease()
	if _, err := readAllLimitedWithLease(strings.NewReader("12345"), 16, lease); err == nil || !strings.Contains(err.Error(), "memory budget") {
		t.Fatalf("decoded aggregate budget error = %v", err)
	}
	lease.release()
	if got := budget.used.Load(); got != 0 {
		t.Fatalf("decoded reservation leaked %d bytes", got)
	}
}

func TestResponseCaptureReaderAccountsBeforeParserRetention(t *testing.T) {
	budget := &responseCaptureBudget{limit: 4}
	lease := budget.lease()
	reader := &responseCaptureReader{reader: strings.NewReader("12345"), lease: lease}
	if _, err := io.ReadAll(reader); err == nil || !strings.Contains(err.Error(), "memory budget") {
		t.Fatalf("aggregate reader budget error = %v", err)
	}
	lease.release()
	if got := budget.used.Load(); got != 0 {
		t.Fatalf("reader reservation leaked %d bytes", got)
	}
}
