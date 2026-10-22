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
