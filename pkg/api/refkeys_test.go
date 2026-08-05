package api

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type failingReferenceSettingsStore struct {
	readErr   error
	saveCalls int
}

func (s *failingReferenceSettingsStore) GetSetting(string) (string, error) {
	return "", s.readErr
}

func (s *failingReferenceSettingsStore) SaveSetting(string, string) error {
	s.saveCalls++
	return nil
}

func TestRunReferenceSequenceAllocatorFailsClosedOnReadError(t *testing.T) {
	readErr := errors.New("settings unavailable")
	store := &failingReferenceSettingsStore{readErr: readErr}
	allocator := newRunReferenceSequenceAllocator("LOC", store)

	sequence, err := allocator.next(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC))
	if sequence != 0 || !errors.Is(err, readErr) || !strings.Contains(err.Error(), "load run reference sequence") {
		t.Fatalf("next = (%d, %v), want wrapped settings read error", sequence, err)
	}
	if store.saveCalls != 0 {
		t.Fatalf("SaveSetting calls = %d, want zero after failed read", store.saveCalls)
	}
}
