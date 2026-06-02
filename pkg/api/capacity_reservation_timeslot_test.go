package api

import (
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestReservationActiveAt(t *testing.T) {
	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		res  CapacityReservation
		want bool
	}{
		{"always active (no window)", CapacityReservation{}, true},
		{"within window", CapacityReservation{StartsAt: ptr(now.Add(-time.Hour)), EndsAt: ptr(now.Add(time.Hour))}, true},
		{"before window", CapacityReservation{StartsAt: ptr(now.Add(time.Hour))}, false},
		{"after window", CapacityReservation{EndsAt: ptr(now.Add(-time.Hour))}, false},
		{"open-ended start, within", CapacityReservation{EndsAt: ptr(now.Add(time.Hour))}, true},
		{"open-ended end, within", CapacityReservation{StartsAt: ptr(now.Add(-time.Hour))}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reservationActiveAt(tc.res, now); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestCapacityReservationTotalsAtRespectsWindow(t *testing.T) {
	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	settings := SystemSettings{}
	settings.CapacityPlanning.Reservations = []CapacityReservation{
		{ProjectID: "always", ReservedGenerators: 2}, // active
		{ProjectID: "active-window", ReservedGenerators: 3, StartsAt: ptr(now.Add(-time.Hour)), EndsAt: ptr(now.Add(time.Hour))}, // active
		{ProjectID: "future", ReservedGenerators: 5, StartsAt: ptr(now.Add(time.Hour))},                                          // inactive
		{ProjectID: "past", ReservedGenerators: 4, EndsAt: ptr(now.Add(-time.Hour))},                                             // inactive
	}

	total, _ := capacityReservationTotalsAt(settings, "", now)
	if total != 5 {
		t.Fatalf("active total = %d, want 5 (2 always + 3 window)", total)
	}
	if _, project := capacityReservationTotalsAt(settings, "future", now); project != 0 {
		t.Fatalf("future reservation should not be active, got %d", project)
	}
	if _, project := capacityReservationTotalsAt(settings, "active-window", now); project != 3 {
		t.Fatalf("active-window project reserved = %d, want 3", project)
	}
}

func TestNormalizeDropsInvertedWindow(t *testing.T) {
	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	out := normalizeCapacityReservations([]CapacityReservation{
		{ProjectID: "good", ReservedGenerators: 1},
		{ProjectID: "inverted", ReservedGenerators: 2, StartsAt: ptr(now), EndsAt: ptr(now.Add(-time.Hour))},
	})
	if len(out) != 1 || out[0].ProjectID != "good" {
		t.Fatalf("inverted-window reservation should be dropped, got %#v", out)
	}
}

func TestValidateSystemSettingsRejectsInvertedWindow(t *testing.T) {
	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	settings := defaultSystemSettings(4)
	settings.CapacityPlanning.Reservations = []CapacityReservation{
		{ProjectID: "x", ReservedGenerators: 1, StartsAt: ptr(now), EndsAt: ptr(now.Add(-time.Minute))},
	}
	if err := validateSystemSettings(settings); err == nil {
		t.Fatal("expected validation error for inverted reservation window")
	}
}

func TestValidateSystemSettingsAllowsNonOverlappingOversubscription(t *testing.T) {
	morning := time.Date(2026, time.June, 1, 9, 0, 0, 0, time.UTC)
	settings := defaultSystemSettings(10)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 10
	// Two windowed reservations of 8 each that never overlap -> allowed even
	// though their sum (16) exceeds the 10-generator inventory.
	settings.CapacityPlanning.Reservations = []CapacityReservation{
		{ProjectID: "team-a", ReservedGenerators: 8, StartsAt: ptr(morning), EndsAt: ptr(morning.Add(3 * time.Hour))},
		{ProjectID: "team-b", ReservedGenerators: 8, StartsAt: ptr(morning.Add(4 * time.Hour)), EndsAt: ptr(morning.Add(7 * time.Hour))},
	}
	if err := validateSystemSettings(settings); err != nil {
		t.Fatalf("non-overlapping windowed reservations should validate, got %v", err)
	}
}
