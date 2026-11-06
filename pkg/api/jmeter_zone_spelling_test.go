package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJMeterZoneSpelledExactly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Testland"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A case-variant sibling listed before the exact entry must not make the
	// exact spelling read as misspelled.
	for _, name := range []string{"alpha_city", "Alpha_City"} {
		if err := os.WriteFile(filepath.Join(root, "Testland", name), []byte("TZif"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Testland", "Beta_Town"), []byte("TZif"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZONEINFO", root)
	entries, err := os.ReadDir(filepath.Join(root, "Testland"))
	if err != nil {
		t.Fatal(err)
	}
	// Only a case-sensitive filesystem keeps both spellings; on macOS the
	// second write replaces the first entry's contents under its first name.
	caseSensitive := len(entries) == 3

	cases := []struct {
		zone string
		want bool
	}{
		{zone: "Testland/Alpha_City", want: caseSensitive},
		{zone: "Testland/Beta_Town", want: true},
		{zone: "testland/beta_town", want: false},
		{zone: "Testland/BETA_TOWN", want: false},
		// Not in any directory: the zone came from an embedded zip.
		{zone: "Nowhere_Zone/Absent_City", want: true},
		{zone: "Testland//Beta_Town", want: false},
		{zone: "Testland/./Beta_Town", want: false},
		{zone: "Testland/../Testland/Beta_Town", want: false},
	}
	for _, tc := range cases {
		if got := jmeterZoneSpelledExactly(tc.zone); got != tc.want {
			t.Errorf("jmeterZoneSpelledExactly(%q) = %v, want %v", tc.zone, got, tc.want)
		}
	}
}

func TestJMeterReferenceTimezoneRefusesMisspelledSystemZone(t *testing.T) {
	if _, err := os.Stat("/usr/share/zoneinfo/America/Chicago"); err != nil {
		t.Skip("system zoneinfo not available")
	}
	if zone, ok := jmeterReferenceTimezone("America/Chicago"); !ok || zone != "America/Chicago" {
		t.Fatalf("America/Chicago = %q, %v; want accepted", zone, ok)
	}
	if zone, ok := jmeterReferenceTimezone("america/chicago"); ok {
		t.Fatalf("america/chicago = %q accepted; want refused", zone)
	}
	if zone, ok := jmeterReferenceTimezone("etc/utc"); !ok || zone != "UTC" {
		t.Fatalf("etc/utc = %q, %v; want UTC", zone, ok)
	}
}
