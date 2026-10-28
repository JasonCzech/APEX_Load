package browser

import (
	"context"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
	"time"
)

type nativeAuthoredDocumentIdentity struct{ document, lifecycle string }

// One pending native evaluation per Browser, even after its caller times out.
// A late result has no publication path; teardown owns the native context.
func (b *Browser) readNativeAuthoredIdentity(ctx context.Context, frame playwright.Frame, finalizer string) (nativeAuthoredDocumentIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if frame == nil || finalizer == "" {
		return nativeAuthoredDocumentIdentity{}, errors.New("native identity reader unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nativeAuthoredDocumentIdentity{}, err
	}
	b.mu.Lock()
	if b.authoredIdentityReadPending {
		b.mu.Unlock()
		return nativeAuthoredDocumentIdentity{}, errors.New("native identity read already pending")
	}
	b.authoredIdentityReadPending = true
	b.mu.Unlock()
	type reply struct {
		identity nativeAuthoredDocumentIdentity
		err      error
	}
	done := make(chan reply, 1)
	go func() {
		defer func() { b.mu.Lock(); b.authoredIdentityReadPending = false; b.mu.Unlock() }()
		if err := ctx.Err(); err != nil {
			done <- reply{err: err}
			return
		}
		value, err := frame.Evaluate(fmt.Sprintf(`() => { const value=globalThis[%q]?.identity?.(); if(typeof value?.document_token!=='string'||typeof value?.lifecycle_token!=='string'||value.document_token.length>64||value.lifecycle_token.length>64)return null; return [value.document_token,value.lifecycle_token]; }`, finalizer))
		if err != nil {
			done <- reply{err: err}
			return
		}
		values, ok := value.([]any)
		if !ok || len(values) != 2 {
			done <- reply{err: errors.New("document identity unavailable")}
			return
		}
		document, _ := values[0].(string)
		lifecycle, _ := values[1].(string)
		if !browserDocumentToken(document) || !browserDocumentToken(lifecycle) {
			done <- reply{err: errors.New("invalid document identity")}
			return
		}
		done <- reply{identity: nativeAuthoredDocumentIdentity{document: document, lifecycle: lifecycle}}
	}()
	select {
	case response := <-done:
		if err := ctx.Err(); err != nil {
			return nativeAuthoredDocumentIdentity{}, err
		}
		return response.identity, response.err
	case <-ctx.Done():
		return nativeAuthoredDocumentIdentity{}, ctx.Err()
	}
}

func (b *Browser) bindNativeAuthoredPage(ctx context.Context, request playwright.Request, evidence protocol.BrowserResourceEvidence, generation string, receipt *protocol.BrowserAuthoredPageBinding) {
	frame := request.Frame()
	before := playwright.APEXFrameNavigationCommit(frame)
	if before.Sequence == 0 || before.Sequence > 9007199254740991 || before.Request != request {
		return
	}
	b.mu.Lock()
	finalizer := b.evidence.finalizer
	b.mu.Unlock()
	if finalizer == "" {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	identity, err := b.readNativeAuthoredIdentity(readCtx, frame, finalizer)
	if err != nil {
		return
	}
	after := playwright.APEXFrameNavigationCommit(frame)
	if after.Sequence != before.Sequence || after.Request != request || readCtx.Err() != nil {
		return
	}
	receipt.Reason = "document_lifecycle_unavailable"
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != generation {
		return
	}
	var matched *browserDocumentState
	for _, document := range b.evidence.documents {
		if document.frame != frame || document.token != identity.document || document.lifecycleToken != identity.lifecycle || document.restored {
			continue
		}
		if matched != nil {
			return
		}
		matched = document
	}
	if matched == nil {
		return
	}
	receipt.Version, receipt.State, receipt.Reason = 2, "bound", ""
	receipt.PageID = protocol.BrowserAuthoredPageID(receipt.AuthoredStep)
	receipt.SourceGeneration, receipt.RequestID = generation, evidence.RequestID
	receipt.DocumentID, receipt.LifecycleID = matched.documentID, matched.lifecycleID
	receipt.Association = "native_navigation_commit"
	receipt.NavigationSequence = before.Sequence
	receipt.ObservedAt = time.Now().UTC()
}

func (b *Browser) resolveNativeFrameDocument(ctx context.Context, frame playwright.Frame, expectedSeq uint64, allowRestored bool) (documentID, lifecycleID, generation string, navSeq uint64, ok bool) {
	if frame == nil {
		return "", "", "", 0, false
	}
	b.mu.Lock()
	finalizer := b.evidence.finalizer
	generation = b.evidence.generation
	b.mu.Unlock()
	if finalizer == "" || generation == "" {
		return "", "", "", 0, false
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	identity, err := b.readNativeAuthoredIdentity(readCtx, frame, finalizer)
	if err != nil {
		return "", "", "", 0, false
	}
	after := playwright.APEXFrameNavigationCommit(frame)
	if after.Failed || after.Sequence > 9007199254740991 || (expectedSeq > 0 && after.Sequence != expectedSeq) || readCtx.Err() != nil {
		return "", "", "", 0, false
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		b.mu.Lock()
		if b.evidence.generation != generation {
			b.mu.Unlock()
			return "", "", "", 0, false
		}
		var matched *browserDocumentState
		var ambiguous bool
		for _, document := range b.evidence.documents {
			if document.frame != frame || document.token != identity.document || document.lifecycleToken != identity.lifecycle || (!allowRestored && document.restored) {
				continue
			}
			if matched != nil {
				ambiguous = true
				break
			}
			matched = document
		}
		if ambiguous {
			b.mu.Unlock()
			return "", "", "", 0, false
		}
		if matched != nil {
			docID, lifeID := matched.documentID, matched.lifecycleID
			b.mu.Unlock()
			seq := after.Sequence
			if seq == 0 {
				seq = 1
			}
			return docID, lifeID, generation, seq, true
		}
		b.mu.Unlock()
		if time.Now().After(deadline) || readCtx.Err() != nil {
			return "", "", "", 0, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (b *Browser) requestEvidenceID(request playwright.Request) string {
	if request == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if id := b.evidence.requests[request]; id != nil && b.evidence.generation == id.evidence.SourceGeneration {
		return id.evidence.RequestID
	}
	return ""
}

func (b *Browser) bindV3HistoryTraversalPage(ctx context.Context, response playwright.Response, receipt *protocol.BrowserAuthoredPageBinding) {
	receipt.Version = protocol.BrowserAuthoredPageVersion3
	receipt.State = "unavailable"
	receipt.Reason = protocol.BrowserAuthoredPageUnavailableHistoryTraversal
	page := b.currentPage()
	if page == nil || page.MainFrame() == nil {
		return
	}
	b.mu.Lock()
	beforeSeq := b.currentNavPreSequence
	b.mu.Unlock()
	commit := playwright.APEXFrameNavigationCommit(page.MainFrame())
	if commit.Sequence <= beforeSeq || commit.Failed {
		return
	}
	reqID := ""
	if response != nil && response.Request() != nil {
		if commit.Request != nil && commit.Request != response.Request() {
			return
		}
		reqID = b.requestEvidenceID(response.Request())
		if reqID == "" {
			receipt.Reason = "navigation_join_unavailable"
			return
		}
	} else if commit.Request != nil {
		reqID = b.requestEvidenceID(commit.Request)
	}
	b.bindV3HistoryTraversalCommit(ctx, page.MainFrame(), commit, reqID, receipt)
}

func (b *Browser) bindV3HistoryTraversalCommit(ctx context.Context, frame playwright.Frame, commit playwright.APEXNavigationCommit, reqID string, receipt *protocol.BrowserAuthoredPageBinding) {
	receipt.Version = protocol.BrowserAuthoredPageVersion3
	receipt.State = "unavailable"
	receipt.Reason = "document_lifecycle_unavailable"
	docID, lifeID, generation, navSeq, ok := b.resolveNativeFrameDocument(ctx, frame, commit.Sequence, true)
	if !ok {
		return
	}
	b.mu.Lock()
	var routeSeq uint64
	key := generation + "/" + docID + "/" + lifeID
	if commit.SameDocument {
		routeSeq = b.nextLifecycleRouteSeqLocked(generation, docID, lifeID)
	} else if _, exists := b.lifecycleRouteSeq[key]; exists {
		routeSeq = b.nextLifecycleRouteSeqLocked(generation, docID, lifeID)
	} else {
		if b.lifecycleRouteSeq == nil {
			b.lifecycleRouteSeq = make(map[string]uint64)
		}
		b.lifecycleRouteSeq[key] = 0
	}
	b.mu.Unlock()
	receipt.State, receipt.Reason = "bound", ""
	receipt.PageID = protocol.BrowserAuthoredPageID(receipt.AuthoredStep)
	receipt.SourceGeneration, receipt.RequestID = generation, reqID
	receipt.DocumentID, receipt.LifecycleID = docID, lifeID
	receipt.Association = protocol.BrowserAuthoredPageAssociationHistoryTraversal
	receipt.NavigationSequence = navSeq
	receipt.RouteSeq = routeSeq
	receipt.ObservedAt = time.Now().UTC()
}

func (b *Browser) bindV3SameDocumentRoutePage(ctx context.Context, page playwright.Page, receipt *protocol.BrowserAuthoredPageBinding) {
	receipt.Version = protocol.BrowserAuthoredPageVersion3
	receipt.State = "unavailable"
	receipt.Reason = protocol.BrowserAuthoredPageUnavailableSameDocumentRoute
	if page == nil || page.MainFrame() == nil {
		return
	}
	b.mu.Lock()
	beforeSeq := b.currentNavPreSequence
	b.mu.Unlock()
	commit := playwright.APEXFrameNavigationCommit(page.MainFrame())
	if commit.Sequence <= beforeSeq || commit.Failed || !commit.SameDocument {
		return
	}
	b.bindV3SameDocumentRouteWithCommit(ctx, page.MainFrame(), commit, receipt)
}

func (b *Browser) bindV3SameDocumentRouteWithCommit(ctx context.Context, frame playwright.Frame, commit playwright.APEXNavigationCommit, receipt *protocol.BrowserAuthoredPageBinding) {
	receipt.Version = protocol.BrowserAuthoredPageVersion3
	receipt.State = "unavailable"
	receipt.Reason = "document_lifecycle_unavailable"
	docID, lifeID, generation, navSeq, ok := b.resolveNativeFrameDocument(ctx, frame, commit.Sequence, true)
	if !ok {
		return
	}
	b.mu.Lock()
	routeSeq := b.nextLifecycleRouteSeqLocked(generation, docID, lifeID)
	b.mu.Unlock()
	receipt.State, receipt.Reason = "bound", ""
	receipt.PageID = protocol.BrowserAuthoredPageID(receipt.AuthoredStep)
	receipt.SourceGeneration, receipt.RequestID = generation, ""
	receipt.DocumentID, receipt.LifecycleID = docID, lifeID
	receipt.Association = protocol.BrowserAuthoredPageAssociationSameDocumentRoute
	receipt.NavigationSequence = navSeq
	receipt.RouteSeq = routeSeq
	receipt.ObservedAt = time.Now().UTC()
}

func (b *Browser) bindV3OpenedByActionPage(ctx context.Context, page playwright.Page, response playwright.Response, receipt *protocol.BrowserAuthoredPageBinding) {
	receipt.Version = protocol.BrowserAuthoredPageVersion3
	receipt.State = "unavailable"
	receipt.Reason = protocol.BrowserAuthoredPageUnavailablePopupCommit
	if page == nil || page.MainFrame() == nil {
		return
	}
	commit := playwright.APEXFrameNavigationCommit(page.MainFrame())
	if response == nil && commit.Sequence == 0 {
		deadline := time.Now().Add(500 * time.Millisecond)
		for commit.Sequence == 0 && time.Now().Before(deadline) && ctx.Err() == nil {
			time.Sleep(10 * time.Millisecond)
			commit = playwright.APEXFrameNavigationCommit(page.MainFrame())
		}
	}
	if commit.Failed {
		return
	}
	reqID := ""
	if response != nil && response.Request() != nil {
		reqID = b.requestEvidenceID(response.Request())
		if reqID == "" {
			receipt.Reason = "navigation_join_unavailable"
			return
		}
	} else if commit.Request != nil {
		reqID = b.requestEvidenceID(commit.Request)
	}
	receipt.Reason = "document_lifecycle_unavailable"
	docID, lifeID, generation, navSeq, ok := b.resolveNativeFrameDocument(ctx, page.MainFrame(), commit.Sequence, false)
	if !ok {
		return
	}
	receipt.State, receipt.Reason = "bound", ""
	receipt.PageID = protocol.BrowserAuthoredPageID(receipt.AuthoredStep)
	receipt.SourceGeneration, receipt.RequestID = generation, reqID
	receipt.DocumentID, receipt.LifecycleID = docID, lifeID
	receipt.Association = protocol.BrowserAuthoredPageAssociationOpenedByAction
	receipt.NavigationSequence = navSeq
	receipt.RouteSeq = 0
	receipt.ObservedAt = time.Now().UTC()
}

func (b *Browser) bindV3RedirectChainPage(ctx context.Context, request playwright.Request, receipt *protocol.BrowserAuthoredPageBinding) {
	receipt.Version = protocol.BrowserAuthoredPageVersion3
	receipt.State = "unavailable"
	receipt.Reason = protocol.BrowserAuthoredPageUnavailableRedirectChain
	if request == nil || request.Frame() == nil {
		return
	}
	reqID := b.requestEvidenceID(request)
	if reqID == "" {
		receipt.Reason = "navigation_join_unavailable"
		return
	}
	commit := playwright.APEXFrameNavigationCommit(request.Frame())
	if commit.Sequence == 0 || commit.Failed || commit.Request != request {
		return
	}
	receipt.Reason = "document_lifecycle_unavailable"
	docID, lifeID, generation, navSeq, ok := b.resolveNativeFrameDocument(ctx, request.Frame(), commit.Sequence, false)
	if !ok {
		return
	}
	receipt.State, receipt.Reason = "bound", ""
	receipt.PageID = protocol.BrowserAuthoredPageID(receipt.AuthoredStep)
	receipt.SourceGeneration, receipt.RequestID = generation, reqID
	receipt.DocumentID, receipt.LifecycleID = docID, lifeID
	receipt.Association = protocol.BrowserAuthoredPageAssociationRedirectChain
	receipt.NavigationSequence = navSeq
	receipt.RouteSeq = 0
	receipt.ObservedAt = time.Now().UTC()
}

func (b *Browser) discoverUnregisteredContextPages() {
	for _, p := range b.contextPages() {
		if p == nil {
			continue
		}
		b.mu.Lock()
		known := false
		for _, existing := range b.pages {
			if existing == p {
				known = true
				break
			}
		}
		b.mu.Unlock()
		if !known {
			b.registerPage(p, b.autoActivatePopups)
		}
	}
}

func (b *Browser) observeActionPageTransitionV3(ctx context.Context, action *protocol.Action, result *protocol.Result, prePage playwright.Page, preNavSeq uint64) *protocol.BrowserInteractionOutcome {
	if b.authoredPageV3Enabled() && action != nil && (!browserPageNavigation(action.Type) || action.Type == "browser_switch_window") {
		b.discoverUnregisteredContextPages()
	}
	popup := b.takeLastActionPopup()
	if !b.authoredPageV3Enabled() || action == nil || result == nil {
		return nil
	}
	if browserPageNavigation(action.Type) && action.Type != "browser_switch_window" {
		return nil
	}
	curPage := b.currentPage()
	if popup == nil && action.Type == "browser_switch_window" && curPage != nil && curPage != prePage && b.activePageBindingForPage(curPage) == nil {
		popup = curPage
	}
	if popup != nil {
		observedAt := time.Now().UTC()
		receipt := protocol.BrowserAuthoredPageBinding{Version: protocol.BrowserAuthoredPageVersion3, State: "unavailable", Reason: "original_source_unavailable", ObservedAt: observedAt}
		if action.BrowserAuthoredStep.Valid() && action.BrowserAuthoredStep.Version == protocol.BrowserOriginalAuthoredStepVersion {
			receipt.AuthoredStep = action.BrowserAuthoredStep.Clone()
			b.bindV3OpenedByActionPage(ctx, popup, nil, &receipt)
			if receipt.State == "bound" {
				b.rememberActivePageBinding(popup, &receipt)
			}
			if result.ProtocolEvidence == nil {
				result.ProtocolEvidence = map[string]any{}
			}
			result.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey] = receipt.Clone()
		}
		if receipt.State == "bound" {
			return &protocol.BrowserInteractionOutcome{
				Kind:               protocol.InteractionOutcomeKindNavigation,
				Declared:           protocol.BrowserAuthoredPageAssociationOpenedByAction,
				Observed:           protocol.InteractionOutcomeMet,
				ObservedAt:         &receipt.ObservedAt,
				NavigationSequence: receipt.NavigationSequence,
				AuthoredPageID:     receipt.PageID,
				RouteSeq:           receipt.RouteSeq,
			}
		}
		commit := playwright.APEXFrameNavigationCommit(popup.MainFrame())
		if commit.Failed {
			return &protocol.BrowserInteractionOutcome{
				Kind:       protocol.InteractionOutcomeKindNavigation,
				Declared:   protocol.BrowserAuthoredPageAssociationOpenedByAction,
				Observed:   protocol.InteractionOutcomeMissed,
				Reason:     "navigation_failed",
				ObservedAt: &observedAt,
			}
		}
		if commit.Sequence == 0 {
			return &protocol.BrowserInteractionOutcome{
				Kind:     protocol.InteractionOutcomeKindNavigation,
				Declared: protocol.BrowserAuthoredPageAssociationOpenedByAction,
				Observed: protocol.InteractionOutcomeNotReached,
				Reason:   receipt.Reason,
			}
		}
		return &protocol.BrowserInteractionOutcome{
			Kind:               protocol.InteractionOutcomeKindNavigation,
			Declared:           protocol.BrowserAuthoredPageAssociationOpenedByAction,
			Observed:           protocol.InteractionOutcomeMet,
			Reason:             receipt.Reason,
			ObservedAt:         &observedAt,
			NavigationSequence: commit.Sequence,
		}
	}
	if browserPageNavigation(action.Type) || prePage == nil || prePage.MainFrame() == nil {
		return nil
	}
	commit := playwright.APEXFrameNavigationCommit(prePage.MainFrame())
	if commit.Sequence <= preNavSeq {
		return nil
	}
	observedAt := time.Now().UTC()
	if commit.Failed {
		return &protocol.BrowserInteractionOutcome{
			Kind:       protocol.InteractionOutcomeKindNavigation,
			Declared:   "navigation_commit",
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "navigation_failed",
			ObservedAt: &observedAt,
		}
	}
	receipt := protocol.BrowserAuthoredPageBinding{Version: protocol.BrowserAuthoredPageVersion3, State: "unavailable", Reason: "original_source_unavailable", ObservedAt: observedAt}
	if action.BrowserAuthoredStep.Valid() && action.BrowserAuthoredStep.Version == protocol.BrowserOriginalAuthoredStepVersion {
		receipt.AuthoredStep = action.BrowserAuthoredStep.Clone()
		switch {
		case commit.SameDocument:
			b.bindV3SameDocumentRouteWithCommit(ctx, prePage.MainFrame(), commit, &receipt)
		case commit.NewDocument && commit.Request != nil:
			b.bindV3RedirectChainPage(ctx, commit.Request, &receipt)
		default:
			b.bindV3HistoryTraversalCommit(ctx, prePage.MainFrame(), commit, "", &receipt)
		}
		if receipt.State == "bound" {
			b.rememberActivePageBinding(prePage, &receipt)
		}
		if result.ProtocolEvidence == nil {
			result.ProtocolEvidence = map[string]any{}
		}
		result.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey] = receipt.Clone()
		if receipt.State == "bound" {
			return &protocol.BrowserInteractionOutcome{
				Kind:               protocol.InteractionOutcomeKindNavigation,
				Declared:           receipt.Association,
				Observed:           protocol.InteractionOutcomeMet,
				ObservedAt:         &receipt.ObservedAt,
				NavigationSequence: receipt.NavigationSequence,
				AuthoredPageID:     receipt.PageID,
				RouteSeq:           receipt.RouteSeq,
			}
		}
	}
	declared := protocol.BrowserAuthoredPageAssociationRedirectChain
	if commit.SameDocument {
		declared = protocol.BrowserAuthoredPageAssociationSameDocumentRoute
	}
	return &protocol.BrowserInteractionOutcome{
		Kind:               protocol.InteractionOutcomeKindNavigation,
		Declared:           declared,
		Observed:           protocol.InteractionOutcomeMet,
		Reason:             receipt.Reason,
		ObservedAt:         &observedAt,
		NavigationSequence: commit.Sequence,
	}
}
