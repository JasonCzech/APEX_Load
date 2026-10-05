package duckdb

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// Count a conservative encoded upper bound without materializing a complete
// JSON object. Optional fields are counted even when omitted. This protects all
// serialization/clone sites from many individually bounded large values.
func marshalWorkerJSON(value any) ([]byte, error) {
	v := reflect.ValueOf(value)
	if !v.IsValid() || preflightWorkerValue(v, "", 0, new(int)) != nil {
		return nil, errWorkerProtocol
	}
	remaining := int64(workerMaxFrameBytes)
	if workerEncodedBytes(v, 0, &remaining) != nil {
		return nil, errWorkerProtocol
	}
	body, err := json.Marshal(value)
	if err != nil || len(body) > workerMaxFrameBytes {
		return nil, errWorkerProtocol
	}
	return body, nil
}
func workerEncodedCharge(remaining *int64, n int64) error {
	if n < 0 || n > *remaining {
		return errWorkerProtocol
	}
	*remaining -= n
	return nil
}
func workerEncodedString(text string, remaining *int64) error {
	if workerEncodedCharge(remaining, 2) != nil {
		return errWorkerProtocol
	}
	for _, r := range text {
		n := int64(1)
		switch {
		case r == '"' || r == '\\':
			n = 2
		case r < 32 || r == '<' || r == '>' || r == '&' || r == 0x2028 || r == 0x2029:
			n = 6
		case r >= 0x10000:
			n = 4
		case r >= 0x800:
			n = 3
		case r >= 0x80:
			n = 2
		}
		if workerEncodedCharge(remaining, n) != nil {
			return errWorkerProtocol
		}
	}
	return nil
}
func workerEncodedBytes(value reflect.Value, depth int, remaining *int64) error {
	if depth > 64 {
		return errWorkerProtocol
	}
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return workerEncodedCharge(remaining, 4)
		}
		value = value.Elem()
	}
	if value.Type() == workerTimeType {
		return workerEncodedCharge(remaining, 40)
	}
	switch value.Kind() {
	case reflect.Struct:
		if workerEncodedCharge(remaining, 2) != nil {
			return errWorkerProtocol
		}
		for i := 0; i < value.NumField(); i++ {
			f := value.Type().Field(i)
			if f.PkgPath != "" {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if workerEncodedString(name, remaining) != nil || workerEncodedCharge(remaining, 2) != nil || workerEncodedBytes(value.Field(i), depth+1, remaining) != nil {
				return errWorkerProtocol
			}
		}
	case reflect.Slice:
		if value.IsNil() {
			return workerEncodedCharge(remaining, 4)
		}
		if workerEncodedCharge(remaining, 2+int64(value.Len())) != nil {
			return errWorkerProtocol
		}
		for i := 0; i < value.Len(); i++ {
			if workerEncodedBytes(value.Index(i), depth+1, remaining) != nil {
				return errWorkerProtocol
			}
		}
	case reflect.Map:
		if value.IsNil() {
			return workerEncodedCharge(remaining, 4)
		}
		if workerEncodedCharge(remaining, 2+int64(value.Len())*2) != nil {
			return errWorkerProtocol
		}
		iter := value.MapRange()
		for iter.Next() {
			if workerEncodedString(iter.Key().String(), remaining) != nil || workerEncodedBytes(iter.Value(), depth+1, remaining) != nil {
				return errWorkerProtocol
			}
		}
	case reflect.String:
		return workerEncodedString(value.String(), remaining)
	case reflect.Bool:
		return workerEncodedCharge(remaining, 5)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return workerEncodedCharge(remaining, int64(len(strconv.FormatInt(value.Int(), 10))))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return workerEncodedCharge(remaining, int64(len(strconv.FormatUint(value.Uint(), 10))))
	case reflect.Float32, reflect.Float64:
		return workerEncodedCharge(remaining, 32)
	default:
		return errWorkerProtocol
	}
	return nil
}
