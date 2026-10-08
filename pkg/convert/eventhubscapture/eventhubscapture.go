// Package eventhubscapture imports Azure Event Hubs Capture files (Avro Object
// Container Files) into a native eventhubs producer script. Capture writes the
// real events that flowed through a hub — body, application properties, and
// partition key — so the import seeds a load script from production-shaped
// payloads. Any other Avro data file is accepted too: each record is the event,
// rendered as a JSON body. The generated script is mock-mode by default; point
// it at a real namespace (and build -tags eventhubs_real) to drive load.
package eventhubscapture

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/hamba/avro/v2"
	"github.com/hamba/avro/v2/ocf"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// defaultMaxEvents caps how many captured events become produce actions, so a
// multi-gigabyte capture does not yield an unusable script. Records beyond the
// cap are still counted (and reported) but not emitted.
const defaultMaxEvents = 25

// maxMaxEvents bounds an explicit max_events. Every seeded event is a produce
// action carried in the draft response and scanned by the shared import passes,
// so an unbounded request would turn one upload into a huge draft.
const maxMaxEvents = 1000

// partitionKeyProperty is the Event Hubs system-property key carrying the
// producer-supplied partition key.
const partitionKeyProperty = "x-opt-partition-key"

// ConvertRequest is the input to Convert.
type ConvertRequest struct {
	Name      string
	ProjectID string
	EventHub  string // optional; defaults to "captured-events"
	Data      []byte
	MaxEvents int
}

// Stats summarizes what the import produced.
type Stats struct {
	// SourceFormat is SourceFormatCapture for a Capture EventData file and
	// SourceFormatAvroRecords for any other Avro data file.
	SourceFormat          string `json:"source_format"`
	AvroSchema            string `json:"avro_schema,omitempty"`
	RecordsRead           int    `json:"records_read"`
	ActionsEmitted        int    `json:"actions_emitted"`
	PartitionKeys         int    `json:"partition_keys"`
	EmptyBodies           int    `json:"empty_bodies"`
	BinaryBodies          int    `json:"binary_bodies"`
	ReviewGatedProperties int    `json:"review_gated_properties,omitempty"`
}

// ConvertResult is the import output (mirrors the other importer result shapes).
type ConvertResult struct {
	Script   *dsl.Script `json:"script"`
	Warnings []string    `json:"warnings,omitempty"`
	Stats    Stats       `json:"stats"`
	Summary  string      `json:"summary"`
}

