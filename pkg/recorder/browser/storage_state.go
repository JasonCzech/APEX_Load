package browser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	playwright "github.com/mxschmitt/playwright-go"
)

const (
	maxBrowserStorageStateBytes        = 8 << 20
	maxBrowserStorageCookies           = 2048
	maxBrowserStorageCookieBytes       = 4 << 20
	maxBrowserStorageOrigins           = maxBrowserPages
	maxBrowserStorageEntriesPerOrigin  = 256
	maxBrowserStorageOriginBytes       = 128 << 10
	maxBrowserStorageLocalBytes        = 4 << 20
	maxBrowserStorageCookieNameBytes   = 1 << 10
	maxBrowserStorageCookieValueBytes  = 16 << 10
	maxBrowserStorageCookieDomainBytes = 4 << 10
	maxBrowserStorageCookiePathBytes   = 4 << 10
	maxBrowserStorageEntryNameBytes    = 1 << 10
	maxBrowserStorageEntryValueBytes   = 16 << 10
)

type recorderStorageExport struct {
	data      []byte
	truncated bool
	err       error
}

func loadRecorderStorageState(path string) (*playwright.OptionalStorageState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("storage state must be a regular file")
	}
	raw, err := readFileLimited(path, maxBrowserStorageStateBytes)
	if err != nil {
		return nil, err
	}
	return decodeRecorderStorageState(raw)
}

// decodeRecorderStorageState parses arrays incrementally so a small JSON file
// containing hundreds of thousands of empty objects cannot amplify into an
// arbitrarily large Go object graph before cardinality validation runs.
func decodeRecorderStorageState(raw []byte) (*playwright.OptionalStorageState, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("storage state must be a JSON object")
	}
	state := &playwright.OptionalStorageState{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyToken.(string)
		switch key {
		case "cookies":
			cookies, err := decodeRecorderStorageCookies(decoder)
			if err != nil {
				return nil, err
			}
			state.Cookies = cookies
		case "origins":
			origins, err := decodeRecorderStorageOrigins(decoder)
			if err != nil {
				return nil, err
			}
			state.Origins = origins
		default:
			var discard json.RawMessage
			if err := decoder.Decode(&discard); err != nil {
				return nil, err
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("storage state has trailing JSON data")
	}
	return state, nil
}

func decodeRecorderStorageCookies(decoder *json.Decoder) ([]playwright.OptionalCookie, error) {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("storage state cookies must be an array")
	}
	cookies := make([]playwright.OptionalCookie, 0)
	retained := 0
	for decoder.More() {
		if len(cookies) >= maxBrowserStorageCookies {
			return nil, fmt.Errorf("storage state exceeds %d cookies", maxBrowserStorageCookies)
		}
		var cookie playwright.OptionalCookie
		if err := decoder.Decode(&cookie); err != nil {
			return nil, err
		}
		bytes, err := validateOptionalStorageCookie(cookie)
		if err != nil {
			return nil, err
		}
		if bytes > maxBrowserStorageCookieBytes-retained {
			return nil, fmt.Errorf("storage state cookie data exceeds %d bytes", maxBrowserStorageCookieBytes)
		}
		retained += bytes
		cookies = append(cookies, cookie)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return cookies, nil
}

func validateOptionalStorageCookie(cookie playwright.OptionalCookie) (int, error) {
	fields := []struct {
		name  string
		value string
		limit int
	}{
		{name: "name", value: cookie.Name, limit: maxBrowserStorageCookieNameBytes},
		{name: "value", value: cookie.Value, limit: maxBrowserStorageCookieValueBytes},
	}
	for _, field := range fields {
		if len(field.value) > field.limit {
			return 0, fmt.Errorf("storage state cookie %s exceeds %d bytes", field.name, field.limit)
		}
	}
	total := len(cookie.Name) + len(cookie.Value)
	for label, value := range map[string]*string{"url": cookie.URL, "domain": cookie.Domain, "path": cookie.Path, "partitionKey": cookie.PartitionKey} {
		if value == nil {
			continue
		}
		limit := maxBrowserStorageCookieDomainBytes
		if label == "path" {
			limit = maxBrowserStorageCookiePathBytes
		}
		if len(*value) > limit {
			return 0, fmt.Errorf("storage state cookie %s exceeds %d bytes", label, limit)
		}
		total += len(*value)
	}
	return total, nil
}

func decodeRecorderStorageOrigins(decoder *json.Decoder) ([]playwright.Origin, error) {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("storage state origins must be an array")
	}
	origins := make([]playwright.Origin, 0)
	totalBytes := 0
	for decoder.More() {
		if len(origins) >= maxBrowserStorageOrigins {
			return nil, fmt.Errorf("storage state exceeds %d origins", maxBrowserStorageOrigins)
		}
		var rawOrigin json.RawMessage
		if err := decoder.Decode(&rawOrigin); err != nil {
			return nil, err
		}
		origin, bytes, err := decodeRecorderStorageOrigin(rawOrigin)
		if err != nil {
			return nil, err
		}
		if bytes > maxBrowserStorageLocalBytes-totalBytes {
			return nil, fmt.Errorf("storage state local storage exceeds %d bytes", maxBrowserStorageLocalBytes)
		}
		totalBytes += bytes
		origins = append(origins, origin)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return origins, nil
}

func decodeRecorderStorageOrigin(raw []byte) (playwright.Origin, int, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var wire struct {
		Origin       string            `json:"origin"`
		LocalStorage []json.RawMessage `json:"localStorage"`
	}
	if err := decoder.Decode(&wire); err != nil {
		return playwright.Origin{}, 0, err
	}
	if len(wire.Origin) > maxBrowserNativeURLBytes {
		return playwright.Origin{}, 0, fmt.Errorf("storage state origin exceeds %d bytes", maxBrowserNativeURLBytes)
	}
	if len(wire.LocalStorage) > maxBrowserStorageEntriesPerOrigin {
		return playwright.Origin{}, 0, fmt.Errorf("storage state origin exceeds %d local storage entries", maxBrowserStorageEntriesPerOrigin)
	}
	origin := playwright.Origin{Origin: wire.Origin, LocalStorage: make([]playwright.NameValue, 0, len(wire.LocalStorage))}
	bytesUsed := len(wire.Origin)
	for _, rawEntry := range wire.LocalStorage {
		var entry playwright.NameValue
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			return playwright.Origin{}, 0, err
		}
		if len(entry.Name) > maxBrowserStorageEntryNameBytes || len(entry.Value) > maxBrowserStorageEntryValueBytes {
			return playwright.Origin{}, 0, fmt.Errorf("storage state local storage entry exceeds field limit")
		}
		bytesUsed += len(entry.Name) + len(entry.Value)
		if bytesUsed > maxBrowserStorageOriginBytes {
			return playwright.Origin{}, 0, fmt.Errorf("storage state origin exceeds %d bytes", maxBrowserStorageOriginBytes)
		}
		origin.LocalStorage = append(origin.LocalStorage, entry)
	}
	return origin, bytesUsed, nil
}

