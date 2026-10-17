package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol/kafkacontract"
	"golang.org/x/sync/semaphore"
)

type producerTuning = kafkacontract.ProducerTuning

func parseProducerTuning(values map[string]any) (producerTuning, error) {
	return kafkacontract.ParseProducerTuning(values)
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
	return kafkacontract.TLSVersions(c.TLSMinVersion, c.TLSMaxVersion)
}

// ValidateProducerSettings applies the same bounds used by the runtime map.
func ValidateProducerSettings(values map[string]any) error {
	return kafkacontract.ValidateProducerSettings(values)
}
func ValidateTLSVersions(min, max string) error {
	return kafkacontract.ValidateTLSVersions(min, max)
}
