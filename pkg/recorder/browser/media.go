package browser

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/url"
	"strings"
	"unicode/utf8"
)

// thumbnailDataURL decodes a JPEG screenshot, downscales it to maxWidth using a
// simple box filter, and returns a data:image/jpeg;base64 URL. Keeps per-step
// thumbnails to a few KB so they can ride inline in the session snapshot.
func thumbnailDataURL(raw []byte, maxWidth int) (string, error) {
	if maxWidth <= 0 {
		maxWidth = 200
	}
	src, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("decode screenshot: %w", err)
	}
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	if sw == 0 || sh == 0 {
		return "", fmt.Errorf("empty screenshot")
	}
	dw := maxWidth
	if sw < dw {
		dw = sw
	}
	dh := sh * dw / sw
	if dh < 1 {
		dh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy := bounds.Min.Y + y*sh/dh
		for x := 0; x < dw; x++ {
			sx := bounds.Min.X + x*sw/dw
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 60}); err != nil {
		return "", fmt.Errorf("encode thumbnail: %w", err)
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// isPrintableUTF8 reports whether s is valid UTF-8 without control bytes that
// would corrupt a JSON string (used to decide text vs base64 for WS payloads).
func isPrintableUTF8(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

// hostMatches reports whether host matches any of the substring patterns,
// mirroring the proxy recorder's include/exclude semantics.
func hostMatches(host string, patterns []string) bool {
	host = strings.ToLower(host)
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && strings.Contains(host, p) {
			return true
		}
	}
	return false
}

// filterHARByHost removes HAR log entries whose request host fails the
// include/exclude host filters, returning the rewritten HAR and the number of
// dropped entries. Unknown JSON shapes are left untouched.
func filterHARByHost(data []byte, filters HARFilters) ([]byte, int, error) {
	if filters.empty() {
		return data, 0, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, 0, fmt.Errorf("parse HAR: %w", err)
	}
	log, ok := doc["log"].(map[string]any)
	if !ok {
		return data, 0, nil
	}
	entries, ok := log["entries"].([]any)
	if !ok {
		return data, 0, nil
	}
	kept := make([]any, 0, len(entries))
	dropped := 0
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		host := harEntryHost(entry)
		if host == "" {
			kept = append(kept, raw)
			continue
		}
		if len(filters.IncludeHosts) > 0 && !hostMatches(host, filters.IncludeHosts) {
			dropped++
			continue
		}
		if hostMatches(host, filters.ExcludeHosts) {
			dropped++
			continue
		}
		kept = append(kept, raw)
	}
	if dropped == 0 {
		return data, 0, nil
	}
	log["entries"] = kept
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, 0, fmt.Errorf("re-encode HAR: %w", err)
	}
	return out, dropped, nil
}

func harEntryHost(entry map[string]any) string {
	req, ok := entry["request"].(map[string]any)
	if !ok {
		return ""
	}
	rawURL, ok := req["url"].(string)
	if !ok {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
