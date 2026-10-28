package javarunner

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMeasureApprovedObjectFootprintFixtures(t *testing.T) {
	ctx := context.Background()
	if _, err := ResolveJDK(ctx, ResolveOptions{}); err != nil {
		t.Skipf("JDK unavailable: %v", err)
	}

	t.Run("primitives_without_codec", func(t *testing.T) {
		res, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "primitives",
		})
		if err != nil {
			t.Fatalf("MeasureApprovedObjectFootprint(primitives): %v", err)
		}
		if res.JVMVersion == "" {
			t.Fatal("expected non-empty JVMVersion")
		}
		if !res.ShallowObject.Available || res.ShallowObject.Method != ShallowObjectMethodV1 || res.ShallowObject.Bytes == nil || *res.ShallowObject.Bytes <= 0 {
			t.Fatalf("unexpected shallow footprint: %+v", res.ShallowObject)
		}
		if !res.ReachableGraph.Available || res.ReachableGraph.Method != ReachableGraphMethodV1 || res.ReachableGraph.Bytes == nil {
			t.Fatalf("unexpected reachable graph footprint: %+v", res.ReachableGraph)
		}
		// Root PrimitiveBoxFixture + boxedLong + boxedInt = 3 unique objects.
		if res.ReachableGraph.UniqueObjects != 3 {
			t.Fatalf("UniqueObjects = %d, want 3", res.ReachableGraph.UniqueObjects)
		}
		if *res.ReachableGraph.Bytes <= *res.ShallowObject.Bytes {
			t.Fatalf("expected reachable graph bytes (%d) > shallow bytes (%d)", *res.ReachableGraph.Bytes, *res.ShallowObject.Bytes)
		}
		if res.ReachableGraph.TraversalExhausted {
			t.Fatal("unexpected traversal_exhausted for small primitive fixture")
		}
		if res.ShallowObject.RetainedHeapNote != ObjectFootprintRetainedNote || res.ReachableGraph.RetainedHeapNote != ObjectFootprintRetainedNote {
			t.Fatal("missing retained heap note")
		}
		if res.CodecExecuted || res.JavaSerializationMs != nil || res.JavaDeserializationMs != nil || res.SerializedBytes != nil {
			t.Fatalf("expected no codec timings when RunCodec=false, got %+v", res)
		}
	})

	t.Run("arrays_scaling", func(t *testing.T) {
		small, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "arrays",
			ArrayLength: 64,
		})
		if err != nil {
			t.Fatalf("small arrays: %v", err)
		}
		large, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "arrays",
			ArrayLength: 4096,
		})
		if err != nil {
			t.Fatalf("large arrays: %v", err)
		}
		// ArrayFixture + byte[] + int[] = 3 unique objects.
		if small.ReachableGraph.UniqueObjects != 3 || large.ReachableGraph.UniqueObjects != 3 {
			t.Fatalf("unexpected unique objects: small=%d large=%d", small.ReachableGraph.UniqueObjects, large.ReachableGraph.UniqueObjects)
		}
		if *large.ReachableGraph.Bytes-*small.ReachableGraph.Bytes < 3500 {
			t.Fatalf("expected reachable graph bytes to scale with byte[] length: small=%d large=%d", *small.ReachableGraph.Bytes, *large.ReachableGraph.Bytes)
		}
	})

	t.Run("shared_diamond_deduplicated_by_identity", func(t *testing.T) {
		res, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "shared_diamond",
		})
		if err != nil {
			t.Fatalf("shared_diamond: %v", err)
		}
		// DiamondRoot (1) + left DiamondBranch (2) + right DiamondBranch (3) + SharedLeaf (4) + SharedLeaf.sharedPayload byte[128] (5) = 5 unique objects.
		if res.ReachableGraph.UniqueObjects != 5 {
			t.Fatalf("shared diamond UniqueObjects = %d, want 5 (shared leaf and byte[] counted once)", res.ReachableGraph.UniqueObjects)
		}
		if res.ReachableGraph.TraversalExhausted {
			t.Fatal("unexpected traversal_exhausted on shared diamond")
		}
	})

	t.Run("cyclic_ring_terminates_and_deduplicates", func(t *testing.T) {
		res, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "cyclic_ring",
			ChainDepth:  6,
		})
		if err != nil {
			t.Fatalf("cyclic_ring: %v", err)
		}
		if res.ReachableGraph.UniqueObjects != 6 {
			t.Fatalf("cyclic ring UniqueObjects = %d, want 6", res.ReachableGraph.UniqueObjects)
		}
		if res.ReachableGraph.TraversalExhausted {
			t.Fatal("unexpected traversal_exhausted on cyclic ring within limits")
		}
	})

	t.Run("depth_and_node_bounds_mark_traversal_exhausted", func(t *testing.T) {
		depthLimited, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "deep_chain",
			ChainDepth:  10,
			MaxDepth:    3,
			MaxNodes:    100,
		})
		if err != nil {
			t.Fatalf("deep_chain depth limited: %v", err)
		}
		if !depthLimited.ReachableGraph.TraversalExhausted {
			t.Fatal("expected traversal_exhausted=true when chain exceeds max_depth")
		}
		if depthLimited.ReachableGraph.MaxDepth != 3 {
			t.Fatalf("MaxDepth = %d, want 3", depthLimited.ReachableGraph.MaxDepth)
		}

		nodeLimited, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "deep_chain",
			ChainDepth:  10,
			MaxDepth:    64,
			MaxNodes:    4,
		})
		if err != nil {
			t.Fatalf("deep_chain node limited: %v", err)
		}
		if !nodeLimited.ReachableGraph.TraversalExhausted {
			t.Fatal("expected traversal_exhausted=true when graph exceeds max_nodes")
		}
		if nodeLimited.ReachableGraph.UniqueObjects != 4 {
			t.Fatalf("UniqueObjects = %d, want 4 (capped at max_nodes)", nodeLimited.ReachableGraph.UniqueObjects)
		}
	})

	t.Run("approved_codec_execution_records_serialization_and_deserialization_ms", func(t *testing.T) {
		res, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "approved_serializable_record",
			ArrayLength: 512,
			RunCodec:    true,
		})
		if err != nil {
			t.Fatalf("approved_serializable_record with codec: %v", err)
		}
		if !res.CodecExecuted || res.SerializedBytes == nil || *res.SerializedBytes <= 512 {
			t.Fatalf("expected CodecExecuted with SerializedBytes > 512, got %+v", res)
		}
		if res.JavaSerializationMs == nil || *res.JavaSerializationMs <= 0 {
			t.Fatalf("expected positive JavaSerializationMs, got %v", res.JavaSerializationMs)
		}
		if res.JavaDeserializationMs == nil || *res.JavaDeserializationMs <= 0 {
			t.Fatalf("expected positive JavaDeserializationMs, got %v", res.JavaDeserializationMs)
		}
	})

	t.Run("unavailable_javaagent_reports_unavailable_and_labeled_fallback", func(t *testing.T) {
		res, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind:      "shared_diamond",
			DisableJavaAgent: true,
		})
		if err != nil {
			t.Fatalf("DisableJavaAgent: %v", err)
		}
		if res.ShallowObject.Available || res.ShallowObject.Bytes != nil || res.ShallowObject.UnavailableReason != "jvm_instrumentation_agent_unavailable" {
			t.Fatalf("expected unavailable shallow instrumentation, got %+v", res.ShallowObject)
		}
		if res.ShallowObject.FieldLayoutFallbackBytes == nil || *res.ShallowObject.FieldLayoutFallbackBytes <= 0 || res.ShallowObject.FieldLayoutFallbackMethod != FieldLayoutFallbackMethodV1 {
			t.Fatalf("expected labeled field-layout fallback on shallow object, got %+v", res.ShallowObject)
		}
		if res.ReachableGraph.Available || res.ReachableGraph.Bytes != nil || res.ReachableGraph.UnavailableReason != "jvm_instrumentation_agent_unavailable" {
			t.Fatalf("expected unavailable reachable graph instrumentation, got %+v", res.ReachableGraph)
		}
		if res.ReachableGraph.FieldLayoutFallbackBytes == nil || *res.ReachableGraph.FieldLayoutFallbackBytes <= *res.ShallowObject.FieldLayoutFallbackBytes || res.ReachableGraph.FieldLayoutFallbackMethod != FieldLayoutFallbackMethodV1 {
			t.Fatalf("expected labeled field-layout fallback on reachable graph, got %+v", res.ReachableGraph)
		}
		if res.ReachableGraph.UniqueObjects != 5 {
			t.Fatalf("UniqueObjects = %d, want 5", res.ReachableGraph.UniqueObjects)
		}
	})

	t.Run("codec_exception_fails_cleanly", func(t *testing.T) {
		_, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "codec_exception",
			RunCodec:    true,
		})
		if err == nil || !strings.Contains(err.Error(), "simulated approved codec serialization failure") {
			t.Fatalf("expected codec exception failure, got %v", err)
		}
	})

	t.Run("timeout_and_context_cancellation_kill_helper", func(t *testing.T) {
		_, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "sleep_timeout",
			Timeout:     250 * time.Millisecond,
		})
		if err == nil || !strings.Contains(err.Error(), "terminated after context/timeout cancellation") {
			t.Fatalf("expected timeout termination error, got %v", err)
		}
	})

	t.Run("rejects_untrusted_wire_bytes_and_unapproved_fixtures", func(t *testing.T) {
		_, err := MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind:        "primitives",
			UntrustedWireBytes: []byte{0xac, 0xed, 0x00, 0x05},
		})
		if err == nil || !strings.Contains(err.Error(), "untrusted wire bytes are forbidden") {
			t.Fatalf("expected untrusted wire bytes rejection, got %v", err)
		}
		_, err = MeasureApprovedObjectFootprint(ctx, FootprintRequest{
			FixtureKind: "arbitrary_class",
		})
		if err == nil || !strings.Contains(err.Error(), "unapproved footprint fixture kind") {
			t.Fatalf("expected unapproved fixture rejection, got %v", err)
		}
		_, err = translateFootprintWireResponse(
			FootprintRequest{FixtureKind: "primitives", MaxNodes: 4096, MaxDepth: 32},
			footprintHelperWireResponse{
				Status:         "ok",
				FixtureKind:    "primitives",
				JVMVersion:     "21",
				AgentAvailable: false,
				UniqueObjects:  0,
				MaxNodes:       4096,
				MaxDepth:       32,
			},
		)
		if err == nil || !strings.Contains(err.Error(), "inconsistent traversal bounds") {
			t.Fatalf("expected UniqueObjects=0 rejection when AgentAvailable=false, got %v", err)
		}
	})
}
