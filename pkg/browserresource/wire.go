package browserresource

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
)

var browserResourcePageUTC = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`)

// DecodeBrowserResourcePage is shared by the CLI and approved page adapters.
// Required zero/nullable fields and duplicate/unknown keys are admitted before
// ordinary Go decoding, so absent evidence never becomes a healthy zero page.
func DecodeBrowserResourcePage(raw []byte) (*BrowserResourcePage, error) {
	invalid := errors.New("invalid browser resource page")
	if len(raw) == 0 || len(raw) > MaxBrowserResourcePageBytes || !utf8.Valid(raw) {
		return nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	tokens := 1 << 17
	if !WireValue(d, reflect.TypeOf(BrowserResourcePage{}), false, 0, &tokens) {
		return nil, invalid
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, invalid
	}
	var page BrowserResourcePage
	if json.Unmarshal(raw, &page) != nil || !ValidateBrowserResourcePage(&page) {
		return nil, invalid
	}
	// Semantic cloning alone cannot prove required nested resource wire fields.
	var fields struct {
		NextCursor json.RawMessage `json:"next_cursor"`
		Items      []struct {
			BrowserEvidence json.RawMessage `json:"browser_evidence"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &fields) != nil || len(fields.Items) != len(page.Items) || len(fields.NextCursor) != 0 && page.NextCursor == "" {
		return nil, invalid
	}
	for i, item := range fields.Items {
		if len(item.BrowserEvidence) != 0 && !bytes.Equal(bytes.TrimSpace(item.BrowserEvidence), []byte("null")) {
			e, err := protocol.DecodeBrowserResourceEvidence(item.BrowserEvidence)
			if err != nil {
				return nil, invalid
			}
			page.Items[i].BrowserEvidence = e
		}
	}
	return &page, nil
}

func WireValue(d *json.Decoder, typ reflect.Type, nullable bool, depth int, remaining *int) bool {
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
		s, ok := token.(string)
		if !ok || !browserResourcePageUTC.MatchString(s) {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, s)
		return err == nil
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
		fields := map[string]field{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			if f.PkgPath != "" || tag[0] == "-" {
				continue
			}
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			required := true
			for _, option := range tag[1:] {
				if option == "omitempty" {
					required = false
				}
			}
			fields[name] = field{f.Type, required}
		}
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return false
			}
			f, ok := fields[name]
			if !ok {
				return false
			}
			seen[name] = true
			kind := f.typ.Kind()
			if !WireValue(d, f.typ, kind == reflect.Pointer || kind == reflect.Slice || kind == reflect.Map, depth+1, remaining) {
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
	case reflect.Slice:
		if token != json.Delim('[') {
			return false
		}
		count := 0
		for d.More() {
			count++
			if count > MaxBrowserResourcePageItems || !WireValue(d, typ.Elem(), typ.Elem().Kind() == reflect.Pointer, depth+1, remaining) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim(']')
	case reflect.String:
		_, ok := token.(string)
		return ok
	case reflect.Bool:
		_, ok := token.(bool)
		return ok
	case reflect.Int, reflect.Int64:
		n, ok := token.(json.Number)
		if !ok {
			return false
		}
		_, err := n.Int64()
		return err == nil
	case reflect.Float64:
		_, ok := token.(json.Number)
		return ok
	default:
		return false
	}
}