// Convert decodes an Avro container into an eventhubs script. A Capture file
// (the EventData envelope) seeds each captured event with its body, string
// properties and partition key. Any other Avro data file has no envelope — each
// record is itself the event — so the record becomes the body: a string or
// bytes value verbatim, anything else as JSON in schema field order.
func Convert(req ConvertRequest) (*ConvertResult, error) {
	if len(req.Data) == 0 {
		return nil, fmt.Errorf("empty capture file")
	}
	var warnings []string
	maxEvents := req.MaxEvents
	if maxEvents <= 0 {
		maxEvents = defaultMaxEvents
	}
	if maxEvents > maxMaxEvents {
		warnings = append(warnings, fmt.Sprintf("max_events %d exceeds the import limit of %d; seeded at most %d events", maxEvents, maxMaxEvents, maxMaxEvents))
		maxEvents = maxMaxEvents
	}

	file, err := openContainer(req.Data, maxEvents)
	if err != nil {
		return nil, fmt.Errorf("not a valid Event Hubs Capture Avro file: %w", err)
	}
	defer func() { _ = file.Close() }()

	eventHub := strings.TrimSpace(req.EventHub)
	if eventHub == "" {
		eventHub = "captured-events"
	}
	schema := file.Schema()
	schemaName := avroSchemaName(schema)
	capture := isCaptureSchema(schema)
	stats := Stats{SourceFormat: SourceFormatAvroRecords, AvroSchema: schemaName, RecordsRead: file.records}
	if capture {
		stats.SourceFormat = SourceFormatCapture
	}

	var (
		actions   []dsl.Action
		bodyStats recordBodyStats
		problem   = file.problem
	)
	// Decoding stops at the cap: the block headers already counted every record,
	// so the blocks past it are never decompressed.
	for len(actions) < maxEvents && file.HasNext() {
		var (
			action dsl.Action
			err    error
		)
		if capture {
			action, err = decodeCaptureEvent(file.Decoder, eventHub, len(actions)+1, &stats)
		} else {
			action, err = decodeRecordEvent(file.Decoder, schema, schemaName, eventHub, len(actions)+1, &stats, &bodyStats)
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to decode %s %d: %v", eventNoun(capture, 1), len(actions)+1, err))
			if problem == "" {
				problem = err.Error()
			}
			break
		}
		actions = append(actions, action)
	}
	if err := file.Error(); err != nil && problem == "" {
		warnings = append(warnings, "Avro stream ended with an error: "+err.Error())
		problem = err.Error()
	}
	if file.problem != "" {
		warnings = append(warnings, file.problem)
	}
	if len(actions) == 0 {
		reason := "capture file contained no events"
		if !capture {
			reason = "Avro file contained no records"
		}
		if problem != "" {
			reason += ": " + problem
		}
		return nil, errors.New(reason)
	}
	stats.ActionsEmitted = len(actions)
	if stats.RecordsRead < len(actions) {
		stats.RecordsRead = len(actions)
	}

	if !capture {
		warnings = append([]string{fmt.Sprintf("Avro schema %q is not the Event Hubs Capture EventData schema, so each record was imported as one event body (%s); the file carries no application properties or partition keys, so produced events use namespace default routing", schemaName, recordBodyDescription(schema))}, warnings...)
	}
	if stats.RecordsRead > len(actions) && len(actions) == maxEvents {
		subject := "capture"
		if !capture {
			subject = "file"
		}
		warnings = append(warnings, fmt.Sprintf("%s contained %d %s; seeded the first %d as produce actions (raise max_events to import more)", subject, stats.RecordsRead, eventNoun(capture, stats.RecordsRead), len(actions)))
	}
	if stats.EmptyBodies > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s had an empty body", stats.EmptyBodies, eventNoun(capture, stats.EmptyBodies)))
	}
	if stats.BinaryBodies > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s had a non-UTF-8 (binary) body; imported as base64 (body_encoding=base64) so the payload round-trips losslessly — verify before promotion", stats.BinaryBodies, eventNoun(capture, stats.BinaryBodies)))
	}
	if stats.ReviewGatedProperties > 0 {
		warnings = append(warnings, fmt.Sprintf("%d typed or binary application propert%s could not be represented by string headers; preserved in action metadata and review-gated instead of being lossy-stringified", stats.ReviewGatedProperties, map[bool]string{true: "y", false: "ies"}[stats.ReviewGatedProperties == 1]))
	}
	if capture && stats.PartitionKeys == 0 {
		warnings = append(warnings, "no partition keys found in the capture; produced events use namespace default routing (set params.partition_id to measure per-partition skew)")
	}
	warnings = append(warnings, bodyStats.warnings()...)

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "eventhubs-capture-import"
		if !capture {
			name = "avro-records-import"
		}
	}
	script := &dsl.Script{
		Name:      name,
		Protocol:  "eventhubs",
		ProjectID: req.ProjectID,
		Actions:   actions,
	}
	script.Config.EventHubs = dsl.EventHubsConfig{Mock: true, EventHub: eventHub}

	summary := fmt.Sprintf("Imported %d of %d captured Event Hubs events into produce actions for event hub %q (mock mode).", len(actions), stats.RecordsRead, eventHub)
	if !capture {
		summary = fmt.Sprintf("Imported %d of %d Avro %q records as event bodies in produce actions for event hub %q (mock mode).", len(actions), stats.RecordsRead, schemaName, eventHub)
	}
	return &ConvertResult{Script: script, Warnings: warnings, Stats: stats, Summary: summary}, nil
}

// decodeCaptureEvent reads one EventData record into a produce action carrying
// its body, string application properties and partition key.
func decodeCaptureEvent(dec *ocf.Decoder, eventHub string, index int, stats *Stats) (dsl.Action, error) {
	var rec map[string]any
	if err := dec.Decode(&rec); err != nil {
		return dsl.Action{}, err
	}
	props, unsupportedProps := decodeCaptureApplicationProperties(rec["Properties"])
	sysProps := decodeStringMap(rec["SystemProperties"])
	partitionKey := firstNonEmpty(sysProps[partitionKeyProperty], props[partitionKeyProperty])

	action := dsl.Action{
		Name:     fmt.Sprintf("publish-captured-%d", index),
		Type:     "eventhubs_produce",
		Target:   eventHub,
		Metadata: map[string]any{"eventhubs_capture": true},
	}
	setEventBody(&action, decodeBytes(rec["Body"]), stats)
	if seq, ok := rec["SequenceNumber"]; ok {
		action.Metadata["source_sequence_number"] = seq
	}
	if len(props) > 0 {
		action.Headers = props
	}
	if len(unsupportedProps) > 0 {
		action.Metadata["eventhubs_capture_unmapped_properties"] = unsupportedProps
		action.Metadata["execution_blocker"] = "import_review_required"
		action.Metadata["execution_blockers"] = []string{"import_review_required"}
		action.Metadata["import_review"] = map[string]any{
			"version":      1,
			"source":       "eventhubs_capture",
			"reason_codes": []string{"typed_application_properties_require_mapping"},
		}
		stats.ReviewGatedProperties += len(unsupportedProps)
	}
	if partitionKey != "" {
		action.Params = map[string]any{"partition_key": partitionKey}
		stats.PartitionKeys++
	}
	return action, nil
}

