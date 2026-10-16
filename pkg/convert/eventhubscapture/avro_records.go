package eventhubscapture

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/iskorotkov/avro/v2"
)

// Source formats the importer recognizes. A Capture file carries the
// Microsoft.ServiceBus.Messaging.EventData record (body + properties); any
// other Avro Object Container File is a plain data file whose records are the
// events themselves.
const (
	SourceFormatCapture     = "eventhubs_capture"
	SourceFormatAvroRecords = "avro_records"
)

// captureEnvelopeFields are the EventData fields besides Body. Capture decoding
// reads only the envelope, so it applies only to a schema made of nothing else.
var captureEnvelopeFields = map[string]bool{"SequenceNumber": true, "Offset": true, "EnqueuedTimeUtc": true, "SystemProperties": true, "Properties": true}

// isCaptureSchema reports whether the container's writer schema is the Event
// Hubs Capture EventData envelope: Body, at least one envelope field, and no
// other field. A record that merely has a "Body" (or a stray "Properties")
// field is a plain data record, and reading it as Capture would drop the rest.
func isCaptureSchema(schema avro.Schema) bool {
	record, ok := derefSchema(schema).(*avro.RecordSchema)
	if !ok {
		return false
	}
	hasBody, hasEnvelope := false, false
	for _, field := range record.Fields() {
		switch {
		case field.Name() == "Body":
			hasBody = true
		case captureEnvelopeFields[field.Name()]:
			hasEnvelope = true
		default:
			return false
		}
	}
	return hasBody && hasEnvelope
}

// avroSchemaName names a writer schema for warnings and stats: the full name
// of a named type, otherwise the primitive/complex type name.
func avroSchemaName(schema avro.Schema) string {
	schema = derefSchema(schema)
	if named, ok := schema.(avro.NamedSchema); ok {
		return named.FullName()
	}
	return string(schema.Type())
}

// recordBodyStats counts the lossy-looking spots a generic record body had to
// render, so the import can tell the reviewer about each one.
type recordBodyStats struct {
	base64Values   int
	nonFiniteFloat int
	invalidUTF8    int
}

func (s recordBodyStats) warnings() []string {
	var warnings []string
	if s.base64Values > 0 {
		warnings = append(warnings, fmt.Sprintf("%d bytes/fixed field value(s) were rendered as base64 strings in the JSON bodies", s.base64Values))
	}
	if s.nonFiniteFloat > 0 {
		warnings = append(warnings, fmt.Sprintf("%d NaN/Infinity float value(s) have no JSON number form and were rendered as the strings \"NaN\", \"Infinity\" or \"-Infinity\"", s.nonFiniteFloat))
	}
	if s.invalidUTF8 > 0 {
		warnings = append(warnings, fmt.Sprintf("%d string value(s) were not valid UTF-8 and were rendered with U+FFFD replacement characters", s.invalidUTF8))
	}
	return warnings
}

// renderAvroRecordBody turns one decoded generic Avro value into an event body.
// A string, enum or bytes value is the payload itself — also as the value of a
// top-level union such as ["null","bytes"]; every other value is rendered as
// JSON by walking the writer schema, so record fields keep their schema order,
// unions are unwrapped to their value, and longs stay exact.
func renderAvroRecordBody(schema avro.Schema, value any, stats *recordBodyStats) []byte {
	schema = derefSchema(schema)
	if union, ok := schema.(*avro.UnionSchema); ok && value != nil {
		if branch, inner, ok := unionBranch(union, value); ok {
			schema, value = derefSchema(branch), inner
		}
	}
	if value == nil {
		return nil
	}
	switch schema.Type() {
	case avro.String, avro.Enum:
		if text, ok := value.(string); ok {
			return []byte(text)
		}
	case avro.Bytes:
		if raw, ok := value.([]byte); ok {
			return raw
		}
	}
	w := avroJSONWriter{stats: stats}
	w.write(schema, value)
	return w.buf.Bytes()
}

type avroJSONWriter struct {
	buf   bytes.Buffer
	enc   *json.Encoder
	stats *recordBodyStats
}

