package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestProducerAdmissionDoesNotLimitDelivery(t *testing.T) {
	capacity, timeout := 10, 20
	admission := newProducerAdmission(producerTuning{BufferBytes: &capacity, AdmissionTimeoutMs: &timeout})
	var metadataContext context.Context
	release, err := admission.admit(context.Background(), 8, func(ctx context.Context) error { metadataContext = ctx; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if metadataContext.Err() == nil {
		t.Fatal("admission budget retained after acceptance")
	}
	_, err = admission.admit(context.Background(), 8, func(context.Context) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("buffer wait not bounded", err)
	}
	// Delivery can complete later; the admission deadline never becomes its ctx.
	release()
	release, err = admission.admit(context.Background(), 8, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err = admission.admit(context.Background(), 11, func(context.Context) error { return nil }); err == nil {
		t.Fatal("oversize record accepted")
	}
}
func TestProducerAdmissionCancellationAndMetadataBudget(t *testing.T) {
	timeout := 10
	admission := newProducerAdmission(producerTuning{AdmissionTimeoutMs: &timeout})
	started := time.Now()
	_, err := admission.admit(context.Background(), 1, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = admission.admit(ctx, 1, func(ctx context.Context) error { return ctx.Err() })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
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

func TestGenericKafkaPropertiesCannotSilentlyExecute(t *testing.T) {
	k := &Kafka{name: "kafka"}
	err := k.Init(context.Background(), map[string]any{"kafka_mock": true, "kafka_properties": map[string]string{"unsupported.client.property": "credential-canary"}})
	if err == nil {
		t.Fatal("generic property silently ignored")
	}
	if strings.Contains(err.Error(), "credential-canary") {
		t.Fatal("property value leaked in error")
	}
}

func TestZeroAdmissionAllowsCachedMetadataAndAvailableBuffer(t *testing.T) {
	capacity, timeout := 4, 0
	a := newProducerAdmission(producerTuning{BufferBytes: &capacity, AdmissionTimeoutMs: &timeout})
	cached := func(context.Context) error { return nil }
	release, err := a.admit(context.Background(), 4, cached)
	if err != nil {
		t.Fatal("zero wait rejected immediate admission", err)
	}
	if _, err = a.admit(context.Background(), 1, cached); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("zero wait blocked instead of failing", err)
	}
	release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = a.admit(ctx, 1, cached); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled caller admitted", err)
	}
}

func TestProducerAdmissionRejectsMetadataAfterDeadline(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		t.Run(fmt.Sprint(buffered), func(t *testing.T) {
			timeout, capacity := 1, 10
			tuning := producerTuning{AdmissionTimeoutMs: &timeout}
			if buffered {
				tuning.BufferBytes = &capacity
			}
			admission := newProducerAdmission(tuning)
			_, err := admission.admit(context.Background(), 1, func(ctx context.Context) error { <-ctx.Done(); return nil })
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("late metadata admitted: %v", err)
			}
			if buffered && !admission.buffer.TryAcquire(int64(capacity)) {
				t.Fatal("rejected record retained buffer")
			}
		})
	}
}
