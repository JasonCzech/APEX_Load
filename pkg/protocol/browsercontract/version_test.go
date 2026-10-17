package browsercontract

import "testing"

func TestBrowserVersionParsing(t *testing.T) {
	for version, want := range map[string]int{"149.0.7827.55": 149, "26.0": 26, "152": 152} {
		if major, ok := BrowserMajorVersion(version); !ok || major != want {
			t.Errorf("BrowserMajorVersion(%q) = %d, %v", version, major, ok)
		}
	}
	for _, version := range []string{"", "HeadlessChrome/149", "149.0.0.0.0", "149.x", " 149.0"} {
		if ValidBrowserVersion(version) {
			t.Errorf("ValidBrowserVersion(%q) = true", version)
		}
	}
}
