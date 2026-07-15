package api

import (
	"testing"
	"time"
)

func TestNonNegativeIntegerBoundsFailClosedOnOverflow(t *testing.T) {
	if got := saturatingAddNonNegative(maxPlatformInt, 1); got != maxPlatformInt {
		t.Fatalf("saturating add = %d, want %d", got, maxPlatformInt)
	}
	if got := saturatingMultiplyNonNegative(maxPlatformInt, 2); got != maxPlatformInt {
		t.Fatalf("saturating multiply = %d, want %d", got, maxPlatformInt)
	}
	if !nonNegativeSumExceedsLimit(maxPlatformInt, 1, maxPlatformInt) {
		t.Fatal("overflowing sum did not exceed limit")
	}
}

func TestValidateSystemSettingsRejectsReservationSumOverflow(t *testing.T) {
	settings := defaultSystemSettings(1)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = maxPlatformInt
	settings.CapacityPlanning.Reservations = []CapacityReservation{
		{ProjectID: "one", ReservedGenerators: maxPlatformInt},
		{ProjectID: "two", ReservedGenerators: 1},
	}
	if err := validateSystemSettings(settings); err == nil {
		t.Fatal("validateSystemSettings unexpectedly accepted overflowing reservations")
	}
}

func TestCapacityReservationTotalsSaturateInsteadOfWrapping(t *testing.T) {
	settings := defaultSystemSettings(1)
	settings.CapacityPlanning.Reservations = []CapacityReservation{
		{ProjectID: "one", ReservedGenerators: maxPlatformInt},
		{ProjectID: "one", ReservedGenerators: 1},
	}
	total, project := capacityReservationTotalsAt(settings, "one", time.Now().UTC())
	if total != maxPlatformInt || project != maxPlatformInt {
		t.Fatalf("reservation totals = (%d, %d), want saturated (%d, %d)", total, project, maxPlatformInt, maxPlatformInt)
	}
}
