package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// DecodeNativeDataRequest is shared by guest adapters. Unknown authority fields,
// null cells and extra JSON documents are rejected before service admission.
func DecodeNativeDataRequest(data []byte) (NativeDataRequest, error) {
	var request NativeDataRequest
	invalid := func() (NativeDataRequest, error) {
		return NativeDataRequest{}, &NativeDataError{Code: "request_invalid"}
	}
	if len(data) == 0 || len(data) > NativeDataRequestBytes || !utf8.Valid(data) || !validNativeStringEscapes(data) {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		return invalid()
	}
	if decoder.Decode(new(any)) != io.EOF {
		return invalid()
	}
	// The standard struct decoder folds field names and merges repeated maps.
	// Require canonical, unique envelope keys before checking cell presence.
	fields := make(map[string]json.RawMessage)
	fieldDecoder := json.NewDecoder(bytes.NewReader(data))
	start, err := fieldDecoder.Token()
	if err != nil || start != json.Delim('{') {
		return invalid()
	}
	for fieldDecoder.More() {
		token, err := fieldDecoder.Token()
		if err != nil {
			return invalid()
		}
		key, ok := token.(string)
		if !ok {
			return invalid()
		}
		switch key {
		case "alias", "operation", "record_id", "position", "columns", "values", "expected", "expected_version", "delta", "wait_ms", "lease_ms", "limit", "cursor", "lease_ref":
		default:
			return invalid()
		}
		if _, exists := fields[key]; exists {
			return invalid()
		}
		var value json.RawMessage
		if fieldDecoder.Decode(&value) != nil {
			return invalid()
		}
		fields[key] = value
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid()
		}
		if key == "values" || key == "expected" {
			var cells map[string]json.RawMessage
			if json.Unmarshal(value, &cells) != nil {
				return invalid()
			}
			for _, cell := range cells {
				if bytes.Equal(bytes.TrimSpace(cell), []byte("null")) {
					return invalid()
				}
			}
		}
	}
	if end, err := fieldDecoder.Token(); err != nil || end != json.Delim('}') {
		return invalid()
	}
	return request, nil
}

// encoding/json replaces unpaired UTF-16 surrogates with U+FFFD. Reject them
// before decoding, so malformed keys/identities cannot acquire aliases.
func validNativeStringEscapes(body []byte) bool {
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
		code, ok := nativeUnicodeEscape(body, i)
		if !ok {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			low, ok := nativeUnicodeEscape(body, i+6)
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
func nativeUnicodeEscape(body []byte, offset int) (uint16, bool) {
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
