package eventhubscapture

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/iskorotkov/avro/v2/ocf"
)

// TestConvertCaptureBase64EncodesBinaryBody covers eventhubs-3 (import half): a
// non-UTF-8 (binary) capture body must be base64-encoded with body_encoding set,
// not stored as a raw Go string (which JSON would corrupt via U+FFFD).
func TestConvertCaptureBase64EncodesBinaryBody(t *testing.T) {
	binary := "\xff\xfe\x00\x01\x02avro-ish"
	data := writeCapture(t, []map[string]any{captureRecord(1, "device-9", "sensor", binary)})

	res, err := Convert(ConvertRequest{Name: "bin", Data: data})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Stats.BinaryBodies != 1 {
		t.Fatalf("BinaryBodies = %d, want 1", res.Stats.BinaryBodies)
	}
	a := res.Script.Actions[0]
	if a.BodyEncoding != "base64" {
		t.Fatalf("BodyEncoding = %q, want base64", a.BodyEncoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(a.Body)
	if err != nil {
		t.Fatalf("body is not valid base64: %v", err)
	}
	if string(decoded) != binary {
		t.Fatalf("decoded body = %q, want original binary payload", decoded)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "binary") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a binary-body warning, got: %v", res.Warnings)
	}
}

// captureSchema is the well-known Event Hubs Capture Avro record schema.
const captureSchema = `{
  "type":"record","name":"EventData","namespace":"Microsoft.ServiceBus.Messaging",
  "fields":[
    {"name":"SequenceNumber","type":"long"},
    {"name":"Offset","type":"string"},
    {"name":"EnqueuedTimeUtc","type":"string"},
    {"name":"SystemProperties","type":{"type":"map","values":["long","double","string","bytes"]}},
    {"name":"Properties","type":{"type":"map","values":["long","double","string","bytes","null"]}},
    {"name":"Body","type":["null","bytes"]}
  ]
}`

// writeCapture encodes records into an Event Hubs Capture OCF buffer. Union
// values use hamba's wrapped form (map keyed by branch type, nil for null).
func writeCapture(t *testing.T, records []map[string]any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := ocf.NewEncoder(captureSchema, &buf)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func captureRecord(seq int64, partitionKey, source, body string) map[string]any {
	return map[string]any{
		"SequenceNumber":   seq,
		"Offset":           "100",
		"EnqueuedTimeUtc":  "2026-06-21T00:00:00Z",
		"SystemProperties": map[string]any{"x-opt-partition-key": map[string]any{"string": partitionKey}},
		"Properties":       map[string]any{"source": map[string]any{"string": source}},
		"Body":             map[string]any{"bytes": []byte(body)},
	}
}

func TestConvertCaptureRoundTrip(t *testing.T) {
	data := writeCapture(t, []map[string]any{
		captureRecord(1, "device-7", "sensor", `{"deviceId":"device-7","temp":21}`),
		captureRecord(2, "device-8", "sensor", `{"deviceId":"device-8","temp":22}`),
	})

	res, err := Convert(ConvertRequest{Name: "from-capture", Data: data})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	s := res.Script
	if s == nil || s.Protocol != "eventhubs" {
		t.Fatalf("expected eventhubs script, got %+v", s)
	}
	if !s.Config.EventHubs.Mock || s.Config.EventHubs.EventHub != "captured-events" {
		t.Fatalf("expected mock eventhubs config, got %+v", s.Config.EventHubs)
	}
	if len(s.Actions) != 2 {
		t.Fatalf("expected 2 produce actions, got %d", len(s.Actions))
	}
	a0 := s.Actions[0]
	if a0.Type != "eventhubs_produce" {
		t.Fatalf("action type = %q, want eventhubs_produce", a0.Type)
	}
	if a0.Body != `{"deviceId":"device-7","temp":21}` {
		t.Fatalf("body = %q, want captured payload", a0.Body)
	}
	if a0.Headers["source"] != "sensor" {
		t.Fatalf("header source = %q, want sensor", a0.Headers["source"])
	}
	if pk, _ := a0.Params["partition_key"].(string); pk != "device-7" {
		t.Fatalf("partition_key = %q, want device-7", pk)
	}
	if res.Stats.RecordsRead != 2 || res.Stats.ActionsEmitted != 2 || res.Stats.PartitionKeys != 2 {
		t.Fatalf("unexpected stats: %+v", res.Stats)
	}
}

func TestConvertCaptureMakesNonFiniteApplicationPropertiesJSONSafe(t *testing.T) {
	record := captureRecord(1, "device-9", "sensor", "payload")
	record["Properties"] = map[string]any{
		"nan":     map[string]any{"double": math.NaN()},
		"pos_inf": map[string]any{"double": math.Inf(1)},
		"neg_inf": map[string]any{"double": math.Inf(-1)},
	}
	result, err := Convert(ConvertRequest{Name: "non-finite", Data: writeCapture(t, []map[string]any{record})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatalf("converted capture is not JSON-safe: %v", err)
	}
	unmapped, _ := result.Script.Actions[0].Metadata["eventhubs_capture_unmapped_properties"].(map[string]any)
	for name, want := range map[string]string{
		"nan": "NaN", "pos_inf": "+Infinity", "neg_inf": "-Infinity",
	} {
		descriptor, _ := unmapped[name].(map[string]any)
		if descriptor["value"] != want {
			t.Fatalf("%s descriptor = %#v, want JSON-safe label %q", name, descriptor, want)
		}
		bits, _ := descriptor["ieee754_bits_hex"].(string)
		if len(bits) != 16 {
			t.Fatalf("%s descriptor lost IEEE-754 bits: %#v", name, descriptor)
		}
	}
	descriptor, _ := unmapped["nan"].(map[string]any)
	if descriptor["ieee754_bits_hex"] != fmt.Sprintf("%016x", math.Float64bits(math.NaN())) {
		t.Fatalf("NaN bit preservation = %#v", descriptor)
	}
}

func TestConvertCaptureMaxEventsCap(t *testing.T) {
	var records []map[string]any
	for i := int64(1); i <= 5; i++ {
		records = append(records, captureRecord(i, "k", "s", "body"))
	}
	data := writeCapture(t, records)

	res, err := Convert(ConvertRequest{Data: data, MaxEvents: 2})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Script.Actions) != 2 {
		t.Fatalf("expected 2 actions (capped), got %d", len(res.Script.Actions))
	}
	if res.Stats.RecordsRead != 5 {
		t.Fatalf("expected all 5 records counted, got %d", res.Stats.RecordsRead)
	}
	if !hasWarningContaining(res.Warnings, "raise max_events") {
		t.Fatalf("expected a cap warning, got %v", res.Warnings)
	}
}

func TestConvertCaptureNoPartitionKeyWarns(t *testing.T) {
	rec := map[string]any{
		"SequenceNumber":   int64(1),
		"Offset":           "0",
		"EnqueuedTimeUtc":  "2026-06-21T00:00:00Z",
		"SystemProperties": map[string]any{},
		"Properties":       map[string]any{},
		"Body":             map[string]any{"bytes": []byte("payload")},
	}
	res, err := Convert(ConvertRequest{Data: writeCapture(t, []map[string]any{rec})})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Stats.PartitionKeys != 0 {
		t.Fatalf("expected 0 partition keys, got %d", res.Stats.PartitionKeys)
	}
	if _, hasPK := res.Script.Actions[0].Params["partition_key"]; hasPK {
		t.Fatal("did not expect a partition_key param without a captured key")
	}
	if !hasWarningContaining(res.Warnings, "no partition keys") {
		t.Fatalf("expected a no-partition-key warning, got %v", res.Warnings)
	}
}

func TestConvertCaptureEmptyBodyWarns(t *testing.T) {
	rec := map[string]any{
		"SequenceNumber":   int64(1),
		"Offset":           "0",
		"EnqueuedTimeUtc":  "2026-06-21T00:00:00Z",
		"SystemProperties": map[string]any{},
		"Properties":       map[string]any{},
		"Body":             nil, // null branch
	}
	res, err := Convert(ConvertRequest{Data: writeCapture(t, []map[string]any{rec})})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Stats.EmptyBodies != 1 {
		t.Fatalf("expected 1 empty body, got %d", res.Stats.EmptyBodies)
	}
	if !hasWarningContaining(res.Warnings, "empty body") {
		t.Fatalf("expected an empty-body warning, got %v", res.Warnings)
	}
	if got := res.Script.Actions[0].Assertions; len(got) != 0 {
		t.Fatalf("empty captured event has deterministically failing assertions: %+v", got)
	}
}

func TestConvertCaptureRejectsNonAvro(t *testing.T) {
	if _, err := Convert(ConvertRequest{Data: []byte("not an avro file")}); err == nil {
		t.Fatal("expected an error for non-Avro input")
	}
	if _, err := Convert(ConvertRequest{Data: nil}); err == nil {
		t.Fatal("expected an error for empty input")
	}
}

func TestSupportMatrixNonEmpty(t *testing.T) {
	if len(GetSupportMatrix()) == 0 {
		t.Fatal("support matrix is empty")
	}
	if len(GetSupportMatrixNotes()) == 0 {
		t.Fatal("support matrix notes are empty")
	}
}

func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
