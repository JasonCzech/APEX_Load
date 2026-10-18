package perfevidence

import (
	"testing"
	"time"
)

func TestFootprintInvalidSamplesNeverEnterFit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sample footprintSample
	}{
		{"connection failure", footprintSample{Protocol: "network", VUsers: 2000, ActionSamples: 2000, ActionErrors: 12, HeapAlloc: 1}},
		{"missing VUsers", footprintSample{Protocol: "network", VUsers: 2000, ActionSamples: 1988, HeapAlloc: 1}},
		{"extra actions", footprintSample{Protocol: "network", VUsers: 2000, ActionSamples: 2001, HeapAlloc: 1}},
		{"wrong protocol", footprintSample{Protocol: "http", VUsers: 2000, ActionSamples: 2000}},
		{"empty", footprintSample{Protocol: "network"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := footprintResult{Protocol: "network"}
			for _, count := range []int{250, 1000} {
				if err := result.addSample(footprintSample{Protocol: "network", VUsers: count, ActionSamples: int64(count), HeapAlloc: uint64(count * 1024)}); err != nil {
					t.Fatal(err)
				}
			}
			if err := result.addSample(tc.sample); err == nil {
				t.Fatal("invalid sample accepted")
			}
			result.fit()
			if len(result.InvalidSamples) != 1 || result.InvalidSamples[0].Reason == "" || len(result.Samples) != 2 || !result.FitAvailable || result.GoMemoryKBPerVUser != 1 {
				t.Fatalf("invalid evidence affected fit: %+v", result)
			}
		})
	}
	result := footprintResult{Protocol: "network"}
	_ = result.addSample(footprintSample{Protocol: "network", VUsers: 250, ActionSamples: 250, ActionErrors: 1})
	result.fit()
	if result.FitAvailable {
		t.Fatal("failed-only run reported a fit")
	}
}

func TestFootprintRawNetworkAdmissionIsPaced(t *testing.T) {
	for _, count := range []int{250, 1000, 2000} {
		if got := footprintRampUp("network", count) / time.Duration(count); got != 2*time.Millisecond {
			t.Fatalf("%d VUsers: admission spacing %v", count, got)
		}
	}
	for _, name := range []string{"noop", "http", "websocket", "grpc", "network"} {
		if got := footprintRampUp(name, 1); got != 0 {
			t.Fatalf("single %s user ramp=%v", name, got)
		}
		if name != "network" && footprintRampUp(name, 2000) != 0 {
			t.Fatalf("changed %s admission", name)
		}
	}
}

func TestFootprintFitRequiresDistinctVUserCounts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		counts []int
		valid  bool
	}{
		{"empty", nil, false},
		{"single", []int{250}, false},
		{"duplicates", []int{250, 250}, false},
		{"distinct after duplicates", []int{250, 250, 1000}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := footprintResult{Protocol: "noop"}
			for _, count := range tc.counts {
				if err := result.addSample(footprintSample{Protocol: "noop", VUsers: count, ActionSamples: int64(count), HeapAlloc: uint64(count * 1024)}); err != nil {
					t.Fatal(err)
				}
			}
			result.fit()
			if result.FitAvailable != tc.valid {
				t.Fatalf("fit available=%v, want %v", result.FitAvailable, tc.valid)
			}
			if tc.valid && result.GoMemoryKBPerVUser != 1 {
				t.Fatalf("slope=%v, want1", result.GoMemoryKBPerVUser)
			}
		})
	}
}
