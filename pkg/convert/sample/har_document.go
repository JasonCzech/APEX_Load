package sample

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/clientids"
	"github.com/jasonczech/apexload/pkg/convert/clienttimestamps"
)

// HARDocument owns one parsed capture. Its source maps are private and read
// only; exported projections allocate their own mutable values. The document
// contains source values and must never be included in an import response.
type HARDocument struct {
	items          []map[string]any
	legacyRecorder bool
	evidenceItems  []map[string]any
	evidenceErr    error
	statusLexemes  []string
	statusErrors   []error
	byteOrderMark  bool
	digest         [32]byte
}

func ParseHARDocument(content string) (*HARDocument, error) {
	var payload any
	decoder := json.NewDecoder(strings.NewReader(strings.TrimPrefix(content, "\uFEFF")))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to parse HAR JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return nil, fmt.Errorf("failed to parse HAR JSON: %w", err)
	}
	items, err := extractHARItems(payload)
	if err != nil {
		return nil, err
	}
	doc := &HARDocument{items: items, byteOrderMark: strings.HasPrefix(content, "\uFEFF"), digest: sha256.Sum256([]byte(content))}
	root, _ := payload.(map[string]any)
	// Typed evidence adapters accept case-insensitive keys. Record their view
	// separately from conversion's existing canonical HAR entry selection.
	doc.evidenceItems, doc.evidenceErr = typedEvidenceItems(root)
	if hasDuplicateHAREvidenceFields(content) {
		doc.evidenceErr = fmt.Errorf("duplicate HAR evidence fields require raw decoding")
	}
	doc.statusLexemes = make([]string, len(doc.evidenceItems))
	doc.statusErrors = make([]error, len(doc.evidenceItems))
	for i, item := range doc.evidenceItems {
		response, statusErr := evidenceObject(item, "response")
		var raw any
		if statusErr == nil {
			raw, statusErr = evidenceValue(response, "status")
		}
		if raw != nil && statusErr == nil {
			if number, ok := raw.(json.Number); ok {
				doc.statusLexemes[i] = number.String()
			} else {
				statusErr = fmt.Errorf("invalid HAR response status")
			}
		}
		doc.statusErrors[i] = statusErr
	}
	// Restore the generic converter's float64 representation after retaining
	// status spelling: typed integer JSON rejects 200.0 and 2e2, even though
	// both become float64(200). This is one parse, with no body reserialization.
	if err := normalizeHARJSONNumbers(payload); err != nil {
		return nil, fmt.Errorf("failed to parse HAR JSON: %w", err)
	}
	if log, ok := root["log"].(map[string]any); ok {
		root = log
	}
	if creator, ok := root["creator"].(map[string]any); ok {
		doc.legacyRecorder = creator["name"] == "APEX Load Recorder"
	}
	return doc, nil
}

// Matches prevents accidentally reusing evidence from a different capture.
func (d *HARDocument) Matches(data []byte) bool { return d != nil && d.digest == sha256.Sum256(data) }

// AnyRequestURL performs cheap source detection without decoding response
// bodies or allocating normalized observations. The callback only receives an
// immutable string; malformed and filtered entries may be conservative hits.
func (d *HARDocument) AnyRequestURL(matches func(string) bool) bool {
	if d == nil {
		return false
	}
	for _, item := range d.items {
		request, _ := item["request"].(map[string]any)
		if matches(strings.TrimSpace(stringValue(request["url"]))) {
			return true
		}
	}
	return false
}

func (d *HARDocument) SourceObservations(includeStaticAssets bool) ([]HARSourceObservation, error) {
	return d.observations(includeStaticAssets, true)
}

// ClientTimestamps preserves the timestamp adapter's canonical log.entries
// interpretation, including its refusal of malformed typed evidence.
func (d *HARDocument) ClientTimestamps() (clienttimestamps.Evidence, error) {
	evidence := clienttimestamps.Evidence{Server: map[string]bool{}}
	if d.byteOrderMark {
		return evidence, fmt.Errorf("timestamp evidence has a JSON byte order mark")
	}
	if d.evidenceErr != nil {
		return evidence, d.evidenceErr
	}
	for _, item := range d.evidenceItems {
		started, err := evidenceString(item, "startedDateTime")
		if err != nil {
			return evidence, err
		}
		if at, err := time.Parse(time.RFC3339Nano, started); err == nil {
			evidence.Observe(at)
		}
		response, err := evidenceObject(item, "response")
		if err != nil {
			return evidence, err
		}
		body, err := evidenceBody(response)
		if err != nil {
			return evidence, err
		}
		evidence.AddServerText(body)
		redirect, err := evidenceString(response, "redirectURL")
		if err != nil {
			return evidence, err
		}
		evidence.AddServerText(redirect)
		headers, err := evidenceValues(response, "headers", false)
		if err != nil {
			return evidence, err
		}
		for _, header := range headers {
			evidence.AddServerText(header)
		}
	}
	return evidence, nil
}

