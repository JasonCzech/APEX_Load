package dsl

import (
	"strings"
	"testing"
)

func TestBrowserEmulationCatalogShape(t *testing.T) {
	catalog := BrowserEmulationCatalog()
	if len(catalog) == 0 {
		t.Fatal("catalog is empty")
	}
	platformIDs := map[string]bool{}
	for _, p := range BrowserEmulationPlatforms() {
		platformIDs[p.ID] = true
	}
	for _, entry := range catalog {
		if entry.Type == "" || entry.Display == "" {
			t.Fatalf("catalog entry missing type/display: %+v", entry)
		}
		if len(entry.Versions) == 0 || len(entry.Platforms) == 0 {
			t.Fatalf("catalog entry %s missing versions/platforms", entry.Type)
		}
		templates := browserUATemplates[entry.Type]
		if len(templates) == 0 {
			t.Fatalf("no UA templates for %s", entry.Type)
		}
		for _, p := range entry.Platforms {
			if !platformIDs[p] {
				t.Errorf("%s platform %q missing from BrowserEmulationPlatforms", entry.Type, p)
			}
			if _, ok := templates[p]; !ok {
				t.Errorf("%s platform %q has no UA template", entry.Type, p)
			}
		}
	}
	if len(BrowserEmulationLanguages()) == 0 {
		t.Fatal("languages list is empty")
	}
}

func TestResolveBrowserEmulationUserAgent(t *testing.T) {
	tests := []struct {
		name           string
		browserType    string
		version        string
		platform       string
		wantContains   string
		wantExact      bool
		wantOK         bool
		wantNoTemplate string
	}{
		{name: "exact chrome windows", browserType: "chrome", version: "126", platform: "windows", wantContains: "Chrome/126.0.0.0", wantExact: true, wantOK: true},
		{name: "empty version resolves newest", browserType: "chrome", version: "", platform: "linux", wantContains: "Chrome/126.0.0.0", wantExact: true, wantOK: true},
		{name: "dotted version normalized", browserType: "firefox", version: "115.0", platform: "linux", wantContains: "Firefox/115.0", wantExact: true, wantOK: true},
		{name: "unknown version nearest major", browserType: "chrome", version: "121", platform: "windows", wantContains: "Chrome/120.0.0.0", wantExact: false, wantOK: true},
		{name: "empty platform defaults", browserType: "edge", version: "126", platform: "", wantContains: "Edg/126.0.0.0", wantExact: true, wantOK: true},
		{name: "unsupported platform falls back inexact", browserType: "safari", version: "17", platform: "windows", wantContains: "Macintosh", wantExact: false, wantOK: true},
		{name: "ie 11 trident", browserType: "ie", version: "11", platform: "windows", wantContains: "Trident/7.0; rv:11.0", wantExact: true, wantOK: true},
		{name: "ie 10 legacy", browserType: "ie", version: "10", platform: "windows", wantContains: "MSIE 10.0", wantExact: true, wantOK: true},
		{name: "case insensitive type", browserType: "Chrome", version: "126", platform: "windows", wantContains: "Chrome/126.0.0.0", wantExact: true, wantOK: true},
		{name: "unknown type", browserType: "netscape", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ua, exact, ok := ResolveBrowserEmulationUserAgent(tt.browserType, tt.version, tt.platform)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if !strings.Contains(ua, tt.wantContains) {
				t.Errorf("ua %q does not contain %q", ua, tt.wantContains)
			}
			if exact != tt.wantExact {
				t.Errorf("exact = %v, want %v (ua %q)", exact, tt.wantExact, ua)
			}
			if strings.Contains(ua, "{v}") {
				t.Errorf("ua %q retains version placeholder", ua)
			}
		})
	}
}

func TestAcceptLanguageForLanguage(t *testing.T) {
	tests := []struct{ in, want string }{
		{"en-US", "en-US,en;q=0.9"},
		{"de-DE", "de-DE,de;q=0.9"},
		{"fr", "fr"},
		{" pt-BR ", "pt-BR,pt;q=0.9"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := AcceptLanguageForLanguage(tt.in); got != tt.want {
			t.Errorf("AcceptLanguageForLanguage(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
