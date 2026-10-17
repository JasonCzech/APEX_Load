package kafkacontract

import (
	"crypto/tls"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	MaxConfiguredTimeout          = time.Hour
	MaxConfiguredPartitions       = 1_000_000
	MaxConfiguredMessageBytes     = 64 << 20
	MaxConfiguredSchemaBytes      = 1 << 20
	MaxConfiguredSubjectBytes     = 4 << 10
	MaxConfiguredRegistryBytes    = 8 << 10
	MaxKafkaTopicBytes            = 249
	MaxKafkaIdentifierBytes       = 4 << 10
	FeatureRealSeekV1             = "kafka_real_seek_v1"
	FeatureConsumerTransactionsV1 = "kafka_consumer_transactions_v1"
)

func BoundedConfigInt(cfg map[string]any, key string, fallback, minimum, maximum int) (int, error) {
	value, exists := cfg[key]
	if !exists {
		return fallback, nil
	}
	parsed, err := NumericInt64(value)
	if err != nil || parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, fmt.Errorf("kafka %s must be an integer between %d and %d", strings.TrimPrefix(key, "kafka_"), minimum, maximum)
	}
	return int(parsed), nil
}
func NumericInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < math.MinInt64 || typed > math.MaxInt64 {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(typed), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	default:
		return 0, fmt.Errorf("not an integer")
	}
}
func ParseProducerTuning(values map[string]any) (ProducerTuning, error) {
	var p ProducerTuning
	for _, f := range []struct {
		key      string
		dst      **int
		min, max int
	}{
		{"retries", &p.Retries, 0, 2147483647}, {"linger_ms", &p.LingerMs, 0, 3600000}, {"batch_bytes", &p.BatchBytes, 512, 67108864}, {"buffer_bytes", &p.BufferBytes, 1, 1073741824}, {"admission_timeout_ms", &p.AdmissionTimeoutMs, 0, 3600000}, {"delivery_timeout_ms", &p.DeliveryTimeoutMs, 1, 3600000}, {"request_timeout_ms", &p.RequestTimeoutMs, 1, 3600000},
	} {
		key := "kafka_producer_" + f.key
		if _, ok := values[key]; !ok {
			continue
		}
		value, err := BoundedConfigInt(values, key, 0, f.min, f.max)
		if err != nil {
			return p, err
		}
		*f.dst = &value
	}
	if p.DeliveryTimeoutMs != nil && p.RequestTimeoutMs != nil {
		linger := 0
		if p.LingerMs != nil {
			linger = *p.LingerMs
		}
		if *p.DeliveryTimeoutMs < *p.RequestTimeoutMs+linger {
			return p, fmt.Errorf("Kafka delivery timeout must include request timeout and linger")
		}
	}
	return p, nil
}
func ValidateProducerSettings(values map[string]any) error {
	_, err := ParseProducerTuning(values)
	return err
}
func ValidateConsumerGroupOverride(group, configuredGroup string) error {
	if group != "" && group != configuredGroup {
		return fmt.Errorf("per-action consumer_group differs from the configured config.kafka.consumer_group; the real Kafka adapter binds one group per client")
	}
	return nil
}
func ValidateOAuthTokenEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if len(raw) > 8192 || err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("Kafka OAuth token endpoint must be an HTTPS URL without user info or fragment")
	}
	return nil
}

type ProducerTuning struct {
	Retries, LingerMs, BatchBytes, BufferBytes, AdmissionTimeoutMs, DeliveryTimeoutMs, RequestTimeoutMs *int
}

func TLSVersions(minVersion, maxVersion string) (uint16, uint16, error) {
	min, max := uint16(tls.VersionTLS12), uint16(0)
	parse := func(v string) (uint16, error) {
		switch v {
		case "1.2":
			return tls.VersionTLS12, nil
		case "1.3":
			return tls.VersionTLS13, nil
		default:
			return 0, fmt.Errorf("Kafka TLS version must be 1.2 or 1.3")
		}
	}
	var err error
	if minVersion != "" {
		min, err = parse(minVersion)
		if err != nil {
			return 0, 0, err
		}
	}
	if maxVersion != "" {
		max, err = parse(maxVersion)
		if err != nil {
			return 0, 0, err
		}
	}
	if max != 0 && min > max {
		return 0, 0, fmt.Errorf("Kafka TLS minimum exceeds maximum")
	}
	return min, max, nil
}
func ValidateTLSVersions(min, max string) error { _, _, err := TLSVersions(min, max); return err }
