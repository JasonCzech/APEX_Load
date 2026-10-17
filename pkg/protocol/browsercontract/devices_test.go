package browsercontract

import "testing"

func TestLookupDevice(t *testing.T) {
	profile, ok := LookupDevice("iphone_se")
	if !ok {
		t.Fatal("LookupDevice(iphone_se) = not found")
	}
	if profile.Platform != "ios" {
		t.Fatalf("Platform = %q, want ios", profile.Platform)
	}
	if profile.CPUThrottleRate != 4 {
		t.Fatalf("CPUThrottleRate = %v, want 4", profile.CPUThrottleRate)
	}
	if profile.NetworkPreset != "56k" {
		t.Fatalf("NetworkPreset = %q, want 56k", profile.NetworkPreset)
	}

	if _, ok := LookupDevice("unknown_device"); ok {
		t.Fatal("LookupDevice(unknown_device) = found, want false")
	}
}

func TestListDevicesSorted(t *testing.T) {
	profiles := ListDevices()
	if len(profiles) < 2 {
		t.Fatalf("len(ListDevices()) = %d, want at least 2", len(profiles))
	}
	for i := 1; i < len(profiles); i++ {
		if profiles[i-1].Name > profiles[i].Name {
			t.Fatalf("profiles not sorted: %q before %q", profiles[i-1].Name, profiles[i].Name)
		}
	}
}
