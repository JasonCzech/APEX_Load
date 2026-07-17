package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

func TestValidateRecorderOptionsRejectsResourceBounds(t *testing.T) {
	validURL := "https://example.test/"
	tests := []struct {
		name string
		opts Options
		url  string
	}{
		{name: "scheme", url: "ftp://example.test/file"},
		{name: "url bytes", url: "https://example.test/" + strings.Repeat("x", maxBrowserNativeURLBytes)},
		{name: "device dimensions", url: validURL, opts: Options{Device: &DeviceEmulation{Width: maxBrowserViewportDimension + 1, Height: 1}}},
		{name: "device area", url: validURL, opts: Options{Device: &DeviceEmulation{Width: maxBrowserViewportDimension, Height: maxBrowserViewportDimension}}},
		{name: "device scale", url: validURL, opts: Options{Device: &DeviceEmulation{Width: 100, Height: 100, DeviceScaleFactor: maxBrowserDeviceScale + 1}}},
		{name: "window pair", url: validURL, opts: Options{WindowWidth: 800}},
		{name: "filter count", url: validURL, opts: Options{HARFilters: HARFilters{IncludeHosts: make([]string, 129)}}},
		{name: "filter bytes", url: validURL, opts: Options{HARFilters: HARFilters{ExcludeHosts: []string{strings.Repeat("x", 1025)}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateRecorderOptions(tc.opts, tc.url); err == nil {
				t.Fatal("validateRecorderOptions returned nil error")
			}
		})
	}
}

func TestRecorderNativeMetadataWarningsAndPauseHistoryAreBounded(t *testing.T) {
	r := New(Options{StartURL: "https://example.test/"})
	r.mu.Lock()
	r.nativeMetadataBytes = maxBrowserNativeMetadata - 5
	retained := r.retainNativeStringLocked("0123456789", 10)
	r.appendWarningLocked(strings.Repeat("w", maxBrowserNativeTextBytes+100))
	r.warningBytes = maxBrowserWarningBytes
	r.appendWarningLocked("must be dropped")
	r.mu.Unlock()
	if len(retained) != 5 || r.nativeMetadataBytes != maxBrowserNativeMetadata {
		t.Fatalf("native metadata retained=%q bytes=%d", retained, r.nativeMetadataBytes)
	}
	if len(r.session.Warnings) != 1 || len(r.session.Warnings[0]) > maxBrowserNativeTextBytes {
		t.Fatalf("warnings = %#v", r.session.Warnings)
	}

	for range maxBrowserPauseIntervals + 10 {
		r.setPaused(true)
		r.setPaused(false)
	}
	if len(r.session.PauseIntervals) != maxBrowserPauseIntervals {
		t.Fatalf("pause intervals = %d, want %d", len(r.session.PauseIntervals), maxBrowserPauseIntervals)
	}
}

func TestRecorderInteractiveOperationsRejectConcurrentFanout(t *testing.T) {
	r := New(Options{StartURL: "https://example.test/"})
	r.navigationSlots <- struct{}{}
	if err := r.Navigate("https://example.test/next"); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("Navigate concurrency error = %v", err)
	}
	<-r.navigationSlots

	r.controlPanelSlots <- struct{}{}
	if err := r.OpenControlPanel("http://127.0.0.1:3000/#/recorder-panel", nil); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("OpenControlPanel concurrency error = %v", err)
	}
}

func TestSessionMarkerBounds(t *testing.T) {
	if _, err := (&Session{}).AddMarker(Marker{Kind: MarkerNote, Text: strings.Repeat("x", maxBrowserMarkerText+1)}); err == nil {
		t.Fatal("oversized marker text was accepted")
	}
	session := &Session{retainedMarkerBytes: maxBrowserUserMarkerBytes}
	if _, err := session.AddMarker(Marker{Kind: MarkerNote, Text: "note"}); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("marker byte-limit error = %v", err)
	}
	session = &Session{Markers: make([]Marker, maxBrowserUserMarkers)}
	if _, err := session.AddMarker(Marker{Kind: MarkerNote, Text: "note"}); err == nil || !strings.Contains(err.Error(), "marker limit") {
		t.Fatalf("marker count-limit error = %v", err)
	}
}

func TestDecodeRecorderStorageStateBoundsCardinalityAndFields(t *testing.T) {
	valid := []byte(`{"cookies":[{"name":"sid","value":"abc","domain":"example.test","path":"/"}],"origins":[{"origin":"https://example.test","localStorage":[{"name":"token","value":"value"}]}]}`)
	state, err := decodeRecorderStorageState(valid)
	if err != nil || len(state.Cookies) != 1 || len(state.Origins) != 1 {
		t.Fatalf("valid storage state = %#v, %v", state, err)
	}

	tooManyCookies := `{"cookies":[` + strings.Repeat(`{},`, maxBrowserStorageCookies) + `{}` + `]}`
	if _, err := decodeRecorderStorageState([]byte(tooManyCookies)); err == nil || !strings.Contains(err.Error(), "cookies") {
		t.Fatalf("cookie cardinality error = %v", err)
	}
	tooManyEntries := `{"origins":[{"origin":"https://example.test","localStorage":[` + strings.Repeat(`{},`, maxBrowserStorageEntriesPerOrigin) + `{}` + `]}]}`
	if _, err := decodeRecorderStorageState([]byte(tooManyEntries)); err == nil || !strings.Contains(err.Error(), "local storage entries") {
		t.Fatalf("local-storage cardinality error = %v", err)
	}
}

func TestLoadRecorderStorageStateRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte(`{"cookies":[],"origins":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "state-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRecorderStorageState(link); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink storage state error = %v", err)
	}
}

func TestStorageStateProjectionBoundsCookiesAndLocalStorage(t *testing.T) {
	cookies := make([]playwright.Cookie, maxBrowserStorageCookies+1)
	for index := range cookies {
		cookies[index] = playwright.Cookie{Name: fmt.Sprintf("cookie-%d", index), Value: "value", Domain: "example.test", Path: "/"}
	}
	bounded, truncated := boundRecorderStorageCookies(cookies)
	if !truncated || len(bounded) > maxBrowserStorageCookies {
		t.Fatalf("bounded cookies=%d truncated=%v", len(bounded), truncated)
	}

	entries := make([]any, maxBrowserStorageEntriesPerOrigin+1)
	for index := range entries {
		entries[index] = map[string]any{"name": fmt.Sprintf("key-%d", index), "value": strings.Repeat("v", maxBrowserStorageEntryValueBytes+1)}
	}
	origin, bytesUsed, originTruncated := projectRecorderStorageOrigin(map[string]any{
		"origin":       "https://example.test",
		"localStorage": entries,
	}, maxBrowserStorageOriginBytes)
	if !originTruncated || len(origin.LocalStorage) > maxBrowserStorageEntriesPerOrigin || bytesUsed > maxBrowserStorageOriginBytes {
		t.Fatalf("origin entries=%d bytes=%d truncated=%v", len(origin.LocalStorage), bytesUsed, originTruncated)
	}
}