func exportRecorderStorageState(context playwright.BrowserContext, pages []playwright.Page) recorderStorageExport {
	urls := make([]string, 0, len(pages))
	seenURLs := make(map[string]struct{}, len(pages))
	for _, page := range pages {
		if page == nil {
			continue
		}
		rawURL := boundedtext.TruncateUTF8(page.URL(), maxBrowserNativeURLBytes)
		parsed, err := url.Parse(rawURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			continue
		}
		if _, ok := seenURLs[rawURL]; ok {
			continue
		}
		seenURLs[rawURL] = struct{}{}
		urls = append(urls, rawURL)
		if len(urls) >= maxBrowserPages {
			break
		}
	}
	state := playwright.StorageState{}
	truncated := false
	if len(urls) > 0 {
		cookies, err := context.Cookies(urls...)
		if err != nil {
			return recorderStorageExport{err: err}
		}
		state.Cookies, truncated = boundRecorderStorageCookies(cookies)
	}
	seenOrigins := make(map[string]struct{}, len(pages))
	localBytes := 0
	for _, page := range pages {
		if page == nil || len(state.Origins) >= maxBrowserStorageOrigins || localBytes >= maxBrowserStorageLocalBytes {
			break
		}
		result, err := page.Evaluate(`(() => {
			const entries = [];
			let remaining = 131072;
			try {
				const count = Math.min(localStorage.length, 256);
				for (let i = 0; i < count && remaining > 0; i++) {
					const name = String(localStorage.key(i) || '').slice(0, 1024);
					const value = String(localStorage.getItem(name) || '').slice(0, Math.min(16384, remaining));
					remaining -= name.length + value.length;
					entries.push({name, value});
				}
			} catch (_) {}
			return {origin: String(location.origin || '').slice(0, 16384), localStorage: entries};
		})()`)
		if err != nil {
			continue
		}
		origin, bytesUsed, wasTruncated := projectRecorderStorageOrigin(result, maxBrowserStorageLocalBytes-localBytes)
		truncated = truncated || wasTruncated
		if origin.Origin == "" {
			continue
		}
		if _, exists := seenOrigins[origin.Origin]; exists {
			continue
		}
		seenOrigins[origin.Origin] = struct{}{}
		state.Origins = append(state.Origins, origin)
		localBytes += bytesUsed
	}
	data, err := json.Marshal(state)
	if err != nil {
		return recorderStorageExport{err: err}
	}
	if len(data) > maxBrowserStorageStateBytes {
		return recorderStorageExport{err: fmt.Errorf("storage state exceeds %d bytes", maxBrowserStorageStateBytes)}
	}
	return recorderStorageExport{data: data, truncated: truncated}
}

