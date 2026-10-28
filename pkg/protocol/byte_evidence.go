package protocol

import (
	"math"
	"strings"
)

// Byte evidence (NA-C) names what an action's BytesSent and BytesReceived
// measure, so an archive reader or live interval aggregator can tell a
// measured zero from a byte count its protocol does not take. The adapter that
// ran the action sets it on the result, and the archive writer and interval
// aggregator of the same binary keep it; an earlier binary sets nothing, and
// its bytes stay unavailable.

// ByteEvidenceHTTPBody is HTTP message body bytes as they crossed the wire,
// a compressed body counted compressed (after transfer decoding, before
// content decoding):
//   - sent: the body of the request the action dispatched, once, and again
//     for a digest-auth replay that completed (a failed replay is not
//     counted); nothing when the request failed before its headers were
//     written, or was served from the cache. A redirect's re-sent body and
//     an engine retry's earlier attempts are not counted, and a request that
//     failed after its headers were written counts its whole body.
//   - received: the response body, and the bodies of the embedded resources
//     and meta-refresh follow-ups it fetched; nothing for a response served
//     from the cache (fresh, or after a 304).
//
// Headers, framing, TLS and connection setup are not counted.
const ByteEvidenceHTTPBody = "http_body.v1"

// ByteEvidenceGRPCUncompressedPayload is uncompressed gRPC protobuf payload
// bytes observed by the gRPC stats handler across unary, client-stream,
// server-stream, and bidirectional RPCs. HTTP/2 framing, HPACK headers, and
// TLS record overhead are excluded.
const ByteEvidenceGRPCUncompressedPayload = "grpc_uncompressed_payload.v1"

// ByteEvidenceWebSocketApplicationPayload is WebSocket application message
// payload bytes sent or received on data frames (ws_send, ws_receive, and
// callback data messages). WebSocket frame headers, masking keys, control
// frames (ws_ping, ws_close), and connection/dispatch synthetic steps are
// excluded.
const ByteEvidenceWebSocketApplicationPayload = "ws_application_payload.v1"

// ByteEvidenceNone marks a step that dispatched no payload-bearing transfer
// (a check, handshake, ping, close, or an action refused before sending): it
// is not a payload measurement, and its zero bytes are not scored.
const ByteEvidenceNone = "none"

// ByteEvidenceUnmeasured marks an aggregated interval where at least one
// execution lacked measured byte evidence, reported negative or overflowing
// bytes, or mixed incompatible byte evidence contracts.
const ByteEvidenceUnmeasured = "unmeasured"

// ByteEvidence is the byte evidence an archived result carries: what its
// adapter set, except for a business transaction, whose bytes are its
// actions' sums.
func ByteEvidence(result *Result) string {
	if result == nil || result.BusinessTransaction {
		return ""
	}
	return result.ByteEvidence
}

// ArchiveByteMeasurement returns the BytesSent, BytesReceived, and ByteEvidence
// to persist in run archives for result. For gRPC actions with valid RPC
// telemetry, it projects the uncompressed protobuf payload byte counts from
// ServerTelemetry.GRPC so archived byte columns match grpc_uncompressed_payload.v1.
func ArchiveByteMeasurement(result *Result) (sent, received int64, evidence string) {
	if result == nil {
		return 0, 0, ""
	}
	evidence = ByteEvidence(result)
	sent, received = result.BytesSent, result.BytesReceived
	if evidence == ByteEvidenceGRPCUncompressedPayload && result.ServerTelemetry != nil && result.ServerTelemetry.GRPC != nil && result.ServerTelemetry.GRPC.Valid() {
		sent = result.ServerTelemetry.GRPC.RequestBytes
		received = result.ServerTelemetry.GRPC.ResponseBytes
	}
	return sent, received, evidence
}

// IsMeasuredByteEvidence reports whether evidence names a measured protocol
// byte contract in the byte query catalog.
func IsMeasuredByteEvidence(evidence string) bool {
	switch strings.TrimSpace(evidence) {
	case ByteEvidenceHTTPBody, ByteEvidenceGRPCUncompressedPayload, ByteEvidenceWebSocketApplicationPayload:
		return true
	default:
		return false
	}
}

// ExpectedByteEvidenceForProtocol returns the measured byte evidence provenance
// required for a canonical or alias protocol name.
func ExpectedByteEvidenceForProtocol(proto string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(proto)) {
	case "http", "https", "http1", "http2", "http3":
		return ByteEvidenceHTTPBody, true
	case "grpc":
		return ByteEvidenceGRPCUncompressedPayload, true
	case "websocket", "ws", "wss":
		return ByteEvidenceWebSocketApplicationPayload, true
	default:
		return "", false
	}
}

// ProtocolForByteEvidence returns the canonical protocol family for a measured
// byte evidence contract.
func ProtocolForByteEvidence(evidence string) string {
	switch strings.TrimSpace(evidence) {
	case ByteEvidenceHTTPBody:
		return "http"
	case ByteEvidenceGRPCUncompressedPayload:
		return "grpc"
	case ByteEvidenceWebSocketApplicationPayload:
		return "websocket"
	default:
		return ""
	}
}

