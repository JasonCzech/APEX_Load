package kafka

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"golang.org/x/sync/semaphore"
)

type producerTuning struct {
	Retries, LingerMs, BatchBytes, BufferBytes, AdmissionTimeoutMs, DeliveryTimeoutMs, RequestTimeoutMs *int
}

func parseProducerTuning(values map[string]any) (producerTuning, error) {
	var p producerTuning
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
		value, err := boundedConfigInt(values, key, 0, f.min, f.max)
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

type producerAdmission struct {
	buffer   *semaphore.Weighted
	capacity int64
	timeout  time.Duration
}

func newProducerAdmission(p producerTuning) *producerAdmission {
	if p.BufferBytes == nil && p.AdmissionTimeoutMs == nil {
		return nil
	}
	a := &producerAdmission{timeout: time.Minute}
	if p.BufferBytes != nil {
		a.capacity = int64(*p.BufferBytes)
		a.buffer = semaphore.NewWeighted(a.capacity)
	}
	if p.AdmissionTimeoutMs != nil {
		a.timeout = time.Duration(*p.AdmissionTimeoutMs) * time.Millisecond
	}
	return a
}

// The admission deadline is discarded once metadata and buffer space are
// acquired. Delivery receives the caller context and its own configured budget.
func (a *producerAdmission) admit(ctx context.Context, bytes int64, metadata func(context.Context) error) (func(), error) {
	if a == nil {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if bytes < 0 || (a.buffer != nil && bytes > a.capacity) {
		return nil, fmt.Errorf("Kafka record exceeds producer buffer capacity")
	}
	admission, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	if err := metadata(admission); err != nil {
		return nil, fmt.Errorf("Kafka metadata admission: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A zero budget allows only immediately available metadata/buffer space.
	// Positive budgets must not admit a late metadata response.
	if a.timeout > 0 && admission.Err() != nil {
		return nil, fmt.Errorf("Kafka admission deadline: %w", admission.Err())
	}
	if a.buffer != nil {
		if a.buffer.TryAcquire(bytes) {
			return func() { a.buffer.Release(bytes) }, nil
		}
		if err := a.buffer.Acquire(admission, bytes); err != nil {
			return nil, fmt.Errorf("Kafka buffer admission: %w", err)
		}
		return func() { a.buffer.Release(bytes) }, nil
	}
	return func() {}, nil
}

func producerTLSVersions(c config) (uint16, uint16, error) {
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
	if c.TLSMinVersion != "" {
		min, err = parse(c.TLSMinVersion)
		if err != nil {
			return 0, 0, err
		}
	}
	if c.TLSMaxVersion != "" {
		max, err = parse(c.TLSMaxVersion)
		if err != nil {
			return 0, 0, err
		}
	}
	if max != 0 && min > max {
		return 0, 0, fmt.Errorf("Kafka TLS minimum exceeds maximum")
	}
	return min, max, nil
}

// ValidateProducerSettings applies the same bounds used by the runtime map.
func ValidateProducerSettings(values map[string]any) error {
	_, err := parseProducerTuning(values)
	return err
}
func ValidateTLSVersions(min, max string) error {
	_, _, err := producerTLSVersions(config{TLSMinVersion: min, TLSMaxVersion: max})
	return err
}
