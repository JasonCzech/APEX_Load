package api

import "testing"

func TestNativeProviderPack(t *testing.T) {
	for _, tc := range []struct {
		pack string
		want bool
	}{{"appdynamics", true}, {"dynatrace", true}, {"prometheus-query", true}, {"linux", false}, {"azure-monitor", false}, {"", false}} {
		if got := nativeProviderPack(tc.pack); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.pack, got, tc.want)
		}
	}
}
