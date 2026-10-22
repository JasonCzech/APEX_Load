package protocol

// Byte evidence (NA-C) names what an action's BytesSent and BytesReceived
// measure, so an archive reader can tell a measured zero from a byte count
// its protocol does not take. The adapter that ran the action sets it on the
// result, and the archive writer of the same binary keeps it; an earlier
// binary sets nothing, and its bytes stay unavailable.
//
// Only the HTTP adapter sets it today; SOAP and Java HTTP, which wrap it,
// keep its label, but their actions are outside the byte population. gRPC
// results count the JSON representation of their messages (the payload
// bytes are in its diagnostics), and a WebSocket close counts its payload
// even when the write failed.

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

// ByteEvidenceNone marks an HTTP step that dispatched no request (a check,
// or an action refused before sending): it is not a request, and its zero
// bytes are not measurements.
const ByteEvidenceNone = "none"

// ByteEvidence is the byte evidence an archived result carries: what its
// adapter set, except for a business transaction, whose bytes are its
// actions' sums.
func ByteEvidence(result *Result) string {
	if result == nil || result.BusinessTransaction {
		return ""
	}
	return result.ByteEvidence
}
