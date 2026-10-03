package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestMessagingEvidenceObservedZeroUnavailableAndClone(t *testing.T) {
	zero := 0.0
	diagnostic := &MessagingDiagnostics{Version: MessagingEvidenceVersion, Durations: []MessagingDurationObservation{
		{Metric: "producer_ack", ValueMs: &zero},
		{Metric: "enqueue_age", UnavailableReason: "clock_unqualified"},
	}, Counters: []MessagingCounterObservation{{Metric: "duplicate", Population: "received_message", Scope: "local_sequence_window", Eligible: 1, Observed: 1, WindowEntries: 4096, LowerBound: true}}}
	if !diagnostic.Valid() {
		t.Fatal("valid explicit observations rejected")
	}
	clone := diagnostic.Clone()
	*clone.Durations[0].ValueMs = 12
	clone.Counters[0].Count = 1
	if *diagnostic.Durations[0].ValueMs != 0 || diagnostic.Counters[0].Count != 0 {
		t.Fatal("raw clone aliases observations")
	}
	data, err := json.Marshal(diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	var restored MessagingDiagnostics
	if err = json.Unmarshal(data, &restored); err != nil || !restored.Valid() || restored.Durations[0].ValueMs == nil {
		t.Fatalf("zero lost: %s %v", data, err)
	}
	var summary *MessagingSummary
	MergeMessagingTelemetry(&summary, &ServerTelemetry{MessagesSent: 1, Messaging: diagnostic}, "events", "kafka_topic")
	if summary.EvidenceVersion() != 2 || summary.Incomplete || !summary.Valid() {
		t.Fatalf("summary: %+v", summary)
	}
	evidence := summary.Dimensions[0].Evidence
	for _, duration := range evidence.Durations {
		switch duration.Metric {
		case "producer_ack":
			if duration.Eligible != 1 || duration.Distribution.Count != 1 || duration.Distribution.Sum != 0 {
				t.Fatal("measured zero became missing", duration)
			}
			if p95, ok := duration.Distribution.Quantile(.95); !ok || p95 != 0 {
				t.Fatal("zero quantile unavailable")
			}
		case "enqueue_age":
			if duration.Eligible != 1 || duration.Distribution.Count != 0 || duration.Unavailable["clock_unqualified"] != 1 {
				t.Fatal("missing age became zero", duration)
			}
		}
	}
	projected := summary.Clone()
	projected.Dimensions[0].Evidence.Durations[0].Unavailable["clock_unqualified"] = 4
	projected.Dimensions[0].Evidence.Counters[0].Count = 1
	if evidence.Durations[0].Unavailable["clock_unqualified"] != 1 || evidence.Counters[0].Count != 0 {
		t.Fatal("summary clone aliases v2 evidence")
	}
}

func TestMessagingEvidenceDistributionsMergeMatchSequential(t *testing.T) {
	var first, second, sequential, merged *MessagingSummary
	values := []float64{0, .001, .0012, 1, 2, 20, 200, 86400000}
	for i, value := range values {
		diagnostic := &MessagingDiagnostics{Version: 2, Durations: []MessagingDurationObservation{{Metric: "producer_ack", ValueMs: &value}}}
		telemetry := &ServerTelemetry{MessagesSent: 2, AcknowledgementMs: &value, Messaging: diagnostic}
		MergeMessagingTelemetry(&sequential, telemetry, "events", "eventhubs", "group")
		if i < 3 {
			MergeMessagingTelemetry(&first, telemetry, "events", "eventhubs", "group")
		} else {
			MergeMessagingTelemetry(&second, telemetry, "events", "eventhubs", "group")
		}
	}
	MergeMessagingSummary(&merged, first)
	MergeMessagingSummary(&merged, second)
	if !merged.Valid() || merged.Incomplete || !reflect.DeepEqual(merged, sequential) {
		t.Fatalf("merge differs: %+v %+v", merged, sequential)
	}
	duration := merged.Dimensions[0].Evidence.Durations[0]
	if duration.Eligible != int64(len(values)) || duration.Distribution.Count != int64(len(values)) || merged.Dimensions[0].Sent != 16 {
		t.Fatal("batch messages replaced operation population")
	}
	if p50, ok := duration.Distribution.Quantile(.5); !ok || p50 < 1 || p50 > 1.2 {
		t.Fatalf("percentile %v available %v", p50, ok)
	}
	first.Dimensions[0].Evidence.Durations[0].Distribution.Buckets[0] = 999
	if merged.Dimensions[0].Evidence.Durations[0].Distribution.Buckets[0] == 999 {
		t.Fatal("merge aliases source")
	}
}

func TestMessagingEvidenceMixedLegacyCoverage(t *testing.T) {
	value := 3.0
	typed := &ServerTelemetry{MessagesSent: 1, AcknowledgementMs: &value, Messaging: &MessagingDiagnostics{Version: 2, Durations: []MessagingDurationObservation{{Metric: "producer_ack", ValueMs: &value}}}}
	legacy := &ServerTelemetry{MessagesSent: 1, AcknowledgementMs: &value}
	for _, reverse := range []bool{false, true} {
		var summary *MessagingSummary
		if reverse {
			MergeMessagingTelemetry(&summary, legacy, "events", "kafka_topic")
			MergeMessagingTelemetry(&summary, typed, "events", "kafka_topic")
		} else {
			MergeMessagingTelemetry(&summary, typed, "events", "kafka_topic")
			MergeMessagingTelemetry(&summary, legacy, "events", "kafka_topic")
		}
		if !summary.Valid() || !summary.Incomplete || summary.EvidenceVersion() != 2 {
			t.Fatalf("unknown peer coverage lost: %+v", summary)
		}
		dimension := summary.Dimensions[0]
		if dimension.Operations != 2 || dimension.Ack.Count != 2 || dimension.Evidence.Operations != 1 || !dimension.Evidence.Incomplete || dimension.Evidence.Durations[0].Distribution.Count != 1 {
			t.Fatalf("legacy became observed v2: %+v", dimension)
		}
	}
	var summary *MessagingSummary
	MergeMessagingTelemetry(&summary, typed, "first", "kafka_topic")
	MergeMessagingTelemetry(&summary, legacy, "second", "kafka_topic")
	if !summary.Incomplete || !summary.Valid() {
		t.Fatal("separate legacy source coverage hidden")
	}
}

func TestMessagingEvidenceRejectsInvalidRawAndRetainsKnownLegacy(t *testing.T) {
	for _, mutation := range []func(*MessagingDiagnostics){
		func(d *MessagingDiagnostics) { d.Version = 99 },
		func(d *MessagingDiagnostics) { d.Durations[0].Metric = "server_processing" },
		func(d *MessagingDiagnostics) { d.Durations[0].UnavailableReason = "unsupported" },
		func(d *MessagingDiagnostics) { d.Durations[0].ValueMs = nil },
		func(d *MessagingDiagnostics) { *d.Durations[0].ValueMs = math.NaN() },
		func(d *MessagingDiagnostics) { *d.Durations[0].ValueMs = 86400001 },
		func(d *MessagingDiagnostics) { d.Durations = append(d.Durations, d.Durations[0]) },
		func(d *MessagingDiagnostics) {
			d.Counters = []MessagingCounterObservation{{Metric: "duplicate", Population: "received_message", Scope: "broker_inventory", Eligible: 1, Observed: 1, Count: 1}}
		},
	} {
		value := 3.0
		diagnostic := &MessagingDiagnostics{Version: 2, Durations: []MessagingDurationObservation{{Metric: "producer_ack", ValueMs: &value}}}
		mutation(diagnostic)
		if diagnostic.Valid() {
			t.Fatalf("invalid raw accepted: %+v", diagnostic)
		}
		ack := 2.0
		var summary *MessagingSummary
		MergeMessagingTelemetry(&summary, &ServerTelemetry{MessagesSent: 1, AcknowledgementMs: &ack, Messaging: diagnostic}, "events", "kafka_topic")
		if summary == nil || !summary.Valid() || !summary.Incomplete || summary.Dimensions[0].Ack.Count != 1 || summary.Dimensions[0].Evidence != nil {
			t.Fatalf("invalid evidence became complete or discarded known observations: %+v", summary)
		}
	}
}

func TestMessagingEvidenceRejectsMalformedHistogramAndOverflow(t *testing.T) {
	value := 20.0
	diagnostic := &MessagingDiagnostics{Version: 2, Durations: []MessagingDurationObservation{{Metric: "producer_ack", ValueMs: &value}}}
	var good *MessagingSummary
	MergeMessagingTelemetry(&good, &ServerTelemetry{MessagesSent: 1, Messaging: diagnostic}, "events", "kafka_topic")
	for _, mutation := range []func(*MessagingEvidence){
		func(e *MessagingEvidence) { e.Durations[0].Distribution.Buckets[0] = -1 },
		func(e *MessagingEvidence) { e.Durations[0].Distribution.Count++ },
		func(e *MessagingEvidence) { e.Durations[0].Distribution.Sum = 0 },
		func(e *MessagingEvidence) { e.Durations[0].Distribution.Min = 2 },
		func(e *MessagingEvidence) { e.Durations[0].Unavailable = map[string]int64{"not_observed": 1} },
		func(e *MessagingEvidence) { e.Durations[0].Population = "received_message" },
		func(e *MessagingEvidence) { e.Operations = 2 },
	} {
		bad := good.Clone()
		mutation(bad.Dimensions[0].Evidence)
		if bad.Valid() {
			t.Fatal("malformed summary accepted")
		}
		out := good.Clone()
		MergeMessagingSummary(&out, bad)
		if !out.Valid() || !out.Incomplete || out.Dimensions[0].Evidence.Durations[0].Distribution.Count != 1 {
			t.Fatal("invalid merge mutated retained distribution")
		}
	}
	enormous := &MessagingSummary{Dimensions: []MessagingDimension{{Protocol: "kafka", Source: "events", Operations: maxMessagingEvidenceCount, Evidence: &MessagingEvidence{Operations: maxMessagingEvidenceCount}}}}
	MergeMessagingSummary(&enormous, good)
	if !enormous.Valid() || !enormous.Incomplete || enormous.Dimensions[0].Operations != maxMessagingEvidenceCount {
		t.Fatal("overflow replaced valid retained evidence")
	}
}

func TestMessagingCounterEligibilityAndScope(t *testing.T) {
	observation := MessagingCounterObservation{Metric: "duplicate", Population: "received_message", Scope: "local_sequence_window", Eligible: 4, Observed: 3, Count: 1, WindowEntries: 4096, Sampled: true, LowerBound: true}
	for _, mutation := range []func(*MessagingCounterObservation){
		func(c *MessagingCounterObservation) { c.Eligible = -1 },
		func(c *MessagingCounterObservation) { c.Observed = 5 },
		func(c *MessagingCounterObservation) { c.Count = 4 },
		func(c *MessagingCounterObservation) { c.WindowEntries = 4097 },
		func(c *MessagingCounterObservation) { c.LowerBound = false },
		func(c *MessagingCounterObservation) { c.Population = "action_operation" },
	} {
		bad := observation
		mutation(&bad)
		if (&MessagingDiagnostics{Version: 2, Counters: []MessagingCounterObservation{bad}}).Valid() {
			t.Fatalf("invalid counter accepted: %+v", bad)
		}
	}
	var summary *MessagingSummary
	for i := 0; i < 2; i++ {
		MergeMessagingTelemetry(&summary, &ServerTelemetry{Messaging: &MessagingDiagnostics{Version: 2, Counters: []MessagingCounterObservation{observation}}}, "events", "kafka_topic")
	}
	counter := summary.Dimensions[0].Evidence.Counters[0]
	if !summary.Valid() || summary.Incomplete || counter.Eligible != 8 || counter.Observed != 6 || counter.Count != 2 || !counter.Sampled || !counter.LowerBound || counter.WindowEntries != 4096 {
		t.Fatalf("counter population lost: %+v", counter)
	}
}

func TestMessagingEvidenceClonesBoundInvalidInput(t *testing.T) {
	oversized := &MessagingDiagnostics{Version: 2, Durations: make([]MessagingDurationObservation, MaxMessagingDurationMetrics+1)}
	clone := oversized.Clone()
	if clone.Valid() || len(clone.Durations) != 0 || len(clone.Counters) != 0 {
		t.Fatal("invalid raw clone retained unbounded input")
	}
	summary := &MessagingSummary{Dimensions: make([]MessagingDimension, MaxMessagingDimensions+1)}
	if clone := summary.Clone(); !clone.Incomplete || len(clone.Dimensions) != 0 {
		t.Fatal("invalid summary clone retained unbounded input")
	}
}

func TestMessagingEvidenceRepairsMalformedMergeDestination(t *testing.T) {
	bad := &MessagingSummary{Dimensions: []MessagingDimension{{Operations: -1}}}
	MergeMessagingSummary(&bad, &MessagingSummary{Dimensions: []MessagingDimension{{Source: "events", Operations: 1}}})
	if !bad.Valid() || !bad.Incomplete || len(bad.Dimensions) != 1 || bad.Dimensions[0].Operations != 1 {
		t.Fatalf("invalid destination retained: %+v", bad)
	}
}

func TestMessagingSendOutcomeSeparatesAdmissionAndCommit(t *testing.T) {
	for _, test := range []struct {
		name                              string
		attempted, succeeded, transaction bool
		metric, population                string
	}{
		{name: "preflight rejection", metric: "local_admission"},
		{name: "transaction buffer", succeeded: true, metric: "local_admission"},
		{name: "provider rejection", attempted: true, metric: "producer_ack", population: "send_attempt"},
		{name: "transaction flush", attempted: true, transaction: true, population: "transaction_flush_attempt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			telemetry := &ServerTelemetry{}
			AnnotateMessagingSendOutcome(telemetry, test.attempted, test.succeeded, test.transaction)
			if !telemetry.Messaging.Valid() {
				t.Fatal("invalid outcome contract")
			}
			if test.metric != "" && (len(telemetry.Messaging.Durations) != 1 || telemetry.Messaging.Durations[0].Metric != test.metric) {
				t.Fatal("wrong eligible duration population")
			}
			if test.population == "" && len(telemetry.Messaging.Counters) != 0 {
				t.Fatal("local admission became provider attempt")
			}
			if test.population != "" && (len(telemetry.Messaging.Counters) != 1 || telemetry.Messaging.Counters[0].Population != test.population) {
				t.Fatal("counter scopes pooled")
			}
			if test.transaction && len(telemetry.Messaging.Durations) != 0 {
				t.Fatal("commit flush became producer acknowledgement")
			}
		})
	}
}

