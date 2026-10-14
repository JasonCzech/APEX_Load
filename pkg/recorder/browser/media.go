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

	"github.com/jasonczech/apexload/pkg/internal/hostpattern"
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

// hostFilterResult says what a recording's host filters left out of its HAR.
type hostFilterResult struct {
	// applied is false when the HAR had no entries list to filter.
	applied bool
	dropped int
	// notIncluded counts the entries no include host matched; excludedBy
	// counts the entries each of excludeHosts left out, in the same order.
	notIncluded  int
	excludeHosts []string
	excludedBy   []int
}

// summary says how many HAR entries each exclude host left out and which
// matched nothing, so a mistyped or too-narrow entry shows after the stop.
func (result hostFilterResult) summary() string {
	var leftOut, unmatched []string
	for index, host := range result.excludeHosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if count := result.excludedBy[index]; count > 0 {
			leftOut = append(leftOut, fmt.Sprintf("%s %d", host, count))
		} else {
			unmatched = append(unmatched, host)
		}
	}
	if result.notIncluded > 0 {
		leftOut = append(leftOut, fmt.Sprintf("%d outside Include hosts", result.notIncluded))
	}
	var parts []string
	if result.dropped > 0 {
		noun := "entries"
		if result.dropped == 1 {
			noun = "entry"
		}
		parts = append(parts, fmt.Sprintf("Host filters left %d HAR %s out: %s.", result.dropped, noun, strings.Join(leftOut, ", ")))
	}
	if result.applied && len(unmatched) > 0 {
		parts = append(parts, fmt.Sprintf("No request matched Exclude hosts %s.", strings.Join(unmatched, ", ")))
	}
	return strings.Join(parts, " ")
}

// filterHARByHost removes HAR log entries whose request host fails the
// include/exclude host filters, returning the rewritten HAR and what was
// left out. Unknown JSON shapes are left untouched.
func filterHARByHost(data []byte, filters HARFilters) ([]byte, hostFilterResult, error) {
	if filters.empty() {
		return data, hostFilterResult{}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, hostFilterResult{}, fmt.Errorf("parse HAR: %w", err)
	}
	result := filterHARDocumentByHost(doc, filters)
	if result.dropped == 0 {
		return data, result, nil
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, hostFilterResult{}, fmt.Errorf("re-encode HAR: %w", err)
	}
	return out, result, nil
}

// filterHARDocumentByHost removes a parsed HAR's log entries whose request
// host fails the filters and says what it removed. A host entry matches that
// host and its subdomains (see hostpattern.Match). Unknown JSON shapes are
// left untouched.
func filterHARDocumentByHost(doc map[string]any, filters HARFilters) hostFilterResult {
	result := hostFilterResult{
		excludeHosts: filters.ExcludeHosts,
		excludedBy:   make([]int, len(filters.ExcludeHosts)),
	}
	if filters.empty() {
		return result
	}
	log, ok := doc["log"].(map[string]any)
	if !ok {
		return result
	}
	entries, ok := log["entries"].([]any)
	if !ok {
		return result
	}
	result.applied = true
	kept := make([]any, 0, len(entries))
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
		if len(filters.IncludeHosts) > 0 && hostpattern.Index(host, filters.IncludeHosts) < 0 {
			result.dropped++
			result.notIncluded++
			continue
		}
		if excluded := hostpattern.Index(host, filters.ExcludeHosts); excluded >= 0 {
			result.dropped++
			result.excludedBy[excluded]++
			continue
		}
		kept = append(kept, raw)
	}
	if result.dropped > 0 {
		log["entries"] = kept
	}
	return result
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
	// With its port, so an entry such as localhost:8080 can match.
	return parsed.Host
}
