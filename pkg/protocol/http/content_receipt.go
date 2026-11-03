package http

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/contentdecode"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func addResponseContentReceipt(result *protocol.Result, state, basis string) {
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = make(map[string]any)
	}
	result.ProtocolEvidence["response_content_state"] = state
	if state == "unavailable" {
		delete(result.ProtocolEvidence, "response_content_basis")
	} else {
		result.ProtocolEvidence["response_content_basis"] = basis
	}
}
func fixedResponseReadError(err error) string {
	if contentdecode.Kind(err) != "" {
		return err.Error()
	}
	if err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, "stream_truncated") || strings.HasPrefix(msg, "stream_timeout") {
			return msg
		}
	}
	return "response content read failed"
}
func (h *HTTP) unavailableResponseContentEvidence(result *protocol.Result, outcome bool) {
	if !h.captureCorrelationEvidence {
		return
	}
	result.CorrelationEvidence = protocol.UnavailableCorrelationEvidence("response content is incomplete or unavailable")
	result.CorrelationEvidence.ResponseSuccess = &outcome
}
