package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxBrowserDocumentSummaryBytes = 512 * 1024

// browserDocumentWirePresence preserves unavailable versus explicitly observed
// zero/false at JSON boundaries. Go's ordinary decoder cannot distinguish an
// omitted required field from its zero value and accepts duplicate keys. This
// bounded typed token walk runs before materialization, using the producer's
// canonical json tags as the presence contract. Explicit null collections are
// valid because an owned nil Go slice/map marshals to null; scalars never are.
func browserDocumentWirePresence(raw []byte, typ reflect.Type, limit int) bool {
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	remaining := 1 << 17
	if !browserDocumentWireValue(decoder, typ, false, 0, &remaining) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func browserDocumentWireValue(d *json.Decoder, typ reflect.Type, nullable bool, depth int, remaining *int) bool {
	if depth > 16 || *remaining <= 0 {
		return false
	}
	*remaining -= 1
	token, err := d.Token()
	if err != nil {
		return false
	}
	if token == nil {
		return nullable
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeOf(time.Time{}) {
		_, ok := token.(string)
		return ok
	}
	switch typ.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			return false
		}
		type field struct {
			typ      reflect.Type
			required bool
		}
		fields := make(map[string]field, typ.NumField())
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			parts := strings.Split(f.Tag.Get("json"), ",")
			if f.PkgPath != "" || parts[0] == "-" {
				continue
			}
			name := parts[0]
			if name == "" {
				name = f.Name
			}
			required := true
			for _, option := range parts[1:] {
				if option == "omitempty" {
					required = false
				}
			}
			fields[name] = field{f.Type, required}
		}
		seen := make(map[string]bool, len(fields))
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return false
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return false
			}
			f, exists := fields[name]
			if !exists {
				return false
			}
			seen[name] = true
			nullable := f.typ.Kind() == reflect.Pointer || f.typ.Kind() == reflect.Slice || f.typ.Kind() == reflect.Map
			if !browserDocumentWireValue(d, f.typ, nullable, depth+1, remaining) {
				return false
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') {
			return false
		}
		for name, f := range fields {
			if f.required && !seen[name] {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if token != json.Delim('[') {
			return false
		}
		count := 0
		for d.More() {
			count++
			if count > 4096 || typ.Kind() == reflect.Array && count > typ.Len() {
				return false
			}
			elem := typ.Elem()
			if !browserDocumentWireValue(d, elem, elem.Kind() == reflect.Pointer, depth+1, remaining) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim(']') && (typ.Kind() != reflect.Array || count == typ.Len())
	case reflect.Map:
		if token != json.Delim('{') || typ.Key().Kind() != reflect.String {
			return false
		}
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return false
			}
			name, ok := key.(string)
			if !ok || len(name) > 256 || seen[name] || len(seen) >= 256 {
				return false
			}
			seen[name] = true
			if !browserDocumentWireValue(d, typ.Elem(), false, depth+1, remaining) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim('}')
	case reflect.String:
		_, ok := token.(string)
		return ok
	case reflect.Bool:
		_, ok := token.(bool)
		return ok
	case reflect.Int, reflect.Int64, reflect.Int32:
		n, ok := token.(json.Number)
		if !ok {
			return false
		}
		_, err := n.Int64()
		return err == nil
	case reflect.Uint, reflect.Uint64, reflect.Uint32:
		n, ok := token.(json.Number)
		if !ok {
			return false
		}
		_, err := strconv.ParseUint(string(n), 10, 64)
		return err == nil
	case reflect.Float64, reflect.Float32:
		_, ok := token.(json.Number)
		return ok
	default:
		return false
	}
}

// DecodeBrowserDocumentSummary admits only complete, bounded canonical v2 wire
// metadata. Malformed peer data must become explicitly incomplete at callers,
// rather than an apparently healthy zero-population summary.
func DecodeBrowserDocumentSummary(raw []byte) (*BrowserDocumentSummary, error) {
	if !browserDocumentWirePresence(raw, reflect.TypeOf(BrowserDocumentSummary{}), MaxBrowserDocumentSummaryBytes) {
		return nil, fmt.Errorf("browser document summary is malformed or incomplete")
	}
	var summary BrowserDocumentSummary
	if json.Unmarshal(raw, &summary) != nil {
		return nil, fmt.Errorf("browser document summary is malformed")
	}
	owned := summary.Clone()
	if owned == nil {
		return nil, fmt.Errorf("browser document summary is malformed")
	}
	return owned, nil
}
