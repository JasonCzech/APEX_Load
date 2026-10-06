package duckdb

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var workerTimeType = reflect.TypeOf(time.Time{})
var workerCellsType = reflect.TypeOf([]workerCell{})

// Bootstrap travels only on the separate private parent-owned channel.
func readWorkerBootstrap(reader io.Reader) (workerBootstrap, error) {
	var bootstrap workerBootstrap
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return bootstrap, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size < 2 || size > workerMaxFrameBytes {
		return bootstrap, errWorkerProtocol
	}
	body := make([]byte, int(size))
	if _, err := io.ReadFull(reader, body); err != nil {
		return bootstrap, err
	}
	if decodeWorkerJSON(body, &bootstrap) != nil || bootstrap.validate() != nil {
		return workerBootstrap{}, errWorkerProtocol
	}
	return bootstrap, nil
}
func writeWorkerBootstrap(writer io.Writer, bootstrap workerBootstrap) error {
	if bootstrap.validate() != nil || preflightWorkerValue(reflect.ValueOf(bootstrap), "", 0, new(int)) != nil {
		return errWorkerProtocol
	}
	body, err := marshalWorkerJSON(bootstrap)
	if err != nil || len(body) > workerMaxFrameBytes {
		return errWorkerProtocol
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if err := writeWorkerBytes(writer, header[:]); err != nil {
		return err
	}
	return writeWorkerBytes(writer, body)
}

// The size header is checked before make. No trust is derived from a peer's
// count field; collection limits are also walked before typed unmarshalling.
func readWorkerFrame(reader io.Reader) (workerFrame, error) {
	var frame workerFrame
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return frame, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size < 2 || size > workerMaxFrameBytes {
		return frame, errWorkerProtocol
	}
	body := make([]byte, int(size))
	if _, err := io.ReadFull(reader, body); err != nil {
		return frame, err
	}
	if decodeWorkerJSON(body, &frame) != nil || frame.validate() != nil {
		return workerFrame{}, errWorkerProtocol
	}
	return frame, nil
}
func writeWorkerFrame(writer io.Writer, frame workerFrame) error {
	if frame.validate() != nil {
		return errWorkerProtocol
	}
	if preflightWorkerValue(reflect.ValueOf(frame), "", 0, new(int)) != nil {
		return errWorkerProtocol
	}
	body, err := marshalWorkerJSON(frame)
	if err != nil || len(body) > workerMaxFrameBytes {
		return errWorkerProtocol
	}
	// Writing and reading share the exact same canonical grammar.
	var validated workerFrame
	if decodeWorkerJSON(body, &validated) != nil {
		return errWorkerProtocol
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if err := writeWorkerBytes(writer, header[:]); err != nil {
		return err
	}
	return writeWorkerBytes(writer, body)
}
func writeWorkerBytes(writer io.Writer, body []byte) error {
	for len(body) > 0 {
		n, err := writer.Write(body)
		if n < 0 || n > len(body) {
			return io.ErrShortWrite
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		body = body[n:]
	}
	return nil
}
func decodeWorkerJSON(body []byte, destination any) error {
	typeOf := reflect.TypeOf(destination)
	if typeOf == nil || typeOf.Kind() != reflect.Pointer || reflect.ValueOf(destination).IsNil() || len(body) < 2 || len(body) > workerMaxFrameBytes || !utf8.Valid(body) || !workerRawScalarBounds(body) {
		return errWorkerProtocol
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	nodes := 0
	if preflightWorkerJSON(decoder, typeOf.Elem(), "", 0, &nodes) != nil {
		return errWorkerProtocol
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errWorkerProtocol
	}
	decoder = json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errWorkerProtocol
	}
	return nil
}

// Reject excessive scalar lexemes and ambiguous surrogate escapes before
// json.Decoder.Token materializes a string/number. The frame itself is bounded.
func workerRawScalarBounds(body []byte) bool {
	for i := 0; i < len(body); i++ {
		if body[i] == '"' {
			count, closed := 0, false
			for i++; i < len(body); i++ {
				if body[i] == '"' {
					closed = true
					break
				}
				if body[i] != '\\' {
					count++
				} else {
					i++
					if i >= len(body) {
						return false
					}
					if body[i] != 'u' {
						count++
					} else {
						if i+4 >= len(body) {
							return false
						}
						n, err := strconv.ParseUint(string(body[i+1:i+5]), 16, 16)
						if err != nil {
							return false
						}
						i += 4
						switch {
						case n >= 0xdc00 && n <= 0xdfff:
							return false
						case n >= 0xd800 && n <= 0xdbff:
							if i+6 >= len(body) || body[i+1] != '\\' || body[i+2] != 'u' {
								return false
							}
							low, err := strconv.ParseUint(string(body[i+3:i+7]), 16, 16)
							if err != nil || low < 0xdc00 || low > 0xdfff {
								return false
							}
							i += 6
							count += 4
						case n < 0x80:
							count++
						case n < 0x800:
							count += 2
						default:
							count += 3
						}
					}
				}
				if count > workerMaxScalarTextBytes {
					return false
				}
			}
			if !closed {
				return false
			}
		} else if body[i] == '-' || body[i] >= '0' && body[i] <= '9' {
			start := i
			for i < len(body) && (body[i] >= '0' && body[i] <= '9' || strings.ContainsRune(".eE+-", rune(body[i]))) {
				i++
				if i-start > 32 {
					return false
				}
			}
			i--
		}
	}
	return true
}
func workerArrayLimit(field string, element reflect.Type) int {
	if element == workerCellsType {
		return 20
	}
	switch field {
	case "handles":
		return workerMaxInputs + workerMaxOutputs
	case "input_handles":
		return workerMaxInputs
	case "output_handles", "outputs":
		return workerMaxOutputs
	case "columns":
		return 1024
	case "measures":
		return 64
	case "path_components", "timestamp", "value", "name_column", "derivation", "source", "host", "service", "dimensions":
		return 16
	case "samples":
		return 1024 // each sample row's cells
	case "points":
		return 10000
	case "rejections":
		return 1024
	case "rows":
		return 100 // rejection ordinals
	default:
		return 1024
	}
}
func preflightWorkerJSON(decoder *json.Decoder, t reflect.Type, field string, depth int, nodes *int) error {
	*nodes++
	if depth > 64 || *nodes > 1000000 {
		return errWorkerProtocol
	}
	nullable := t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	token, err := decoder.Token()
	if err != nil {
		return errWorkerProtocol
	}
	if token == nil {
		if nullable {
			return nil
		}
		return errWorkerProtocol
	}
	if text, ok := token.(string); ok && len(text) > workerMaxScalarTextBytes {
		return errWorkerProtocol
	}
	if number, ok := token.(json.Number); ok && len(number) > 32 {
		return errWorkerProtocol
	}
	if t == workerTimeType {
		text, ok := token.(string)
		if !ok {
			return errWorkerProtocol
		}
		if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
			return errWorkerProtocol
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			return errWorkerProtocol
		}
		fields := map[string]reflect.StructField{}
		required := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			name := tag[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			fields[name] = f
			optional := false
			for _, flag := range tag[1:] {
				if flag == "omitempty" || flag == "omitzero" {
					optional = true
				}
			}
			if !optional {
				required[name] = true
			}
		}
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || len(key) > 256 || seen[key] {
				return errWorkerProtocol
			}
			f, known := fields[key]
			if !known {
				return errWorkerProtocol
			}
			seen[key] = true
			if preflightWorkerJSON(decoder, f.Type, key, depth+1, nodes) != nil {
				return errWorkerProtocol
			}
		}
		for key := range required {
			if !seen[key] {
				return errWorkerProtocol
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errWorkerProtocol
		}
	case reflect.Slice:
		if token != json.Delim('[') {
			return errWorkerProtocol
		}
		count := 0
		limit := workerArrayLimit(field, t.Elem())
		for decoder.More() {
			count++
			if count > limit {
				return errWorkerProtocol
			}
			if preflightWorkerJSON(decoder, t.Elem(), field, depth+1, nodes) != nil {
				return errWorkerProtocol
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errWorkerProtocol
		}
	case reflect.Map:
		if t.Key().Kind() != reflect.String || t.Elem().Kind() != reflect.String || token != json.Delim('{') {
			return errWorkerProtocol
		}
		count := 0
		seen := map[string]bool{}
		for decoder.More() {
			count++
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if count > 16 || err != nil || !ok || len(key) > 128 || seen[key] {
				return errWorkerProtocol
			}
			seen[key] = true
			if preflightWorkerJSON(decoder, t.Elem(), field, depth+1, nodes) != nil {
				return errWorkerProtocol
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errWorkerProtocol
		}
	case reflect.String:
		if _, ok := token.(string); !ok {
			return errWorkerProtocol
		}
	case reflect.Bool:
		if _, ok := token.(bool); !ok {
			return errWorkerProtocol
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number, ok := token.(json.Number)
		if !ok {
			return errWorkerProtocol
		}
		if _, err := strconv.ParseInt(string(number), 10, t.Bits()); err != nil {
			return errWorkerProtocol
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number, ok := token.(json.Number)
		if !ok {
			return errWorkerProtocol
		}
		if _, err := strconv.ParseUint(string(number), 10, t.Bits()); err != nil {
			return errWorkerProtocol
		}
	case reflect.Float32, reflect.Float64:
		number, ok := token.(json.Number)
		if !ok {
			return errWorkerProtocol
		}
		value, err := strconv.ParseFloat(string(number), t.Bits())
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return errWorkerProtocol
		}
	default:
		return errWorkerProtocol
	}
	return nil
}

// Child/parent writes enforce the same bounds before Marshal allocates a frame.
func preflightWorkerValue(value reflect.Value, field string, depth int, nodes *int) error {
	*nodes++
	if depth > 64 || *nodes > 1000000 {
		return errWorkerProtocol
	}
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if value.Type() == workerTimeType {
		return nil
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			f := value.Type().Field(i)
			if f.PkgPath != "" {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			if preflightWorkerValue(value.Field(i), name, depth+1, nodes) != nil {
				return errWorkerProtocol
			}
		}
	case reflect.Slice:
		if value.Len() > workerArrayLimit(field, value.Type().Elem()) {
			return errWorkerProtocol
		}
		for i := 0; i < value.Len(); i++ {
			if preflightWorkerValue(value.Index(i), field, depth+1, nodes) != nil {
				return errWorkerProtocol
			}
		}
	case reflect.Map:
		if value.Len() > 16 || value.Type().Key().Kind() != reflect.String || value.Type().Elem().Kind() != reflect.String {
			return errWorkerProtocol
		}
		iter := value.MapRange()
		for iter.Next() {
			if len(iter.Key().String()) > 128 || preflightWorkerValue(iter.Value(), field, depth+1, nodes) != nil {
				return errWorkerProtocol
			}
		}
	case reflect.String:
		if len(value.String()) > workerMaxScalarTextBytes || !utf8.ValidString(value.String()) {
			return errWorkerProtocol
		}
	case reflect.Float32, reflect.Float64:
		if math.IsInf(value.Float(), 0) || math.IsNaN(value.Float()) {
			return errWorkerProtocol
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
	default:
		return errWorkerProtocol
	}
	return nil
}