func TestMessagingDurationSummaryJSONRejectsMalformedBucketShape(t *testing.T) {
	// The first 144 bins reconcile exactly with Count=1. The previous fixed
	// array decoder silently dropped a 145th bin and accepted this evidence.
	validBuckets := "[1," + strings.Repeat("0,", 142) + "0]"
	for _, tc := range []struct {
		name    string
		buckets string
	}{
		{name: "oversized", buckets: strings.TrimSuffix(validBuckets, "]") + ",1]"},
		{name: "many extra entries", buckets: strings.TrimSuffix(validBuckets, "]") + "," + strings.Repeat("1,", 1023) + "1]"},
		{name: "short", buckets: "[1," + strings.Repeat("0,", 141) + "0]"},
		{name: "empty", buckets: "[]"},
		{name: "null array", buckets: "null"},
		{name: "object", buckets: "{}"},
		{name: "null entry", buckets: "[1,null," + strings.Repeat("0,", 141) + "0]"},
		{name: "string entry", buckets: "[1,\"0\"," + strings.Repeat("0,", 141) + "0]"},
		{name: "fraction entry", buckets: "[1,0.5," + strings.Repeat("0,", 141) + "0]"},
		{name: "overflow entry", buckets: "[1,9223372036854775808," + strings.Repeat("0,", 141) + "0]"},
		{name: "nested entry", buckets: "[1,[0]," + strings.Repeat("0,", 141) + "0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := `{"metric":"producer_ack","population":"producer_operation","eligible":1,"distribution_ms":{"count":1,"sum":0,"min":0,"max":0,"buckets":` + tc.buckets + `}}`
			var restored MessagingDurationSummary
			if err := json.Unmarshal([]byte(wire), &restored); err == nil {
				t.Fatal("malformed messaging duration buckets accepted")
			}
		})
	}
	// Other protocol and historical archive evidence retain the old decoder;
	// strictness is scoped to MessagingDurationSummary rather than the shared type.
	var legacy DiagnosticDistribution
	if err := json.Unmarshal([]byte(`{"count":1,"buckets":`+strings.TrimSuffix(validBuckets, "]")+`,1]}`), &legacy); err != nil || legacy.Count != 1 || legacy.Buckets[0] != 1 {
		t.Fatalf("legacy histogram decoding changed: %+v %v", legacy, err)
	}
}

