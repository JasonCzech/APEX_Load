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
