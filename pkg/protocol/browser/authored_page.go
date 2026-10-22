package browser

import (
	"context"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
	"time"
)

// noteAuthoredPageResponse observes only the response returned by an explicit
// navigation command. It does not issue a target request or use URL equality to
// attribute a page. Missing/ambiguous native joins remain unavailable.
func (b *Browser) noteAuthoredPageResponse(ctx context.Context, action *protocol.Action, result *protocol.Result, response playwright.Response) {
	b.noteCausalDocumentResponse(ctx, action, response)
	if action == nil || result == nil {
		return
	}
	receipt := protocol.BrowserAuthoredPageBinding{Version: 1, State: "unavailable", Reason: "original_source_unavailable", ObservedAt: time.Now().UTC()}
	defer func() {
		if result.ProtocolEvidence == nil {
			result.ProtocolEvidence = map[string]any{}
		}
		result.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey] = receipt.Clone()
	}()
	if !action.BrowserAuthoredStep.Valid() || action.BrowserAuthoredStep.Version != protocol.BrowserOriginalAuthoredStepVersion {
		return
	}
	receipt.AuthoredStep = action.BrowserAuthoredStep.Clone()
	// History may restore an existing lifecycle and redirects may involve a
	// different request. Only a newly committed navigation/reload/explicit new-window response is
	// considered by this version; unsupported cases never acquire a guessed key.
	if action.Type != "browser_navigate" && action.Type != "browser_reload" && action.Type != "browser_open_window" {
		receipt.Reason = "unsupported_navigation_binding"
		return
	}
	receipt.Reason = "navigation_response_unavailable"
	if response == nil {
		return
	}
	request := response.Request()
	if request == nil || !request.IsNavigationRequest() {
		return
	}
	receipt.Reason = "navigation_join_unavailable"
	b.mu.Lock()
	identity := b.evidence.requests[request]
	if identity == nil {
		b.mu.Unlock()
		return
	}
	evidence, join, generation := identity.evidence, identity.join, b.evidence.generation
	b.mu.Unlock()
	if b.engine == "webkit" {
		b.bindNativeAuthoredPage(ctx, request, evidence, generation, &receipt)
		return
	}
	evidence.ObservedAt = time.Now().UTC()
	evidence.State = "finished"
	populated := populateResourceEvidence(&evidence, request, response, nil)
	joined := b.associateResourceEvidence(populated, join, generation)
	if joined == nil || joined.DocumentAssociation != "cdp_loader_join" || joined.DocumentID == "" {
		b.bindNativeAuthoredPage(ctx, request, evidence, generation, &receipt)
		return
	}
	receipt.Reason = "document_lifecycle_unavailable"
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != generation {
		return
	}
	var lifecycle string
	for _, document := range b.evidence.documents {
		if document.documentID != joined.DocumentID || document.restored {
			continue
		}
		if lifecycle != "" && lifecycle != document.lifecycleID {
			return
		}
		lifecycle = document.lifecycleID
	}
	if lifecycle == "" {
		return
	}
	receipt.State, receipt.Reason = "bound", ""
	receipt.PageID = protocol.BrowserAuthoredPageID(receipt.AuthoredStep)
	receipt.SourceGeneration, receipt.RequestID = joined.SourceGeneration, joined.RequestID
	receipt.DocumentID, receipt.LifecycleID = joined.DocumentID, lifecycle
	receipt.Association = joined.DocumentAssociation
}
