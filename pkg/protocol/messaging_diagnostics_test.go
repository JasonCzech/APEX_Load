package protocol

import (
	"fmt"
	"math"
	"testing"
)

func TestMessagingDimensionsAndObservedZero(t *testing.T) {
	var s *MessagingSummary
	zero := 0.0
	MergeMessagingTelemetry(&s, &ServerTelemetry{MessagesSent: 9, PartitionID: "0"}, "topic-a")
	MergeMessagingTelemetry(&s, &ServerTelemetry{MessagesSent: 1, PartitionID: "1"}, "topic-a")
	if skew, ok := s.ObservedPartitionSkew(); !ok || math.Abs(skew-80) > 1e-9 {
		t.Fatalf("skew %v %v", skew, ok)
	}
	MergeMessagingTelemetry(&s, &ServerTelemetry{MessagesReceived: 1, PartitionID: "0", SubscriptionID: "first", ConsumerLagObserved: true, ConsumerLagMs: 0, AcknowledgementMs: &zero}, "topic-a")
	MergeMessagingTelemetry(&s, &ServerTelemetry{MessagesReceived: 1, PartitionID: "0", SubscriptionID: "first", ConsumerLagObserved: true, ConsumerLagMs: 20}, "topic-a")
	MergeMessagingTelemetry(&s, &ServerTelemetry{MessagesReceived: 1, PartitionID: "0", SubscriptionID: "second"}, "topic-a")
	var first, second MessagingDimension
	for _, d := range s.Dimensions {
		if d.Subscription == "first" {
			first = d
		}
		if d.Subscription == "second" {
			second = d
		}
	}
	if mean, ok := first.Age.Mean(); !ok || mean != 10 || first.Age.Count != 2 || first.Ack.Count != 1 {
		t.Fatalf("observations: %+v", first)
	}
	if _, ok := second.Age.Mean(); ok {
		t.Fatal("missing became zero")
	}
	clone := s.Clone()
	clone.Dimensions[0].Sent = 100
	if s.Dimensions[0].Sent == 100 {
		t.Fatal("clone aliases input")
	}
	MergeMessagingTelemetry(&s, &ServerTelemetry{MessagesSent: 1, PartitionID: "0"}, "topic-b")
	if _, ok := s.ObservedPartitionSkew(); ok {
		t.Fatal("pooled sources scored")
	}
}
func TestMessagingBoundedMergeAndValidation(t *testing.T) {
	var s *MessagingSummary
	for i := 0; i < MaxMessagingDimensions+10; i++ {
		MergeMessagingTelemetry(&s, &ServerTelemetry{MessagesSent: 1, PartitionID: fmt.Sprint(i)}, "topic")
	}
	if len(s.Dimensions) != MaxMessagingDimensions || !s.Incomplete {
		t.Fatalf("unbounded: %+v", s)
	}
	if _, ok := s.ObservedPartitionSkew(); ok {
		t.Fatal("partial partitions scored")
	}
	for _, bad := range []*MessagingSummary{{Dimensions: []MessagingDimension{{Operations: -1}}}, {Dimensions: []MessagingDimension{{Operations: 1, Ack: MessagingMoments{Count: 1, Sum: math.NaN()}}}}, {Dimensions: []MessagingDimension{{Source: "a\nsecret", Operations: 1}}}, {Dimensions: []MessagingDimension{{Operations: 1}, {Operations: 1}}}} {
		var out *MessagingSummary
		MergeMessagingSummary(&out, bad)
		if out == nil || !out.Incomplete || len(out.Dimensions) != 0 {
			t.Fatalf("invalid evidence accepted: %+v", out)
		}
	}
}
func TestMessagingMergeMatchesSequential(t *testing.T) {
	var one, two, sequential, merged *MessagingSummary
	for i := 0; i < 10; i++ {
		v := float64(i)
		tel := &ServerTelemetry{MessagesReceived: 1, PartitionID: "0", ConsumerLagObserved: true, ConsumerLagMs: v, AcknowledgementMs: &v}
		MergeMessagingTelemetry(&sequential, tel, "topic")
		if i < 3 {
			MergeMessagingTelemetry(&one, tel, "topic")
		} else {
			MergeMessagingTelemetry(&two, tel, "topic")
		}
	}
	MergeMessagingSummary(&merged, one)
	MergeMessagingSummary(&merged, two)
	if merged.Dimensions[0] != sequential.Dimensions[0] {
		t.Fatalf("merge mismatch: %+v %+v", merged, sequential)
	}
}

func TestMessagingOverflowAndMapAdmission(t *testing.T) {
	if (MessagingMoments{Sum: 1e-10}).valid() {
		t.Fatal("nonempty missing moments")
	}
	s := &MessagingSummary{Dimensions: []MessagingDimension{{Source: "topic", Operations: 1e12, Sent: 1e12}}}
	MergeMessagingSummary(&s, &MessagingSummary{Dimensions: []MessagingDimension{{Source: "topic", Operations: 1, Sent: 1}}})
	if !s.Valid() || !s.Incomplete || s.Dimensions[0].Sent != 1e12 {
		t.Fatal("overflow admitted")
	}
	in := map[string]*MessagingSummary{}
	for i := 0; i < 150; i++ {
		in[fmt.Sprint(i)] = s
	}
	out := CloneMessagingSummaries(in)
	if len(out) > 128 || !out["__other__"].Incomplete {
		t.Fatal("map overflow hidden")
	}
}
