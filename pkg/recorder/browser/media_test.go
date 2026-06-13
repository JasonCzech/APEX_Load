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
		out, dropped, err := filterHARByHost(har("app.example.com", "analytics.google.com", "cdn.example.com"),
			HARFilters{ExcludeHosts: []string{"google", "cdn"}})
		if err != nil {
			t.Fatal(err)
		}
		if dropped != 2 || countEntries(out) != 1 {
			t.Errorf("dropped=%d kept=%d, want 2 dropped / 1 kept", dropped, countEntries(out))
		}
	})

	t.Run("include hosts whitelist", func(t *testing.T) {
		out, dropped, err := filterHARByHost(har("api.example.com", "tracker.io"),
			HARFilters{IncludeHosts: []string{"example.com"}})
		if err != nil {
			t.Fatal(err)
		}
		if dropped != 1 || countEntries(out) != 1 {
			t.Errorf("dropped=%d kept=%d, want 1/1", dropped, countEntries(out))
		}
	})

	t.Run("no filters keeps everything", func(t *testing.T) {
		data := har("a.com", "b.com")
		out, dropped, err := filterHARByHost(data, HARFilters{})
		if err != nil || dropped != 0 || !bytes.Equal(out, data) {
			t.Errorf("expected passthrough, got dropped=%d err=%v", dropped, err)
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
