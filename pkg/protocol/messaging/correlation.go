package messaging

// CorrelationMatches reports whether a reply carrying replyID satisfies a request
// expecting expectedID, using the permissive JMS-style rule: an empty expected or
// reply correlation ID matches, since some brokers omit the correlation ID on
// simple echo replies. It centralizes the matching previously duplicated across
// the JMS request-reply adapters.
//
// Note: request-reply requeue-safety for brokers that consume-to-peek (e.g. Azure
// Service Bus completing uncorrelated replies instead of abandoning them) requires
// client-controlled settlement, which is delivered with the Service Bus settlement
// work (P2.4); this helper only owns the match decision.
func CorrelationMatches(expectedID, replyID string) bool {
	return expectedID == "" || replyID == "" || replyID == expectedID
}
