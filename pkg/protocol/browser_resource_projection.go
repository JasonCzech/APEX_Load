package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const BrowserResourceArchiveProjectionVersion = 1
const MaxBrowserResourceProjectionBytes = 8 << 20

var errBrowserResourceProjectionLimit = errors.New("browser resource archive projection exceeds resource limits")
var errBrowserResourceProjectionMalformed = errors.New("browser resource archive projection is malformed")

// The producer derives this column from the exact transaction JSONL row after
// its existing disclosure boundary. Only reader fields survive; bodies, headers,
// screenshots and debug values are absent. Explicit unavailable markers prevent
// a projection limit from truncating unrelated transaction archive content.
func BrowserResourceArchiveProjection(raw []byte) []byte {
	reason := "resource_projection_invalid"
	if len(raw) > MaxBrowserResourceProjectionBytes {
		reason = "resource_projection_limit"
	} else {
		transaction, err := browserResourceProjectTransaction(raw)
		if err == nil {
			out, err := json.Marshal(struct {
				Version      int             `json:"version"`
				Availability string          `json:"availability"`
				Transaction  json.RawMessage `json:"transaction"`
			}{1, "retained_projection", transaction})
			if err == nil && len(out) <= MaxBrowserResourceProjectionBytes {
				return out
			}
			reason = "resource_projection_limit"
		} else if errors.Is(err, errBrowserResourceProjectionLimit) {
			reason = "resource_projection_limit"
		}
	}
	out, _ := json.Marshal(struct {
		Version      int    `json:"version"`
		Availability string `json:"availability"`
		Reason       string `json:"reason"`
	}{1, "unavailable", reason})
	return out
}