func (w *avroJSONWriter) write(schema avro.Schema, value any) {
	schema = derefSchema(schema)
	if value == nil {
		w.buf.WriteString("null")
		return
	}
	logical := logicalTypeOf(schema)
	switch schema.Type() {
	case avro.Null:
		w.buf.WriteString("null")
	case avro.Boolean:
		if b, ok := value.(bool); ok {
			w.buf.WriteString(strconv.FormatBool(b))
			return
		}
		w.writeJSON(value)
	case avro.Int, avro.Long:
		w.writeInteger(logical, value)
	case avro.Float:
		if f, ok := value.(float32); ok {
			w.writeFloat(float64(f), 32)
			return
		}
		w.writeJSON(value)
	case avro.Double:
		if f, ok := value.(float64); ok {
			w.writeFloat(f, 64)
			return
		}
		w.writeJSON(value)
	case avro.String, avro.Enum:
		if text, ok := value.(string); ok {
			w.writeString(text)
			return
		}
		w.writeJSON(value)
	case avro.Bytes, avro.Fixed:
		w.writeBinary(schema, value)
	case avro.Array:
		items, ok := value.([]any)
		if !ok {
			w.writeJSON(value)
			return
		}
		itemSchema := schema.(*avro.ArraySchema).Items()
		w.buf.WriteByte('[')
		for i, item := range items {
			if i > 0 {
				w.buf.WriteByte(',')
			}
			w.write(itemSchema, item)
		}
		w.buf.WriteByte(']')
	case avro.Map:
		entries, ok := value.(map[string]any)
		if !ok {
			w.writeJSON(value)
			return
		}
		// Avro maps are unordered; sort so the body is deterministic.
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		valueSchema := schema.(*avro.MapSchema).Values()
		w.buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				w.buf.WriteByte(',')
			}
			w.writeString(key)
			w.buf.WriteByte(':')
			w.write(valueSchema, entries[key])
		}
		w.buf.WriteByte('}')
	case avro.Record:
		fields, ok := value.(map[string]any)
		if !ok {
			w.writeJSON(value)
			return
		}
		w.buf.WriteByte('{')
		for i, field := range schema.(*avro.RecordSchema).Fields() {
			if i > 0 {
				w.buf.WriteByte(',')
			}
			w.writeString(field.Name())
			w.buf.WriteByte(':')
			w.write(field.Type(), fields[field.Name()])
		}
		w.buf.WriteByte('}')
	case avro.Union:
		w.writeUnion(schema.(*avro.UnionSchema), value)
	default:
		w.writeJSON(value)
	}
}

func (w *avroJSONWriter) writeUnion(union *avro.UnionSchema, value any) {
	if branch, inner, ok := unionBranch(union, value); ok {
		w.write(branch, inner)
		return
	}
	w.writeJSON(value)
}

// unionBranch finds the branch a non-null union value belongs to. hamba hands
// back either the resolved native value or, when it cannot resolve the branch,
// a one-key map keyed by the branch's type name; both shapes are accepted.
func unionBranch(union *avro.UnionSchema, value any) (avro.Schema, any, bool) {
	branches := make([]avro.Schema, 0, len(union.Types()))
	for _, branch := range union.Types() {
		if derefSchema(branch).Type() != avro.Null {
			branches = append(branches, branch)
		}
	}
	if wrapped, ok := value.(map[string]any); ok && len(wrapped) == 1 {
		for key, inner := range wrapped {
			for _, branch := range branches {
				if unionBranchName(branch) == key {
					return branch, inner, true
				}
			}
		}
	}
	if len(branches) == 1 {
		return branches[0], value, true
	}
	for _, branch := range branches {
		if valueFitsSchema(branch, value) {
			return branch, value, true
		}
	}
	return nil, value, false
}

func (w *avroJSONWriter) writeInteger(logical avro.LogicalType, value any) {
	switch typed := value.(type) {
	case time.Time:
		switch logical {
		case avro.Date:
			w.buf.WriteString(strconv.FormatInt(int64(math.Floor(float64(typed.Unix())/86400)), 10))
		case avro.TimestampMicros, avro.LocalTimestampMicros:
			w.buf.WriteString(strconv.FormatInt(typed.UnixMicro(), 10))
		default:
			w.buf.WriteString(strconv.FormatInt(typed.UnixMilli(), 10))
		}
	case time.Duration:
		if logical == avro.TimeMicros {
			w.buf.WriteString(strconv.FormatInt(typed.Microseconds(), 10))
		} else {
			w.buf.WriteString(strconv.FormatInt(typed.Milliseconds(), 10))
		}
	case int:
		w.buf.WriteString(strconv.FormatInt(int64(typed), 10))
	case int32:
		w.buf.WriteString(strconv.FormatInt(int64(typed), 10))
	case int64:
		w.buf.WriteString(strconv.FormatInt(typed, 10))
	default:
		w.writeJSON(value)
	}
}

