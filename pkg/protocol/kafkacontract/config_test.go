package kafkacontract

import (
	"strings"
	"testing"
)

func TestProducerTuningBoundsAndTLS(t *testing.T) {
	for _, values := range []map[string]any{{"kafka_producer_retries": -1}, {"kafka_producer_linger_ms": 1.5}, {"kafka_producer_buffer_bytes": 0}, {"kafka_producer_batch_bytes": 1}, {"kafka_producer_delivery_timeout_ms": 10, "kafka_producer_request_timeout_ms": 20}} {
		if err := ValidateProducerSettings(values); err == nil {
			t.Fatal("invalid producer config", values)
		}
	}
	if err := ValidateProducerSettings(map[string]any{"kafka_producer_retries": 0, "kafka_producer_linger_ms": 0, "kafka_producer_admission_timeout_ms": 0}); err != nil {
		t.Fatal(err)
	}
	for _, bounds := range [][2]string{{"1.1", ""}, {"1.3", "1.2"}, {"", "1.1"}} {
		if err := ValidateTLSVersions(bounds[0], bounds[1]); err == nil {
			t.Fatal(bounds)
		}
	}
	if err := ValidateTLSVersions("1.2", "1.3"); err != nil {
		t.Fatal(err)
	}
}
func TestConsumerGroupOverrideRefusalDoesNotDiscloseIdentities(t *testing.T) {
	err := ValidateConsumerGroupOverride("private-authored-group", "private-configured-group")
	if err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatalf("group identity disclosed by refusal: %v", err)
	}
}

func TestStructuralKafkaContracts(t *testing.T) {
	if got, err := ParseProducerTuning(nil); err != nil || got.Retries != nil {
		t.Fatalf("unset tuning: %+v, %v", got, err)
	}
	if err := ValidateOAuthTokenEndpoint("https://example.test/token"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "://invalid", "http://example.test/token", "https://example.test/token#fragment"} {
		if err := ValidateOAuthTokenEndpoint(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if got, err := BoundedConfigInt(nil, "kafka_count", 4, 1, 5); err != nil || got != 4 {
		t.Fatalf("fallback: %d, %v", got, err)
	}
	for _, value := range []any{1.5, "1.5", nil, 6, -1} {
		if _, err := BoundedConfigInt(map[string]any{"kafka_count": value}, "kafka_count", 4, 1, 5); err == nil {
			t.Fatalf("accepted %v", value)
		}
	}
	if got, err := BoundedConfigInt(map[string]any{"kafka_count": "5"}, "kafka_count", 4, 1, 5); err != nil || got != 5 {
		t.Fatalf("upper bound: %d, %v", got, err)
	}
}

func TestProducerMaxInFlightRequestsContract(t *testing.T) {
	p, err := ParseProducerTuning(map[string]any{"kafka_producer_max_in_flight_requests": float64(5)})
	if err != nil || p.MaxInFlightRequests == nil || *p.MaxInFlightRequests != 5 {
		t.Fatalf("parsed %+v, %v", p, err)
	}
	if p, err = ParseProducerTuning(nil); err != nil || p.MaxInFlightRequests != nil {
		t.Fatalf("unset in-flight must stay nil to keep the franz default: %+v, %v", p, err)
	}
	for _, value := range []any{0, -1, 1.5, "many"} {
		if err := ValidateProducerSettings(map[string]any{"kafka_producer_max_in_flight_requests": value}); err == nil {
			t.Fatalf("accepted max_in_flight_requests %v", value)
		}
	}
	for _, test := range []struct {
		acks, idempotence string
		idempotent        bool
	}{{"", "", true}, {"all", "enabled", true}, {"ALL", "Disabled", false}, {"leader", "", false}, {"none", "enabled", false}} {
		if got := IdempotentProducer(test.acks, test.idempotence); got != test.idempotent {
			t.Fatalf("IdempotentProducer(%q, %q) = %v", test.acks, test.idempotence, got)
		}
	}
	six, five := 6, 5
	if err := ValidateProducerInFlight(ProducerTuning{MaxInFlightRequests: &six}, "", ""); err == nil {
		t.Fatal("idempotent producer accepted six in-flight requests")
	}
	for _, test := range []struct {
		value             *int
		acks, idempotence string
	}{{&five, "", ""}, {&six, "all", "disabled"}, {&six, "leader", ""}, {nil, "", ""}} {
		if err := ValidateProducerInFlight(ProducerTuning{MaxInFlightRequests: test.value}, test.acks, test.idempotence); err != nil {
			t.Fatalf("%+v: %v", test, err)
		}
	}
}
