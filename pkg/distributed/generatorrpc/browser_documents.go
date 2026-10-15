package generatorrpc

import (
	"encoding/json"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const FeatureBrowserDocumentEvidenceV2 = "browser_document_evidence_v2"

// FeatureBrowserInteractionEvidenceV1 advertises gesture interaction
// evidence capture (E3).
const FeatureBrowserInteractionEvidenceV1 = "browser_interaction_evidence_v1"

// browserRuntimeFeatures are the features a browser runtime instance
// advertises, read the same way when the generator advertises them and when
// it stages an assignment that requires them: the browser runtime has no
// RuntimeCapabilities of its own. Observer sessions and salesforce_jwt are
// part of this generator's engine and browser protocol alike.
func browserRuntimeFeatures(instance protocol.Protocol) []string {
	features := []string{dsl.FeatureBrowserSessionIdentitiesV1, dsl.FeatureBrowserSalesforceJWTV1}
	if _, ok := instance.(protocol.BrowserDocumentEvidenceProvider); ok {
		features = append(features, FeatureBrowserDocumentEvidenceV2)
	}
	if _, ok := instance.(protocol.BrowserInteractionEvidenceProvider); ok {
		features = append(features, FeatureBrowserInteractionEvidenceV1)
	}
	return features
}

const maxBrowserDocumentSummaryBytes = 512 << 10

func browserDocumentsVersion(s *protocol.BrowserDocumentSummary) uint32 {
	if s == nil {
		return 0
	}
	return protocol.BrowserEvidenceVersion
}
func browserDocumentsJSON(s *protocol.BrowserDocumentSummary) string {
	if s == nil {
		return ""
	}
	s = s.Clone()
	if s == nil {
		s = &protocol.BrowserDocumentSummary{Version: protocol.BrowserEvidenceVersion, Incomplete: true}
	}
	data, err := json.Marshal(s)
	if err != nil || len(data) > maxBrowserDocumentSummaryBytes {
		data, _ = json.Marshal(&protocol.BrowserDocumentSummary{Version: protocol.BrowserEvidenceVersion, Incomplete: true})
	}
	return string(data)
}
func browserDocumentsFromJSON(raw string, version uint32) *protocol.BrowserDocumentSummary {
	if raw == "" && version == 0 {
		return nil
	}
	unavailable := func() *protocol.BrowserDocumentSummary {
		return &protocol.BrowserDocumentSummary{Version: protocol.BrowserEvidenceVersion, Incomplete: true}
	}
	if version != protocol.BrowserEvidenceVersion || len(raw) > maxBrowserDocumentSummaryBytes || raw == "" {
		return unavailable()
	}
	clone, err := protocol.DecodeBrowserDocumentSummary([]byte(raw))
	if err != nil {
		return unavailable()
	}
	return clone
}
func browserDocumentsForGroup(s *protocol.BrowserDocumentSummary, group string) *protocol.BrowserDocumentSummary {
	if s == nil {
		return nil
	}
	out := s.Clone()
	if out == nil {
		return &protocol.BrowserDocumentSummary{Version: protocol.BrowserEvidenceVersion, Incomplete: true}
	}
	rows := out.Metrics
	out.Metrics = nil
	for _, row := range rows {
		if row.Group == group {
			out.Metrics = append(out.Metrics, row)
		}
	}
	return out
}
