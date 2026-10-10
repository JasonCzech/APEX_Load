package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HARHTTPVersionProvenanceKey is the HAR entry field that carries APEX
// recorder provenance for the entry's HTTP version. Version 1 describes the
// proxy recorder's two legs; version 2 names a browser capture. Import treats
// it as descriptive only.
const HARHTTPVersionProvenanceKey = "_apexload_http_version"

// Browser capture sources for version 2 provenance.
const (
	HARProvenanceBrowser    = "browser"     // the browser recorder
	HARProvenanceBrowserRun = "browser_run" // a browser VUser's run HAR
)

// BrowserHARHTTPVersionProvenance is the version 2 envelope for one entry:
// its source and the protocol the browser reported (h1, h2, h3 or unknown).
func BrowserHARHTTPVersionProvenance(source, httpVersion string) map[string]any {
	return map[string]any{"version": 2, "source": source, "observed": BrowserObservedProtocol(httpVersion)}
}

// BrowserObservedProtocol names a HAR httpVersion as h1, h2 or h3. Draft
// HTTP/3 tokens, HTTP/1.0 and anything else are unknown.
func BrowserObservedProtocol(httpVersion string) string {
	switch strings.ToLower(strings.TrimSpace(httpVersion)) {
	case "http/1.1", "h1":
		return "h1"
	case "http/2", "http/2.0", "h2":
		return "h2"
	case "http/3", "http/3.0", "h3":
		return "h3"
	}
	return "unknown"
}

// TagBrowserHARHTTPVersions adds version 2 provenance to every entry of a
// browser-produced HAR, from the entry's request httpVersion. Entries that
// already carry provenance are left alone.
func TagBrowserHARHTTPVersions(data []byte, source string) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode HAR: %w", err)
	}
	log, _ := document["log"].(map[string]any)
	entries, _ := log["entries"].([]any)
	if len(entries) == 0 {
		return data, nil
	}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, present := entry[HARHTTPVersionProvenanceKey]; present {
			continue
		}
		request, _ := entry["request"].(map[string]any)
		version, _ := request["httpVersion"].(string)
		entry[HARHTTPVersionProvenanceKey] = BrowserHARHTTPVersionProvenance(source, version)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode HAR: %w", err)
	}
	return encoded, nil
}