func TestMessagingDurationSummaryJSONRoundTripAndMissingDistribution(t *testing.T) {
	observed := MessagingDurationSummary{Metric: "producer_ack", Population: "producer_operation", Eligible: 1}
	observed.Distribution.Add(0)
	missing := MessagingDurationSummary{Metric: "producer_ack", Population: "producer_operation", Eligible: 1, Unavailable: map[string]int64{"failed": 1}}
	for _, summary := range []MessagingDurationSummary{observed, missing} {
		data, err := json.Marshal(summary)
		if err != nil {
			t.Fatal(err)
		}
		var restored MessagingDurationSummary
		if err := json.Unmarshal(data, &restored); err != nil || !restored.valid(1) || !reflect.DeepEqual(restored, summary) {
			t.Fatalf("valid duration summary did not roundtrip: %+v %v", restored, err)
		}
	}
	var restored MessagingDurationSummary
	if err := json.Unmarshal([]byte(`{"metric":"producer_ack","population":"producer_operation","eligible":1,"distribution_ms":{},"unavailable":{"failed":1}}`), &restored); err != nil || !restored.valid(1) {
		t.Fatalf("all-missing zero distribution rejected: %+v %v", restored, err)
	}
	if err := json.Unmarshal([]byte(`{"metric":"producer_ack","population":"producer_operation","eligible":1,"distribution_ms":null,"unavailable":{"failed":1}}`), &restored); err == nil {
		t.Fatal("explicit null distribution accepted")
	}
}