// decodeRecordEvent reads one record of a non-Capture Avro file into a produce
// action whose body is the record itself.
func decodeRecordEvent(dec *ocf.Decoder, schema avro.Schema, schemaName, eventHub string, index int, stats *Stats, bodyStats *recordBodyStats) (dsl.Action, error) {
	var record any
	if err := dec.Decode(&record); err != nil {
		return dsl.Action{}, err
	}
	action := dsl.Action{
		Name:     fmt.Sprintf("publish-record-%d", index),
		Type:     "eventhubs_produce",
		Target:   eventHub,
		Metadata: map[string]any{"eventhubs_capture": true, "avro_schema": schemaName, "source_record_index": index},
	}
	setEventBody(&action, renderAvroRecordBody(schema, record, bodyStats), stats)
	return action, nil
}

// setEventBody stores a produce payload. Binary payloads (Avro/protobuf/
// compressed) would be irreversibly mangled by JSON's invalid-UTF-8 -> U+FFFD
// replacement when the draft is returned, so they are base64-encoded; the
// eventhubs runtime base64-decodes body_encoding=base64 on send. An empty body
// gets no message_received assertion, which would deterministically fail.
func setEventBody(action *dsl.Action, body []byte, stats *Stats) {
	switch {
	case len(body) == 0:
		stats.EmptyBodies++
		return
	case !utf8.Valid(body):
		action.Body = base64.StdEncoding.EncodeToString(body)
		action.BodyEncoding = "base64"
		stats.BinaryBodies++
	default:
		action.Body = string(body)
	}
	action.Assertions = []dsl.Assertion{{Type: "message_received"}}
}

func eventNoun(capture bool, count int) string {
	switch {
	case capture && count == 1:
		return "captured event"
	case capture:
		return "captured events"
	case count == 1:
		return "record"
	default:
		return "records"
	}
}

func recordBodyDescription(schema avro.Schema) string {
	switch derefSchema(schema).Type() {
	case avro.String, avro.Enum:
		return "the string value is the body"
	case avro.Bytes:
		return "the bytes value is the body"
	case avro.Union:
		return "string and bytes values are the body, other values are rendered as JSON"
	default:
		return "rendered as JSON in schema field order"
	}
}

// unwrapUnion collapses hamba's generic union representation. hamba decodes a
// non-null Avro union value into a single-key map keyed by the branch type name
// (e.g. {"bytes": [...]}, {"string": "v"}); a null branch decodes to nil. For
// any other value it is returned unchanged.
func unwrapUnion(v any) any {
	if m, ok := v.(map[string]any); ok && len(m) == 1 {
		for _, inner := range m {
			return inner
		}
	}
	return v
}

func decodeBytes(v any) []byte {
	switch b := unwrapUnion(v).(type) {
	case []byte:
		return b
	case string:
		return []byte(b)
	default:
		return nil
	}
}

func decodeStringMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for key, val := range m {
		unwrapped := unwrapUnion(val)
		if unwrapped == nil {
			continue
		}
		if b, ok := unwrapped.([]byte); ok && utf8.Valid(b) {
			out[key] = string(b)
			continue
		}
		if text, ok := unwrapped.(string); ok {
			out[key] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func decodeCaptureApplicationProperties(v any) (map[string]string, map[string]any) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, nil
	}
	stringsOnly := make(map[string]string)
	unsupported := make(map[string]any)
	for key, raw := range m {
		value := unwrapUnion(raw)
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			stringsOnly[key] = text
			continue
		}
		descriptor := map[string]any{"type": capturePropertyType(value)}
		switch typed := value.(type) {
		case []byte:
			descriptor["base64"] = base64.StdEncoding.EncodeToString(typed)
		case float32:
			if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
				descriptor["value"] = nonFiniteCaptureFloatLabel(float64(typed))
				descriptor["ieee754_bits_hex"] = fmt.Sprintf("%08x", math.Float32bits(typed))
			} else {
				descriptor["value"] = typed
			}
		case float64:
			if math.IsNaN(typed) || math.IsInf(typed, 0) {
				descriptor["value"] = nonFiniteCaptureFloatLabel(typed)
				descriptor["ieee754_bits_hex"] = fmt.Sprintf("%016x", math.Float64bits(typed))
			} else {
				descriptor["value"] = typed
			}
		default:
			descriptor["value"] = typed
		}
		unsupported[key] = descriptor
	}
	if len(stringsOnly) == 0 {
		stringsOnly = nil
	}
	if len(unsupported) == 0 {
		unsupported = nil
	}
	return stringsOnly, unsupported
}

func nonFiniteCaptureFloatLabel(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "+Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	default:
		return ""
	}
}

func capturePropertyType(value any) string {
	switch value.(type) {
	case []byte:
		return "bytes"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "integer"
	case float32, float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
