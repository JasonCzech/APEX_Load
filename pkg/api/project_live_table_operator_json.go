package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"
)

var errProjectTableMutationJSON = errors.New("invalid project live table mutation fields or body size")

func decodeProjectTableMutationBody(w http.ResponseWriter, r *http.Request, out *ProjectLiveTableMutation) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err == nil {
		var input ProjectLiveTableMutation
		input, err = decodeProjectTableMutationJSON(body)
		if err == nil {
			*out = input
			return true
		}
	}
	writeError(w, http.StatusBadRequest, errProjectTableMutationJSON.Error())
	return false
}

// Operator edits use the published string-cell contract. Validate the wire
// representation before encoding/json can fold field names, merge duplicate
// maps, replace malformed Unicode or turn a null cell into an empty string.
func decodeProjectTableMutationJSON(body []byte) (ProjectLiveTableMutation, error) {
	var input ProjectLiveTableMutation
	if len(body) == 0 || len(body) > 1<<20 || !utf8.Valid(body) || !validProjectMutationStringEscapes(body) {
		return input, errProjectTableMutationJSON
	}
	valid := projectMutationJSONObject(body, 11, func(key string, value json.RawMessage) bool {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
		switch key {
		case "idempotency_key", "issued_at", "expected_instance_id", "expected_revision", "operation", "record_id", "position", "expected_version", "delta":
			return true // The typed decoder below verifies each scalar's type.
		case "values":
			return projectMutationJSONObject(value, 128, func(_ string, cell json.RawMessage) bool {
				return projectMutationJSONString(cell)
			})
		case "columns":
			decoder := json.NewDecoder(bytes.NewReader(value))
			start, err := decoder.Token()
			if err != nil || start != json.Delim('[') {
				return false
			}
			for count := 0; decoder.More(); count++ {
				var column json.RawMessage
				if count >= 128 || decoder.Decode(&column) != nil || !projectMutationJSONString(column) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']') && decoder.Decode(new(any)) == io.EOF
		default:
			return false
		}
	})
	if !valid || json.Unmarshal(body, &input) != nil {
		return ProjectLiveTableMutation{}, errProjectTableMutationJSON
	}
	return input, nil
}

func projectMutationJSONString(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	var value string
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &value) == nil
}

func projectMutationJSONObject(body []byte, maxKeys int, field func(string, json.RawMessage) bool) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || len(seen) >= maxKeys {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || !field(key, value) {
			return false
		}
	}
	end, err := decoder.Token()
	return err == nil && end == json.Delim('}') && decoder.Decode(new(any)) == io.EOF
}

// Unpaired UTF-16 escapes are valid input to encoding/json's replacement
// behavior, but cannot represent an exact operator cell or column identity.
func validProjectMutationStringEscapes(body []byte) bool {
	inString := false
	for i := 0; i < len(body); i++ {
		if !inString {
			if body[i] == '"' {
				inString = true
			}
			continue
		}
		if body[i] == '"' {
			inString = false
			continue
		}
		if body[i] < 0x20 {
			return false
		}
		if body[i] != '\\' {
			continue
		}
		if i+1 >= len(body) {
			return false
		}
		if body[i+1] != 'u' {
			i++
			continue
		}
		code, ok := projectMutationUnicodeEscape(body, i)
		if !ok {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			low, ok := projectMutationUnicodeEscape(body, i+6)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 11
		} else {
			if code >= 0xdc00 && code <= 0xdfff {
				return false
			}
			i += 5
		}
	}
	return !inString
}

func projectMutationUnicodeEscape(body []byte, offset int) (uint16, bool) {
	if offset+6 > len(body) || body[offset] != '\\' || body[offset+1] != 'u' {
		return 0, false
	}
	var value uint16
	for _, digit := range body[offset+2 : offset+6] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value += uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value += uint16(digit - 'a' + 10)
		case digit >= 'A' && digit <= 'F':
			value += uint16(digit - 'A' + 10)
		default:
			return 0, false
		}
	}
	return value, true
}