func TestMessagingDurationSummaryJSONRejectsNullDistributionNumbers(t *testing.T) {
	buckets := "[1," + strings.Repeat("0,", 142) + "0]"
	valid := `{"metric":"producer_ack","population":"producer_operation","eligible":1,"distribution_ms":{"count":1,"sum":0,"min":0,"max":0,"buckets":` + buckets + `}}`
	for _, field := range []string{"count", "sum", "min", "max"} {
		t.Run(field, func(t *testing.T) {
			value := "0"
			if field == "count" {
				value = "1"
			}
			wire := strings.Replace(valid, `"`+field+`":`+value, `"`+field+`":null`, 1)
			var restored MessagingDurationSummary
			if err := json.Unmarshal([]byte(wire), &restored); err == nil {
				t.Fatal("explicit null histogram number became measured zero")
			}
		})
	}
}

func TestMessagingCounterJSONRejectsNullCounts(t *testing.T) {
	counter := MessagingCounterObservation{Metric: "send_failure", Population: "send_attempt", Scope: "client_action", Eligible: 1, Observed: 1}
	canonical, err := json.Marshal(&MessagingDiagnostics{Version: 2, Counters: []MessagingCounterObservation{counter}})
	if err != nil {
		t.Fatal(err)
	}
	var restored MessagingDiagnostics
	if err := json.Unmarshal(canonical, &restored); err != nil || !restored.Valid() || restored.Counters[0] != counter {
		t.Fatalf("observed zero counter did not roundtrip: %+v %v", restored, err)
	}
	for _, field := range []string{"eligible", "observed", "count", "window_entries"} {
		t.Run(field, func(t *testing.T) {
			wire := string(canonical)
			if field == "window_entries" {
				wire = strings.Replace(wire, `"count":0`, `"count":0,"window_entries":null`, 1)
			} else {
				value := "1"
				if field == "count" {
					value = "0"
				}
				wire = strings.Replace(wire, `"`+field+`":`+value, `"`+field+`":null`, 1)
			}
			if err := json.Unmarshal([]byte(wire), &restored); err == nil {
				t.Fatal("explicit null counter count became observed zero")
			}
		})
	}
	// The summary alias must enforce the same numeric shape as raw evidence.
	var summary MessagingSummary
	poison := `{"dimensions":[{"protocol":"kafka","source":"events","operations":1,"evidence":{"operations":1,"counters":[{"metric":"send_failure","population":"send_attempt","scope":"client_action","eligible":1,"observed":1,"count":null}]}}]}`
	if err := json.Unmarshal([]byte(poison), &summary); err == nil {
		t.Fatal("explicit null summary counter became observed zero")
	}
	if err := json.Unmarshal([]byte(strings.Replace(poison, `"count":null`, `"count":0`, 1)), &summary); err != nil || !summary.Valid() || summary.EvidenceVersion() != 2 {
		t.Fatalf("valid counter-only summary rejected: %+v %v", summary, err)
	}
}