func (d *HARDocument) ServerUUIDs() (map[string]bool, bool, error) {
	evidence := clientids.ServerEvidence{}
	if d.evidenceErr != nil {
		return nil, false, d.evidenceErr
	}
	for i, item := range d.evidenceItems {
		response, err := evidenceObject(item, "response")
		if err != nil {
			return nil, false, err
		}
		if d.statusErrors[i] != nil {
			return nil, false, d.statusErrors[i]
		}
		status := 0
		if d.statusLexemes[i] != "" {
			status, err = strconv.Atoi(d.statusLexemes[i])
			if err != nil {
				return nil, false, fmt.Errorf("invalid HAR response status: %w", err)
			}
		}
		content, err := evidenceObject(response, "content")
		if err != nil {
			return nil, false, err
		}
		mime, err := evidenceString(content, "mimeType")
		if err != nil {
			return nil, false, err
		}
		body, err := evidenceBody(response)
		if err != nil {
			return nil, false, err
		}
		evidence.ObserveResponse(status, mime, body)
		redirect, err := evidenceString(response, "redirectURL")
		if err != nil {
			return nil, false, err
		}
		evidence.AddText(redirect)
		for _, key := range []string{"headers", "cookies"} {
			values, err := evidenceValues(response, key, true)
			if err != nil {
				return nil, false, err
			}
			for _, value := range values {
				evidence.AddText(value)
			}
		}
	}
	found, judged := evidence.Result()
	return found, judged, nil
}

func evidenceObject(object map[string]any, key string) (map[string]any, error) {
	value, err := evidenceValue(object, key)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	mapped, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid HAR %s object", key)
	}
	return mapped, nil
}
func evidenceString(object map[string]any, key string) (string, error) {
	value, err := evidenceValue(object, key)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("invalid HAR %s string", key)
	}
	return text, nil
}
func evidenceBody(response map[string]any) (string, error) {
	content, err := evidenceObject(response, "content")
	if err != nil {
		return "", err
	}
	body, err := evidenceString(content, "text")
	if err != nil {
		return "", err
	}
	encoding, err := evidenceString(content, "encoding")
	if err != nil {
		return "", err
	}
	if strings.EqualFold(encoding, "base64") {
		if decoded, err := base64.StdEncoding.DecodeString(body); err == nil {
			return string(decoded), nil
		}
	}
	return body, nil
}
func evidenceValues(response map[string]any, key string, validateName bool) ([]string, error) {
	value, err := evidenceValue(response, key)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid HAR %s array", key)
	}
	values := make([]string, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok && raw != nil {
			return nil, fmt.Errorf("invalid HAR %s entry", key)
		}
		if validateName {
			if _, err := evidenceString(item, "name"); err != nil {
				return nil, err
			}
		}
		text, err := evidenceString(item, "value")
		if err != nil {
			return nil, err
		}
		values = append(values, text)
	}
	return values, nil
}

// Ambiguous case aliases require the original ordered JSON decoder. The shared
// map intentionally does not guess which spelling occurred last in the source.
func evidenceValue(object map[string]any, key string) (any, error) {
	var value any
	found := false
	for name, candidate := range object {
		if strings.EqualFold(name, key) {
			if found {
				return nil, fmt.Errorf("ambiguous HAR evidence field %s requires raw decoding", key)
			}
			value, found = candidate, true
		}
	}
	return value, nil
}

func typedEvidenceItems(root map[string]any) ([]map[string]any, error) {
	log, err := evidenceObject(root, "log")
	if err != nil {
		return nil, err
	}
	raw, err := evidenceValue(log, "entries")
	if err != nil || raw == nil {
		return nil, err
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid HAR evidence entries array")
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok && entry != nil {
			return nil, fmt.Errorf("invalid HAR evidence entry")
		}
		items = append(items, item)
	}
	return items, nil
}

func normalizeHARJSONNumbers(value any) error {
	convert := func(value any) (any, error) {
		if number, ok := value.(json.Number); ok {
			return number.Float64()
		}
		return value, normalizeHARJSONNumbers(value)
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			normalized, err := convert(item)
			if err != nil {
				return err
			}
			typed[key] = normalized
		}
	case []any:
		for i, item := range typed {
			normalized, err := convert(item)
			if err != nil {
				return err
			}
			typed[i] = normalized
		}
	}
	return nil
}

// A typed JSON struct merges repeated nested object fields, whereas the generic
// conversion map keeps the last object. Keep that rare ordered behavior in the
// raw fallback. This lexical pass skips values (including large bodies) without
// materializing another JSON tree and tracks only relevant field-name bits.
func hasDuplicateHAREvidenceFields(content string) bool {
	type frame struct {
		object, key bool
		fields      uint32
	}
	stack := make([]frame, 0, 8)
	for i := 0; i < len(content); i++ {
		switch content[i] {
		case '{':
			stack = append(stack, frame{object: true, key: true})
		case '[':
			stack = append(stack, frame{})
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case ',':
			if len(stack) > 0 {
				stack[len(stack)-1].key = stack[len(stack)-1].object
			}
		case '"':
			start, escaped := i, false
			for i++; i < len(content); i++ {
				if content[i] == '\\' {
					escaped = true
					i++
					continue
				}
				if content[i] == '"' {
					break
				}
			}
			if len(stack) == 0 || !stack[len(stack)-1].key {
				continue
			}
			current := &stack[len(stack)-1]
			current.key = false
			if i >= len(content) {
				return false
			} // full JSON validation already ran
			key := content[start+1 : i]
			if escaped {
				if err := json.Unmarshal([]byte(content[start:i+1]), &key); err != nil {
					return false
				}
			}
			key = strings.ReplaceAll(strings.ToLower(key), "ſ", "s")
			bit := harEvidenceFieldBits[key]
			if bit != 0 && current.fields&bit != 0 {
				return true
			}
			current.fields |= bit
		}
	}
	return false
}

var harEvidenceFieldBits = map[string]uint32{
	"log": 1 << 0, "entries": 1 << 1, "starteddatetime": 1 << 2, "response": 1 << 3,
	"status": 1 << 4, "content": 1 << 5, "text": 1 << 6, "encoding": 1 << 7,
	"mimetype": 1 << 8, "headers": 1 << 9, "cookies": 1 << 10, "redirecturl": 1 << 11,
	"name": 1 << 12, "value": 1 << 13,
}
