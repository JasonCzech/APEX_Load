package browser

import (
	"context"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
	"time"
)

func (b *Browser) authoredPageV3Enabled() bool {
	return b.interactionVersion >= protocol.BrowserInteractionVersion4 || b.authoredPageVersion >= protocol.BrowserAuthoredPageVersion3
}

func (b *Browser) recordNavPreSequence(page playwright.Page) uint64 {
	var seq uint64
	if page != nil && page.MainFrame() != nil {
		seq = playwright.APEXFrameNavigationCommit(page.MainFrame()).Sequence
	}
	b.mu.Lock()
	b.currentNavPreSequence = seq
	b.mu.Unlock()
	return seq
}

func (b *Browser) rememberActivePageBinding(page playwright.Page, receipt *protocol.BrowserAuthoredPageBinding) {
	if page == nil || receipt == nil || receipt.State != "bound" || !receipt.Valid() {
		return
	}
	cloned := receipt.Clone()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activePageBindings == nil {
		b.activePageBindings = make(map[playwright.Page]*protocol.BrowserAuthoredPageBinding)
	}
	b.activePageBindings[page] = cloned
	if b.lifecycleRouteSeq == nil {
		b.lifecycleRouteSeq = make(map[string]uint64)
	}
	key := receipt.SourceGeneration + "/" + receipt.DocumentID + "/" + receipt.LifecycleID
	if receipt.RouteSeq > b.lifecycleRouteSeq[key] {
		b.lifecycleRouteSeq[key] = receipt.RouteSeq
	} else if _, exists := b.lifecycleRouteSeq[key]; !exists {
		b.lifecycleRouteSeq[key] = receipt.RouteSeq
	}
}

func (b *Browser) activePageBindingForPage(page playwright.Page) *protocol.BrowserAuthoredPageBinding {
	if page == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activePageBindings == nil {
		return nil
	}
	return b.activePageBindings[page].Clone()
}

func (b *Browser) nextLifecycleRouteSeqLocked(generation, documentID, lifecycleID string) uint64 {
	if b.lifecycleRouteSeq == nil {
		b.lifecycleRouteSeq = make(map[string]uint64)
	}
	key := generation + "/" + documentID + "/" + lifecycleID
	next := b.lifecycleRouteSeq[key] + 1
	b.lifecycleRouteSeq[key] = next
	return next
}

// noteAuthoredPageResponse observes only the response returned by an explicit
// navigation command. It does not issue a target request or use URL equality to
// attribute a page. Missing/ambiguous native joins remain unavailable.
func (b *Browser) noteAuthoredPageResponse(ctx context.Context, action *protocol.Action, result *protocol.Result, response playwright.Response) {
	b.noteCausalDocumentResponse(ctx, action, response)
	if action == nil || result == nil {
		return
	}
	receipt := protocol.BrowserAuthoredPageBinding{Version: 1, State: "unavailable", Reason: "original_source_unavailable", ObservedAt: time.Now().UTC()}
	if b.authoredPageVersion >= protocol.BrowserAuthoredPageVersion3 {
		receipt.Version = protocol.BrowserAuthoredPageVersion3
	}
	defer func() {
		if receipt.State == "bound" {
			b.rememberActivePageBinding(b.currentPage(), &receipt)
		}
		if result.ProtocolEvidence == nil {
			result.ProtocolEvidence = map[string]any{}
		}
		result.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey] = receipt.Clone()
	}()
	if !action.BrowserAuthoredStep.Valid() || action.BrowserAuthoredStep.Version != protocol.BrowserOriginalAuthoredStepVersion {
		return
	}
	receipt.AuthoredStep = action.BrowserAuthoredStep.Clone()
	if b.authoredPageV3Enabled() {
		switch action.Type {
		case "browser_back", "browser_forward":
			b.bindV3HistoryTraversalPage(ctx, response, &receipt)
			return
		case "browser_navigate":
			if response == nil {
				b.bindV3SameDocumentRoutePage(ctx, b.currentPage(), &receipt)
				return
			}
			if page := b.currentPage(); page != nil && page.MainFrame() != nil {
				commit := playwright.APEXFrameNavigationCommit(page.MainFrame())
				if commit.Sequence > 0 && commit.NewDocument && !commit.Failed && commit.Request != nil && response.Request() != nil && (commit.Request != response.Request() || response.Request().RedirectedFrom() != nil) {
					b.bindV3RedirectChainPage(ctx, commit.Request, &receipt)
					return
				}
			}
		case "browser_reload":
			if response != nil {
				if page := b.currentPage(); page != nil && page.MainFrame() != nil {
					commit := playwright.APEXFrameNavigationCommit(page.MainFrame())
					if commit.Sequence > 0 && commit.NewDocument && !commit.Failed && commit.Request != nil && response.Request() != nil && (commit.Request != response.Request() || response.Request().RedirectedFrom() != nil) {
						b.bindV3RedirectChainPage(ctx, commit.Request, &receipt)
						return
					}
				}
			}
		case "browser_open_window":
			if response == nil || b.authoredPageVersion >= protocol.BrowserAuthoredPageVersion3 {
				b.bindV3OpenedByActionPage(ctx, b.currentPage(), response, &receipt)
				return
			}
		}
	}
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
	receipt.Version = protocol.BrowserAuthoredPageVersion
	receipt.State, receipt.Reason = "bound", ""
	receipt.PageID = protocol.BrowserAuthoredPageID(receipt.AuthoredStep)
	receipt.SourceGeneration, receipt.RequestID = joined.SourceGeneration, joined.RequestID
	receipt.DocumentID, receipt.LifecycleID = joined.DocumentID, lifecycle
	receipt.Association = joined.DocumentAssociation
}