func boundRecorderStorageCookies(cookies []playwright.Cookie) ([]playwright.Cookie, bool) {
	limit := min(len(cookies), maxBrowserStorageCookies)
	out := make([]playwright.Cookie, 0, limit)
	remaining := maxBrowserStorageCookieBytes
	truncated := len(cookies) > maxBrowserStorageCookies
	for _, cookie := range cookies[:limit] {
		bounded := cookie
		bounded.Name = boundedtext.TruncateUTF8(cookie.Name, maxBrowserStorageCookieNameBytes)
		bounded.Value = boundedtext.TruncateUTF8(cookie.Value, maxBrowserStorageCookieValueBytes)
		bounded.Domain = boundedtext.TruncateUTF8(cookie.Domain, maxBrowserStorageCookieDomainBytes)
		bounded.Path = boundedtext.TruncateUTF8(cookie.Path, maxBrowserStorageCookiePathBytes)
		if cookie.PartitionKey != nil {
			partition := boundedtext.TruncateUTF8(*cookie.PartitionKey, maxBrowserStorageCookieDomainBytes)
			bounded.PartitionKey = &partition
		}
		bytesUsed := len(bounded.Name) + len(bounded.Value) + len(bounded.Domain) + len(bounded.Path)
		if bounded.PartitionKey != nil {
			bytesUsed += len(*bounded.PartitionKey)
		}
		if bytesUsed > remaining {
			truncated = true
			break
		}
		remaining -= bytesUsed
		out = append(out, bounded)
		if bounded.Name != cookie.Name || bounded.Value != cookie.Value || bounded.Domain != cookie.Domain || bounded.Path != cookie.Path {
			truncated = true
		}
	}
	return out, truncated
}

func projectRecorderStorageOrigin(value any, remaining int) (playwright.Origin, int, bool) {
	data, ok := value.(map[string]any)
	if !ok || remaining <= 0 {
		return playwright.Origin{}, 0, true
	}
	rawOrigin, _ := data["origin"].(string)
	origin := playwright.Origin{Origin: boundedtext.TruncateUTF8(rawOrigin, maxBrowserNativeURLBytes)}
	used := len(origin.Origin)
	truncated := origin.Origin != rawOrigin
	rawEntries, _ := data["localStorage"].([]any)
	for index, rawEntry := range rawEntries {
		if index >= maxBrowserStorageEntriesPerOrigin || used >= maxBrowserStorageOriginBytes || used >= remaining {
			truncated = true
			break
		}
		entryData, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		rawName, _ := entryData["name"].(string)
		rawValue, _ := entryData["value"].(string)
		entry := playwright.NameValue{
			Name:  boundedtext.TruncateUTF8(rawName, maxBrowserStorageEntryNameBytes),
			Value: boundedtext.TruncateUTF8(rawValue, maxBrowserStorageEntryValueBytes),
		}
		entryBytes := len(entry.Name) + len(entry.Value)
		allowed := min(maxBrowserStorageOriginBytes-used, remaining-used)
		if entryBytes > allowed {
			truncated = true
			break
		}
		used += entryBytes
		origin.LocalStorage = append(origin.LocalStorage, entry)
		if entry.Name != rawName || entry.Value != rawValue {
			truncated = true
		}
	}
	return origin, used, truncated
}
