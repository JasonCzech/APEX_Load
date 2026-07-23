package dsl

import (
	"strconv"
	"strings"
)

// Browser emulation catalog: the curated set of browser identities the
// "specific browser" mode of BrowserEmulationConfig can select. The catalog is
// the single source of truth for the author UI dropdowns (served via
// GET /api/v1/dsl/browser-emulation-catalog), validation, and the replay-time
// User-Agent resolution in the engine.

// BrowserEmulationCatalogEntry describes one selectable browser type.
type BrowserEmulationCatalogEntry struct {
	Type      string   `json:"type"`
	Display   string   `json:"label"`
	Versions  []string `json:"versions"`  // newest first
	Platforms []string `json:"platforms"` // first entry is the default platform
}

// BrowserEmulationPlatform is a platform option with a display label.
type BrowserEmulationPlatform struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// BrowserEmulationLanguage is a curated language option for the UI dropdown.
// Any valid BCP-47 tag is accepted by validation; this list only seeds the UI.
type BrowserEmulationLanguage struct {
	Tag   string `json:"tag"`
	Label string `json:"label"`
}

// browserUATemplates maps browserType -> platform -> UA template. "{v}" is
// replaced with the selected major version.
var browserUATemplates = map[string]map[string]string{
	"chrome": {
		"windows": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/{v}.0.0.0 Safari/537.36",
		"macos":   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/{v}.0.0.0 Safari/537.36",
		"linux":   "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/{v}.0.0.0 Safari/537.36",
		"android": "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/{v}.0.0.0 Mobile Safari/537.36",
	},
	"edge": {
		"windows": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/{v}.0.0.0 Safari/537.36 Edg/{v}.0.0.0",
		"macos":   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/{v}.0.0.0 Safari/537.36 Edg/{v}.0.0.0",
	},
	"firefox": {
		"windows": "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:{v}.0) Gecko/20100101 Firefox/{v}.0",
		"macos":   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:{v}.0) Gecko/20100101 Firefox/{v}.0",
		"linux":   "Mozilla/5.0 (X11; Linux x86_64; rv:{v}.0) Gecko/20100101 Firefox/{v}.0",
	},
	"safari": {
		"macos": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/{v}.4 Safari/605.1.15",
		"ios":   "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/{v}.4 Mobile/15E148 Safari/604.1",
	},
	"ie": {
		"windows": "Mozilla/5.0 (Windows NT 10.0; WOW64; Trident/7.0; rv:11.0) like Gecko",
	},
}

// ieLegacyUA covers IE versions below 11, which predate the Trident/7.0 token.
const ieLegacyUA = "Mozilla/5.0 (compatible; MSIE {v}.0; Windows NT 6.1; Trident/6.0)"

var browserEmulationCatalog = []BrowserEmulationCatalogEntry{
	{Type: "chrome", Display: "Google Chrome", Versions: []string{"126", "120", "109"}, Platforms: []string{"windows", "macos", "linux", "android"}},
	{Type: "edge", Display: "Microsoft Edge", Versions: []string{"126", "120"}, Platforms: []string{"windows", "macos"}},
	{Type: "firefox", Display: "Mozilla Firefox", Versions: []string{"127", "115"}, Platforms: []string{"windows", "macos", "linux"}},
	{Type: "safari", Display: "Apple Safari", Versions: []string{"17", "16"}, Platforms: []string{"macos", "ios"}},
	{Type: "ie", Display: "Microsoft Internet Explorer", Versions: []string{"11", "10"}, Platforms: []string{"windows"}},
}

var browserEmulationPlatforms = []BrowserEmulationPlatform{
	{ID: "windows", Label: "Windows"},
	{ID: "macos", Label: "macOS"},
	{ID: "linux", Label: "Linux"},
	{ID: "android", Label: "Android"},
	{ID: "ios", Label: "iOS"},
}

var browserEmulationLanguages = []BrowserEmulationLanguage{
	{Tag: "en-US", Label: "English (United States)"},
	{Tag: "en-GB", Label: "English (United Kingdom)"},
	{Tag: "de-DE", Label: "German (Germany)"},
	{Tag: "fr-FR", Label: "French (France)"},
	{Tag: "es-ES", Label: "Spanish (Spain)"},
	{Tag: "pt-BR", Label: "Portuguese (Brazil)"},
	{Tag: "it-IT", Label: "Italian (Italy)"},
	{Tag: "nl-NL", Label: "Dutch (Netherlands)"},
	{Tag: "ja-JP", Label: "Japanese (Japan)"},
	{Tag: "zh-CN", Label: "Chinese (Simplified, China)"},
	{Tag: "ko-KR", Label: "Korean (Korea)"},
	{Tag: "hi-IN", Label: "Hindi (India)"},
}

