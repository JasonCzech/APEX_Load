package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

var analysisDefinitionRawType = reflect.TypeFor[json.RawMessage]()

// New definition writes use a closed, bounded request contract. In particular
// RawMessage cannot bypass duplicate/trailing/structural admission. Historical
// stored reads do not use this decoder and retain their compatibility behavior.
func decodeAnalysisDefinitionJSON(data []byte, target any) error {
	typ := reflect.TypeOf(target)
	if typ == nil || typ.Kind() != reflect.Pointer || reflect.ValueOf(target).IsNil() {
		return errors.New("analytical definition target must be an owned pointer")
	}
	if len(data) == 0 || len(data) > 128<<10 || !utf8.Valid(data) || !analysisDefinitionUnicodeEscapes(data) {
		return errors.New("invalid or excessive analytical definition JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	nodes := 0
	if err := walkAnalysisDefinitionJSON(d, typ, 0, &nodes); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("analytical definition has trailing input")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	owned := reflect.New(typ.Elem())
	if err := d.Decode(owned.Interface()); err != nil {
		return err
	}
	reflect.ValueOf(target).Elem().Set(owned.Elem())
	return nil
}

func walkAnalysisDefinitionJSON(d *json.Decoder, typ reflect.Type, depth int, nodes *int) error {
	*nodes++
	if depth > 8 || *nodes > 10000 {
		return errors.New("analytical definition exceeds structural bound")
	}
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == analysisDefinitionRawType {
		typ = nil // bounded syntax now, closed mapping/comparison admission next
	}
	token, err := d.Token()
	if err != nil || token == nil {
		return errors.New("analytical definition contains invalid or null fields")
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			if typ != nil && typ.Kind() != reflect.Struct && typ.Kind() != reflect.Map {
				return errors.New("unexpected analytical definition object")
			}
			var seen [16]string
			count := 0
			for d.More() {
				if count == len(seen) {
					return errors.New("analytical definition object exceeds field bound")
				}
				token, err := d.Token()
				key, ok := token.(string)
				if err != nil || !ok || key == "" || len(key) > 128 || strings.ContainsRune(key, 0) {
					return errors.New("invalid analytical definition field")
				}
				for i := 0; i < count; i++ {
					if seen[i] == key {
						return errors.New("duplicate analytical definition field")
					}
				}
				seen[count], count = key, count+1
				var child reflect.Type
				if typ != nil {
					if typ.Kind() == reflect.Map {
						child = typ.Elem()
					} else {
						for i := 0; i < typ.NumField(); i++ {
							field := typ.Field(i)
							name := strings.Split(field.Tag.Get("json"), ",")[0]
							if field.PkgPath == "" && name != "-" && name == key {
								child = field.Type
								break
							}
						}
						if child == nil {
							return errors.New("unsupported analytical definition field")
						}
					}
				}
				if err := walkAnalysisDefinitionJSON(d, child, depth+1, nodes); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim('}') {
				return errors.New("invalid analytical definition object")
			}
		case '[':
			if typ != nil && typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
				return errors.New("unexpected analytical definition list")
			}
			var child reflect.Type
			if typ != nil {
				child = typ.Elem()
			}
			count := 0
			for d.More() {
				if count == 64 {
					return errors.New("analytical definition list exceeds population bound")
				}
				count++
				if err := walkAnalysisDefinitionJSON(d, child, depth+1, nodes); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim(']') {
				return errors.New("invalid analytical definition list")
			}
		default:
			return errors.New("invalid analytical definition delimiter")
		}
		return nil
	}
	if text, ok := token.(string); ok {
		if len(text) > 256 || strings.ContainsRune(text, 0) {
			return errors.New("analytical definition scalar exceeds text bound")
		}
		if typ != nil && typ.Kind() != reflect.String && !(typ.PkgPath() == "time" && typ.Name() == "Time") {
			return errors.New("unexpected analytical definition text")
		}
		return nil
	}
	if typ != nil {
		if _, ok := token.(bool); ok && typ.Kind() != reflect.Bool {
			return errors.New("unexpected analytical definition boolean")
		}
		if _, ok := token.(json.Number); ok && (typ.Kind() < reflect.Int || typ.Kind() > reflect.Float64) {
			return errors.New("unexpected analytical definition number")
		}
	}
	return nil
}

func analysisDefinitionUnicodeEscapes(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		code, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