func TestMessagingEvidenceJSONRejectsNullPopulations(t *testing.T) {
	for _, wire := range []string{
		`{"operations":null}`,
		`{"operations":1,"durations":[{"metric":"producer_ack","population":"producer_operation","eligible":null,"distribution_ms":{}}]}`,
	} {
		var evidence MessagingEvidence
		if err := json.Unmarshal([]byte(wire), &evidence); err == nil {
			t.Fatal("explicit null evidence population became zero")
		}
	}
}

func TestMessagingCounterJSONRequiresPopulationAndCount(t *testing.T) {
	valid := `{"version":2,"counters":[{"metric":"send_failure","population":"send_attempt","scope":"client_action","eligible":1,"observed":1,"count":0}]}`
	for _, field := range []string{"eligible", "observed", "count"} {
		t.Run(field, func(t *testing.T) {
			value := "1"
			if field == "count" {
				value = "0"
			}
			wire := strings.Replace(valid, `,"`+field+`":`+value, "", 1)
			var restored MessagingDiagnostics
			if err := json.Unmarshal([]byte(wire), &restored); err == nil {
				t.Fatal("omitted counter number became observed zero")
			}
		})
	}
	var restored MessagingDiagnostics
	if err := json.Unmarshal([]byte(valid), &restored); err != nil || !restored.Valid() || restored.Counters[0].WindowEntries != 0 {
		t.Fatalf("valid counter without optional window rejected: %+v %v", restored, err)
	}
	var summary MessagingSummary
	wire := `{"dimensions":[{"protocol":"kafka","source":"events","operations":1,"evidence":{"operations":1,"counters":[{"metric":"send_failure","population":"send_attempt","scope":"client_action","eligible":1,"observed":1}]}}]}`
	if err := json.Unmarshal([]byte(wire), &summary); err == nil {
		t.Fatal("omitted summary counter count became observed zero")
	}
}

