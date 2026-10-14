package browser

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
)

func TestFilterHARByHost(t *testing.T) {
	har := func(hosts ...string) []byte {
		entries := make([]any, 0, len(hosts))
		for _, h := range hosts {
			entries = append(entries, map[string]any{"request": map[string]any{"url": "https://" + h + "/path"}})
		}
		doc := map[string]any{"log": map[string]any{"version": "1.2", "entries": entries}}
		data, _ := json.Marshal(doc)
		return data
	}
	countEntries := func(data []byte) int {
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		return len(doc["log"].(map[string]any)["entries"].([]any))
	}

	t.Run("exclude hosts", func(t *testing.T) {
		out, result, err := filterHARByHost(har("app.example.com", "analytics.google.com", "cdn.example.com"),
			HARFilters{ExcludeHosts: []string{"google.com", "cdn.example.com"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.dropped != 2 || countEntries(out) != 1 {
			t.Errorf("dropped=%d kept=%d, want 2 dropped / 1 kept", result.dropped, countEntries(out))
		}
	})

	// RA-05: entries match whole labels, "*." is a subdomain wildcard, and
	// the stop warning says what each entry left out and which matched none.
	t.Run("exclude hosts match on label boundaries and are counted", func(t *testing.T) {
		out, result, err := filterHARByHost(
			har("www.microsoft.com", "t.co", "a.conviva.com", "b.conviva.com", "ad.doubleclick.net", "www.salesforce.com"),
			HARFilters{ExcludeHosts: []string{"t.co", "*.conviva.com", "doubleclick.net", "vidyard.com"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.dropped != 4 || countEntries(out) != 2 {
			t.Errorf("dropped=%d kept=%d, want 4 dropped / 2 kept (microsoft.com is not t.co)", result.dropped, countEntries(out))
		}
		want := "Host filters left 4 HAR entries out: t.co 1, *.conviva.com 2, doubleclick.net 1. No request matched Exclude hosts vidyard.com."
		if got := result.summary(); got != want {
			t.Errorf("summary = %q\nwant      %q", got, want)
		}
	})

	t.Run("an entry with a port matches that port only", func(t *testing.T) {
		out, result, err := filterHARByHost(har("localhost:8080", "localhost:9090", "localhost"),
			HARFilters{ExcludeHosts: []string{"localhost:8080"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.dropped != 1 || countEntries(out) != 2 {
			t.Errorf("dropped=%d kept=%d, want only localhost:8080 dropped", result.dropped, countEntries(out))
		}
	})

	t.Run("an unfiltered HAR does not report unmatched entries", func(t *testing.T) {
		_, result, err := filterHARByHost([]byte(`{"log":{"version":"1.2"}}`), HARFilters{ExcludeHosts: []string{"t.co"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := result.summary(); got != "" {
			t.Errorf("summary = %q, want nothing for a HAR without entries", got)
		}
	})

	t.Run("include hosts whitelist", func(t *testing.T) {
		out, result, err := filterHARByHost(har("api.example.com", "tracker.io"),
			HARFilters{IncludeHosts: []string{"example.com"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.dropped != 1 || countEntries(out) != 1 {
			t.Errorf("dropped=%d kept=%d, want 1/1", result.dropped, countEntries(out))
		}
		if got := result.summary(); got != "Host filters left 1 HAR entry out: 1 outside Include hosts." {
			t.Errorf("summary = %q", got)
		}
	})

	t.Run("no filters keeps everything", func(t *testing.T) {
		data := har("a.com", "b.com")
		out, result, err := filterHARByHost(data, HARFilters{})
		if err != nil || result.dropped != 0 || !bytes.Equal(out, data) || result.summary() != "" {
			t.Errorf("expected passthrough, got dropped=%d err=%v", result.dropped, err)
		}
	})
}

func TestIsPrintableUTF8(t *testing.T) {
	cases := map[string]bool{
		"hello world": true,
		"with\ttab\n": true,
		"\x00binary":  false,
		"\x01\x02":    false,
	}
	for in, want := range cases {
		if got := isPrintableUTF8(in); got != want {
			t.Errorf("isPrintableUTF8(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestThumbnailDataURL(t *testing.T) {
	// A 400x300 solid image should downscale to a ~200px-wide JPEG data URL.
	src := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			src.Set(x, y, color.RGBA{R: 80, G: 120, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	url, err := thumbnailDataURL(buf.Bytes(), 200)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("thumbnail prefix = %q", url[:32])
	}
	if len(url) > 16000 {
		t.Errorf("thumbnail unexpectedly large: %d bytes", len(url))
	}
}