// BrowserEmulationCatalog returns the curated browser identities, newest
// versions first. Callers must not mutate the returned slices.
func BrowserEmulationCatalog() []BrowserEmulationCatalogEntry {
	return browserEmulationCatalog
}

// BrowserEmulationPlatforms returns the platform display options.
func BrowserEmulationPlatforms() []BrowserEmulationPlatform {
	return browserEmulationPlatforms
}

// BrowserEmulationLanguages returns the curated language options.
func BrowserEmulationLanguages() []BrowserEmulationLanguage {
	return browserEmulationLanguages
}

// browserEmulationCatalogEntry looks up a catalog entry by type.
func browserEmulationCatalogEntry(browserType string) (BrowserEmulationCatalogEntry, bool) {
	t := strings.ToLower(strings.TrimSpace(browserType))
	for _, e := range browserEmulationCatalog {
		if e.Type == t {
			return e, true
		}
	}
	return BrowserEmulationCatalogEntry{}, false
}

// BrowserEmulationTypeInCatalog reports whether browserType is a known type.
func BrowserEmulationTypeInCatalog(browserType string) bool {
	_, ok := browserEmulationCatalogEntry(browserType)
	return ok
}

// BrowserEmulationPlatformValidFor reports whether platform is offered for
// browserType. An empty platform is always valid (default platform is used).
func BrowserEmulationPlatformValidFor(browserType, platform string) bool {
	if strings.TrimSpace(platform) == "" {
		return true
	}
	entry, ok := browserEmulationCatalogEntry(browserType)
	if !ok {
		return false
	}
	p := strings.ToLower(strings.TrimSpace(platform))
	for _, cand := range entry.Platforms {
		if cand == p {
			return true
		}
	}
	return false
}

// ResolveBrowserEmulationUserAgent maps a (type, version, platform) selection
// to a concrete User-Agent string. Empty version resolves to the newest
// catalog version; an unknown version resolves to the nearest catalog major
// (exact=false). Empty or unavailable platform falls back to the browser's
// default platform. ok=false only when the browser type itself is unknown.
func ResolveBrowserEmulationUserAgent(browserType, version, platform string) (ua string, exact bool, ok bool) {
	entry, found := browserEmulationCatalogEntry(browserType)
	if !found {
		return "", false, false
	}
	exact = true

	p := strings.ToLower(strings.TrimSpace(platform))
	templates := browserUATemplates[entry.Type]
	if _, has := templates[p]; !has {
		if p != "" {
			exact = false
		}
		p = entry.Platforms[0]
	}

	v := normalizeBrowserMajorVersion(version)
	if v == "" {
		v = entry.Versions[0]
	} else if !containsString(entry.Versions, v) {
		v = nearestBrowserVersion(entry.Versions, v)
		exact = false
	}

	tpl := templates[p]
	if entry.Type == "ie" {
		if major, err := strconv.Atoi(v); err == nil && major < 11 {
			tpl = ieLegacyUA
		}
	}
	return strings.ReplaceAll(tpl, "{v}", v), exact, true
}

// AcceptLanguageForLanguage derives a browser-like Accept-Language header
// value from a BCP-47 tag, e.g. "en-US" -> "en-US,en;q=0.9". Returns "" for
// blank input.
func AcceptLanguageForLanguage(lang string) string {
	tag := strings.TrimSpace(lang)
	if tag == "" {
		return ""
	}
	base, _, _ := strings.Cut(tag, "-")
	if base == "" || strings.EqualFold(base, tag) {
		return tag
	}
	return tag + "," + base + ";q=0.9"
}

// normalizeBrowserMajorVersion reduces "10.0" / "126.0.6478" / " 11 " to the
// leading major-version integer as a string; non-numeric input returns "".
func normalizeBrowserMajorVersion(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		return ""
	}
	major, _, _ := strings.Cut(v, ".")
	if _, err := strconv.Atoi(major); err != nil {
		return ""
	}
	return major
}

// nearestBrowserVersion picks the catalog version numerically closest to the
// requested major (ties resolve to the newer version, which sorts first).
func nearestBrowserVersion(catalog []string, requested string) string {
	want, err := strconv.Atoi(requested)
	if err != nil {
		return catalog[0]
	}
	best := catalog[0]
	bestDist := -1
	for _, cand := range catalog {
		cv, err := strconv.Atoi(cand)
		if err != nil {
			continue
		}
		dist := cv - want
		if dist < 0 {
			dist = -dist
		}
		if bestDist < 0 || dist < bestDist {
			best, bestDist = cand, dist
		}
	}
	return best
}

func containsString(list []string, want string) bool {
	for _, cand := range list {
		if cand == want {
			return true
		}
	}
	return false
}