func TestMessagingDurationJSONRequiresObservedStatisticsAndBuckets(t *testing.T) {
	valid := `{"metric":"producer_ack","population":"producer_operation","eligible":1,"distribution_ms":{"count":1,"sum":0,"min":0,"max":0,"buckets":[1,` + strings.Repeat("0,", 142) + `0]}}`
	for _, field := range []string{"sum", "min", "max", "buckets"} {
		t.Run(field, func(t *testing.T) {
			wire := valid
			if field == "buckets" {
				start := strings.Index(wire, `,"buckets":`)
				wire = wire[:start] + `}}`
			} else {
				wire = strings.Replace(wire, `,"`+field+`":0`, "", 1)
			}
			var restored MessagingDurationSummary
			if err := json.Unmarshal([]byte(wire), &restored); err == nil {
				t.Fatal("omitted observed histogram evidence became zero")
			}
		})
	}
}

func TestMessagingEvidenceJSONRequiresPopulationNumbers(t *testing.T) {
	for _, wire := range []string{
		`{}`,
		`{"operations":1,"durations":[{"metric":"producer_ack","population":"producer_operation","distribution_ms":{}}]}`,
	} {
		var evidence MessagingEvidence
		if err := json.Unmarshal([]byte(wire), &evidence); err == nil {
			t.Fatal("omitted evidence population became zero")
		}
	}
	// A real zero-observation distribution need not manufacture statistics.
	var evidence MessagingEvidence
	if err := json.Unmarshal([]byte(`{"operations":1,"durations":[{"metric":"producer_ack","population":"producer_operation","eligible":1,"distribution_ms":{},"unavailable":{"failed":1}}]}`), &evidence); err != nil || !evidence.Valid() {
		t.Fatalf("wholly unavailable distribution rejected: %+v %v", evidence, err)
	}
}