// writeFloat renders a finite float as a JSON number at its own precision.
// JSON has no NaN/Infinity, so those become the strings Jackson and most
// Avro JSON tooling emit ("NaN", "Infinity", "-Infinity").
func (w *avroJSONWriter) writeFloat(value float64, bits int) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		w.stats.nonFiniteFloat++
		label := "NaN"
		if math.IsInf(value, 1) {
			label = "Infinity"
		} else if math.IsInf(value, -1) {
			label = "-Infinity"
		}
		w.writeString(label)
		return
	}
	var encoded []byte
	if bits == 32 {
		encoded, _ = json.Marshal(float32(value))
	} else {
		encoded, _ = json.Marshal(value)
	}
	w.buf.Write(encoded)
}

func (w *avroJSONWriter) writeBinary(schema avro.Schema, value any) {
	switch typed := value.(type) {
	case *big.Rat:
		scale := 0
		if decimal, ok := logicalSchemaOf(schema).(*avro.DecimalLogicalSchema); ok {
			scale = decimal.Scale()
		}
		w.buf.WriteString(typed.FloatString(scale))
		return
	case avro.LogicalDuration:
		fmt.Fprintf(&w.buf, `{"months":%d,"days":%d,"milliseconds":%d}`, typed.Months, typed.Days, typed.Milliseconds)
		return
	}
	raw, ok := value.([]byte)
	if !ok {
		rv := reflect.ValueOf(value)
		if rv.Kind() != reflect.Array || rv.Type().Elem().Kind() != reflect.Uint8 {
			w.writeJSON(value)
			return
		}
		raw = make([]byte, rv.Len())
		for i := range raw {
			raw[i] = byte(rv.Index(i).Uint())
		}
	}
	w.stats.base64Values++
	w.writeString(base64.StdEncoding.EncodeToString(raw))
}

// writeString emits a JSON string without HTML escaping, so markup in a
// payload ("<script>", "&") keeps the bytes a JSON producer would send.
func (w *avroJSONWriter) writeString(text string) {
	if !utf8.ValidString(text) {
		w.stats.invalidUTF8++
	}
	w.writeJSON(text)
}

// writeJSON encodes straight into the body buffer with one reused encoder.
// Encode writes nothing on error and ends each value with a newline.
func (w *avroJSONWriter) writeJSON(value any) {
	if w.enc == nil {
		w.enc = json.NewEncoder(&w.buf)
		w.enc.SetEscapeHTML(false)
	}
	if err := w.enc.Encode(value); err != nil {
		w.buf.WriteString("null")
		return
	}
	w.buf.Truncate(w.buf.Len() - 1)
}

func valueFitsSchema(schema avro.Schema, value any) bool {
	switch derefSchema(schema).Type() {
	case avro.Boolean:
		_, ok := value.(bool)
		return ok
	case avro.Int:
		switch value.(type) {
		case int, int32, time.Time, time.Duration:
			return true
		}
	case avro.Long:
		switch value.(type) {
		case int64, time.Time, time.Duration:
			return true
		}
	case avro.Float:
		_, ok := value.(float32)
		return ok
	case avro.Double:
		_, ok := value.(float64)
		return ok
	case avro.String, avro.Enum:
		_, ok := value.(string)
		return ok
	case avro.Bytes:
		switch value.(type) {
		case []byte, *big.Rat:
			return true
		}
	case avro.Fixed:
		switch value.(type) {
		case *big.Rat, avro.LogicalDuration:
			return true
		}
		rv := reflect.ValueOf(value)
		return rv.Kind() == reflect.Array && rv.Type().Elem().Kind() == reflect.Uint8
	case avro.Array:
		_, ok := value.([]any)
		return ok
	case avro.Map, avro.Record:
		_, ok := value.(map[string]any)
		return ok
	}
	return false
}

// unionBranchName mirrors hamba's key for a wrapped union value: the full name
// of a named type, otherwise the type name plus any logical type.
func unionBranchName(schema avro.Schema) string {
	schema = derefSchema(schema)
	if named, ok := schema.(avro.NamedSchema); ok {
		return named.FullName()
	}
	name := string(schema.Type())
	if logical := logicalTypeOf(schema); logical != "" {
		name += "." + string(logical)
	}
	return name
}

func logicalSchemaOf(schema avro.Schema) avro.LogicalSchema {
	if typed, ok := derefSchema(schema).(avro.LogicalTypeSchema); ok {
		return typed.Logical()
	}
	return nil
}

func logicalTypeOf(schema avro.Schema) avro.LogicalType {
	if logical := logicalSchemaOf(schema); logical != nil {
		return logical.Type()
	}
	return ""
}

func derefSchema(schema avro.Schema) avro.Schema {
	if ref, ok := schema.(*avro.RefSchema); ok {
		return ref.Schema()
	}
	return schema
}
