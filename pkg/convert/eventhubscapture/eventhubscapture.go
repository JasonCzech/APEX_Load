// Package eventhubscapture imports Azure Event Hubs Capture files (Avro Object
// Container Files) into a native eventhubs producer script. Capture writes the
// real events that flowed through a hub — body, application properties, and
// partition key — so the import seeds a load script from production-shaped
// payloads. The generated script is mock-mode by default; point it at a real
// namespace (and build -tags eventhubs_real) to drive load.
package eventhubscapture

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hamba/avro/v2/ocf"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// defaultMaxEvents caps how many captured events become produce actions, so a
// multi-gigabyte capture does not yield an unusable script. Records beyond the
// cap are still counted (and reported) but not emitted.
const defaultMaxEvents = 25

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
	RecordsRead    int `json:"records_read"`
	ActionsEmitted int `json:"actions_emitted"`
	PartitionKeys  int `json:"partition_keys"`
	EmptyBodies    int `json:"empty_bodies"`
	BinaryBodies   int `json:"binary_bodies"`
}

// ConvertResult is the import output (mirrors the other importer result shapes).
type ConvertResult struct {
	Script   *dsl.Script `json:"script"`
	Warnings []string    `json:"warnings,omitempty"`
	Stats    Stats       `json:"stats"`
	Summary  string      `json:"summary"`
}

// Convert decodes an Event Hubs Capture Avro container into an eventhubs script.
func Convert(req ConvertRequest) (*ConvertResult, error) {
	if len(req.Data) == 0 {
		return nil, fmt.Errorf("empty capture file")
	}
	maxEvents := req.MaxEvents
	if maxEvents <= 0 {
		maxEvents = defaultMaxEvents
	}

	dec, err := ocf.NewDecoder(bytes.NewReader(req.Data))
	if err != nil {
		return nil, fmt.Errorf("not a valid Event Hubs Capture Avro file: %w", err)
	}

	eventHub := strings.TrimSpace(req.EventHub)
	if eventHub == "" {
		eventHub = "captured-events"
	}

	var (
		actions       []dsl.Action
		warnings      []string
		recordsRead   int
		partitionKeys int
		emptyBodies   int
		binaryBodies  int
	)
	for dec.HasNext() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			warnings = append(warnings, "failed to decode a capture record: "+err.Error())
			break
		}
		recordsRead++
		if len(actions) >= maxEvents {
			continue
		}
		body := decodeBytes(rec["Body"])
		if len(body) == 0 {
			emptyBodies++
		}
		props := decodeStringMap(rec["Properties"])
		sysProps := decodeStringMap(rec["SystemProperties"])
		partitionKey := firstNonEmpty(sysProps[partitionKeyProperty], props[partitionKeyProperty])

		action := dsl.Action{
			Name:     fmt.Sprintf("publish-captured-%d", len(actions)+1),
			Type:     "eventhubs_produce",
			Target:   eventHub,
			Metadata: map[string]any{"eventhubs_capture": true},
		}
		// Binary payloads (Avro/protobuf/compressed) would be irreversibly mangled
		// by JSON's invalid-UTF-8 -> U+FFFD replacement when the draft is returned.
		// base64-encode them so the body round-trips losslessly; the eventhubs
		// runtime base64-decodes body_encoding=base64 on send.
		if len(body) > 0 && !utf8.Valid(body) {
			action.Body = base64.StdEncoding.EncodeToString(body)
			action.BodyEncoding = "base64"
			binaryBodies++
		} else {
			action.Body = string(body)
		}
		if seq, ok := rec["SequenceNumber"]; ok {
			action.Metadata["source_sequence_number"] = seq
		}
		if len(props) > 0 {
			action.Headers = props
		}
		if partitionKey != "" {
			action.Params = map[string]any{"partition_key": partitionKey}
			partitionKeys++
		}
		action.Assertions = []dsl.Assertion{{Type: "message_received"}}
		actions = append(actions, action)
	}

	if err := dec.Error(); err != nil {
		warnings = append(warnings, "capture stream ended with an error: "+err.Error())
	}
	if recordsRead == 0 {
		return nil, fmt.Errorf("capture file contained no events")
	}
	if recordsRead > len(actions) {
		warnings = append(warnings, fmt.Sprintf("capture contained %d events; seeded the first %d as produce actions (raise max_events to import more)", recordsRead, len(actions)))
	}
	if emptyBodies > 0 {
		warnings = append(warnings, fmt.Sprintf("%d captured event(s) had an empty body", emptyBodies))
	}
	if binaryBodies > 0 {
		warnings = append(warnings, fmt.Sprintf("%d captured event(s) had a non-UTF-8 (binary) body; imported as base64 (body_encoding=base64) so the payload round-trips losslessly — verify before promotion", binaryBodies))
	}
	if partitionKeys == 0 {
		warnings = append(warnings, "no partition keys found in the capture; produced events use namespace default routing (set params.partition_id to measure per-partition skew)")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "eventhubs-capture-import"
	}
	script := &dsl.Script{
		Name:      name,
		Protocol:  "eventhubs",
		ProjectID: req.ProjectID,
		Actions:   actions,
	}
	script.Config.EventHubs = dsl.EventHubsConfig{Mock: true, EventHub: eventHub}

	return &ConvertResult{
		Script:   script,
		Warnings: warnings,
		Stats:    Stats{RecordsRead: recordsRead, ActionsEmitted: len(actions), PartitionKeys: partitionKeys, EmptyBodies: emptyBodies, BinaryBodies: binaryBodies},
		Summary:  fmt.Sprintf("Imported %d of %d captured Event Hubs events into produce actions for event hub %q (mock mode).", len(actions), recordsRead, eventHub),
	}, nil
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
		if b, ok := unwrapped.([]byte); ok {
			out[key] = string(b)
			continue
		}
		out[key] = fmt.Sprint(unwrapped)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