// ObserveResultBytes extracts the measured byte provenance and payload byte
// counts from a single protocol result. For gRPC results that still carry
// ServerTelemetry.GRPC, it reads the uncompressed payload byte counters from
// ServerTelemetry.GRPC; once concurrent public evidence is cleared, BytesSent
// and BytesReceived hold those uncompressed payload counts directly.
func ObserveResultBytes(result *Result) (evidence string, sent, received int64, measured bool, valid bool) {
	if result == nil {
		return "", 0, 0, false, false
	}
	if result.BusinessTransaction {
		return ByteEvidenceNone, 0, 0, false, true
	}
	ev := strings.TrimSpace(result.ByteEvidence)
	if ev == ByteEvidenceNone {
		return ByteEvidenceNone, 0, 0, false, true
	}
	if !IsMeasuredByteEvidence(ev) {
		return "", 0, 0, false, false
	}
	sent, received = result.BytesSent, result.BytesReceived
	if ev == ByteEvidenceGRPCUncompressedPayload && result.ServerTelemetry != nil && result.ServerTelemetry.GRPC != nil {
		if !result.ServerTelemetry.GRPC.Valid() {
			return "", 0, 0, false, false
		}
		sent = result.ServerTelemetry.GRPC.RequestBytes
		received = result.ServerTelemetry.GRPC.ResponseBytes
	}
	if sent < 0 || received < 0 {
		return "", 0, 0, false, false
	}
	return ev, sent, received, true, true
}

func addClampedByteCount(dst *int64, delta int64) bool {
	if delta < 0 {
		return false
	}
	if *dst > math.MaxInt64-delta {
		*dst = math.MaxInt64
		return false
	}
	*dst += delta
	return true
}

// RecordIntervalByteSample folds a single result's byte observation into an
// interval accumulator.
func RecordIntervalByteSample(dstEvidence *string, dstSamples, dstSent, dstReceived *int64, result *Result) {
	if dstEvidence == nil || dstSamples == nil || dstSent == nil || dstReceived == nil {
		return
	}
	ev, sent, received, measured, valid := ObserveResultBytes(result)
	if !valid {
		*dstEvidence = ByteEvidenceUnmeasured
		*dstSamples = 0
		if result != nil && result.BytesSent >= 0 && result.BytesReceived >= 0 {
			addClampedByteCount(dstSent, result.BytesSent)
			addClampedByteCount(dstReceived, result.BytesReceived)
		}
		return
	}
	if !measured {
		if *dstEvidence == "" {
			*dstEvidence = ByteEvidenceNone
		}
		return
	}
	sentOK := addClampedByteCount(dstSent, sent)
	recvOK := addClampedByteCount(dstReceived, received)
	if *dstEvidence == ByteEvidenceUnmeasured {
		*dstSamples = 0
		return
	}
	if *dstEvidence == "" || *dstEvidence == ByteEvidenceNone {
		*dstEvidence = ev
	} else if *dstEvidence != ev {
		*dstEvidence = ByteEvidenceUnmeasured
		*dstSamples = 0
		return
	}
	if !sentOK || !recvOK || *dstSamples == math.MaxInt64 {
		*dstEvidence = ByteEvidenceUnmeasured
		*dstSamples = 0
		return
	}
	*dstSamples++
}

// MergeIntervalByteEvidence folds an interval's byte accumulator into a
// destination accumulator across actions or generator peers.
func MergeIntervalByteEvidence(dstEvidence *string, dstSamples, dstSent, dstReceived *int64, srcCount int64, srcEvidence string, srcSamples, srcSent, srcReceived int64) {
	if dstEvidence == nil || dstSamples == nil || dstSent == nil || dstReceived == nil {
		return
	}
	srcEv := strings.TrimSpace(srcEvidence)
	if srcCount <= 0 && srcSamples <= 0 && srcEv == "" {
		return
	}
	if srcSent < 0 || srcReceived < 0 || srcSamples < 0 {
		*dstEvidence = ByteEvidenceUnmeasured
		*dstSamples = 0
		return
	}
	if srcEv == ByteEvidenceNone {
		if *dstEvidence == "" {
			*dstEvidence = ByteEvidenceNone
		}
		return
	}
	sentOK := addClampedByteCount(dstSent, srcSent)
	recvOK := addClampedByteCount(dstReceived, srcReceived)
	if srcEv == ByteEvidenceUnmeasured || (srcCount > 0 && srcEv == "") || !IsMeasuredByteEvidence(srcEv) || srcSamples <= 0 {
		*dstEvidence = ByteEvidenceUnmeasured
		*dstSamples = 0
		return
	}
	if *dstEvidence == ByteEvidenceUnmeasured {
		*dstSamples = 0
		return
	}
	if *dstEvidence == "" || *dstEvidence == ByteEvidenceNone {
		*dstEvidence = srcEv
	} else if *dstEvidence != srcEv {
		*dstEvidence = ByteEvidenceUnmeasured
		*dstSamples = 0
		return
	}
	if !sentOK || !recvOK || *dstSamples > math.MaxInt64-srcSamples {
		*dstEvidence = ByteEvidenceUnmeasured
		*dstSamples = 0
		return
	}
	*dstSamples += srcSamples
}
