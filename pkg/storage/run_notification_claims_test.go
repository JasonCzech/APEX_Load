package storage

import (
	"testing"
	"time"
)

func TestRunNotificationIsClaimedOncePerRunAndKind(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/claims.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		run, kind string
		want      bool
	}{
		{"run-1", "telemetry_acceptance", true},
		{"run-1", "telemetry_acceptance", false},
		{"run-1", "other_kind", true},
		{"run-2", "telemetry_acceptance", true},
	} {
		claimed, err := store.ClaimRunNotification(tc.run, tc.kind, now)
		if err != nil || claimed != tc.want {
			t.Fatalf("claim %s/%s = %v %v, want %v", tc.run, tc.kind, claimed, err, tc.want)
		}
	}
}