// DecodeBrowserResourceArchiveProjection returns an owned minimal transaction
// or an explicit fixed unavailability reason. It never returns a partial row.
func DecodeBrowserResourceArchiveProjection(raw []byte) ([]byte, string, error) {
	fields, err := browserResourceProjectionObject(raw, []string{"version", "availability", "transaction", "reason"})
	if err != nil {
		return nil, "", err
	}
	// The envelope itself is closed even though input transaction rows are broad.
	var closed struct {
		Version      int             `json:"version"`
		Availability string          `json:"availability"`
		Transaction  json.RawMessage `json:"transaction"`
		Reason       string          `json:"reason"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&closed) != nil || closed.Version != 1 || fields["version"] == nil || fields["availability"] == nil {
		return nil, "", errBrowserResourceProjectionMalformed
	}
	if closed.Availability == "unavailable" {
		if fields["transaction"] != nil || fields["reason"] == nil || closed.Reason != "resource_projection_limit" && closed.Reason != "resource_projection_invalid" {
			return nil, "", errBrowserResourceProjectionMalformed
		}
		return nil, closed.Reason, nil
	}
	if closed.Availability != "retained_projection" || fields["reason"] != nil || len(closed.Transaction) == 0 || bytes.Equal(bytes.TrimSpace(closed.Transaction), []byte("null")) {
		return nil, "", errBrowserResourceProjectionMalformed
	}
	// Admission repeats the projection checks. Canonical equality refuses unknown
	// payloads hidden inside a forged supposedly minimal transaction projection.
	canonical, err := browserResourceProjectTransaction(closed.Transaction)
	if err != nil || !bytes.Equal(canonical, closed.Transaction) {
		return nil, "", errBrowserResourceProjectionMalformed
	}
	return append([]byte(nil), canonical...), "", nil
}

func browserResourceProjectTransaction(raw []byte) ([]byte, error) {
	fields, err := browserResourceProjectionObject(raw, []string{"test_id", "timestamp", "action_name", "group", "region", "page_url", "client_metrics", "network_entries", "snapshot"})
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"client_metrics", "snapshot"} {
		value, present := fields[name]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		selected := []string{"device_profile", "device_platform", "browser_engine"}
		if name == "snapshot" {
			selected = []string{"page_url", "network_entries"}
		}
		minimum, err := browserResourceProjectionObject(value, selected)
		if err != nil {
			return nil, err
		}
		if entries, ok := minimum["network_entries"]; ok {
			projected, err := browserResourceProjectEntries(entries)
			if err != nil {
				return nil, err
			}
			minimum["network_entries"] = projected
		}
		encoded, err := json.Marshal(minimum)
		if err != nil {
			return nil, err
		}
		fields[name] = encoded
	}
	if entries, present := fields["network_entries"]; present {
		projected, err := browserResourceProjectEntries(entries)
		if err != nil {
			return nil, err
		}
		fields["network_entries"] = projected
	}
	out, err := json.Marshal(fields)
	if err != nil || len(out) > MaxBrowserResourceProjectionBytes {
		return nil, errBrowserResourceProjectionMalformed
	}
	return out, nil
}
func browserResourceProjectEntries(raw []byte) ([]byte, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return []byte("null"), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('[') {
		return nil, errBrowserResourceProjectionMalformed
	}
	rows := make([]map[string]json.RawMessage, 0)
	for decoder.More() {
		if len(rows) >= 4096 {
			return nil, errBrowserResourceProjectionLimit
		}
		var entry json.RawMessage
		if decoder.Decode(&entry) != nil {
			return nil, errBrowserResourceProjectionMalformed
		}
		fields, err := browserResourceProjectionObject(entry, []string{"url", "method", "status_code", "resource_type", "failure_kind", "duration_ms", "bytes_received", "browser_evidence"})
		if err != nil {
			return nil, err
		}
		if native, present := fields["browser_evidence"]; present && !bytes.Equal(bytes.TrimSpace(native), []byte("null")) {
			if evidence, err := DecodeBrowserResourceEvidence(native); err == nil {
				fields["browser_evidence"], err = json.Marshal(evidence)
				if err != nil {
					return nil, err
				}
			} else {
				// Preserve malformed/future availability semantics without retaining their
				// unapproved raw body/header/debug keys in the minimal physical column.
				var version struct {
					Version int `json:"version"`
				}
				if json.Unmarshal(native, &version) != nil {
					version.Version = 0
				}
				fields["browser_evidence"], _ = json.Marshal(version)
			}
		}
		rows = append(rows, fields)
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
		return nil, errBrowserResourceProjectionMalformed
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errBrowserResourceProjectionMalformed
	}
	return json.Marshal(rows)
}

func browserResourceProjectionObject(raw []byte, selected []string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaxBrowserResourceProjectionBytes || !utf8.Valid(raw) {
		return nil, errBrowserResourceProjectionMalformed
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, errBrowserResourceProjectionMalformed
	}
	allowed := map[string]bool{}
	for _, key := range selected {
		allowed[key] = true
	}
	seen := map[string]bool{}
	fields := map[string]json.RawMessage{}
	tokens := 1 << 17
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] || len(seen) >= 4096 || len(name) > 256 {
			return nil, errBrowserResourceProjectionMalformed
		}
		seen[name] = true
		for _, candidate := range selected {
			if name != candidate && strings.EqualFold(name, candidate) {
				return nil, errBrowserResourceProjectionMalformed
			}
		}
		if allowed[name] {
			var value json.RawMessage
			if d.Decode(&value) != nil {
				return nil, errBrowserResourceProjectionMalformed
			}
			fields[name] = value
		} else if !browserResourceProjectionSkipValue(d, 0, &tokens) {
			return nil, errBrowserResourceProjectionMalformed
		}
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return nil, errBrowserResourceProjectionMalformed
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errBrowserResourceProjectionMalformed
	}
	return fields, nil
}
func browserResourceProjectionSkipValue(d *json.Decoder, depth int, tokens *int) bool {
	if depth > 32 || *tokens <= 0 {
		return false
	}
	*tokens -= 1
	token, err := d.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] || len(name) > 256 || len(seen) >= 4096 {
				return false
			}
			seen[name] = true
			if !browserResourceProjectionSkipValue(d, depth+1, tokens) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for d.More() {
			if !browserResourceProjectionSkipValue(d, depth+1, tokens) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}
