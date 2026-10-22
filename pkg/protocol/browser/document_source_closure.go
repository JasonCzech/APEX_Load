package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
	"sort"
)

// The freshly owned initial about:blank document exists before Runtime.enable
// replays its context. Seed its expectation from the native frame tree and join
// the immutable bootstrap identity through that exact native execution context.
// Already-navigating popups, child trees and ambiguous snapshots remain unknown.
func (b *Browser) seedNativeInitialDocument(cdp playwright.CDPSession, state *cdpEvidencePage, generation string) {
	tree, err := cdp.Send("Page.getFrameTree", map[string]any{})
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != generation {
		return
	}
	treeFields, _ := tree.(map[string]any)
	root, _ := treeFields["frameTree"].(map[string]any)
	frame, _ := root["frame"].(map[string]any)
	id, loader := cdpText(frame, "id"), cdpText(frame, "loaderId")
	children, _ := root["childFrames"].([]any)
	if err != nil || id == "" || loader == "" || cdpText(frame, "url") != "about:blank" || len(children) > 0 || len(state.frames) > 0 {
		state.discoveryIncomplete = true
		return
	}
	state.frames[id] = cdpFrameCommit{loaderID: loader, commit: 1}
	state.expectedLoaders[loader] = id
}
func (b *Browser) joinNativeInitialDocument(cdp playwright.CDPSession, state *cdpEvidencePage, generation string) {
	b.mu.Lock()
	if b.evidence.generation != generation || state.discoveryIncomplete {
		b.mu.Unlock()
		return
	}
	contexts := map[int64]cdpContextLoader{}
	for id, c := range state.contexts {
		contexts[id] = c
	}
	finalizer := b.evidence.finalizer
	b.mu.Unlock()
	name, _ := json.Marshal(finalizer)
	for id, c := range contexts {
		result, err := cdp.Send("Runtime.evaluate", map[string]any{"contextId": id, "expression": "globalThis[" + string(name) + "]?.identity?.()", "returnByValue": true})
		resultFields, _ := result.(map[string]any)
		value, _ := resultFields["result"].(map[string]any)
		identity, _ := value["value"].(map[string]any)
		token := cdpText(identity, "document_token")
		b.mu.Lock()
		if b.evidence.generation == generation {
			if err != nil || !browserDocumentToken(token) || state.frames[c.frameID].commit != c.commit {
				state.discoveryIncomplete = true
			} else if prior := state.loaderTokens[c.loaderID]; prior != "" && prior != token {
				state.discoveryIncomplete = true
			} else {
				state.loaderTokens[c.loaderID] = token
			}
		}
		b.mu.Unlock()
	}
	if len(contexts) == 0 {
		b.mu.Lock()
		if b.evidence.generation == generation {
			state.discoveryIncomplete = true
		}
		b.mu.Unlock()
	}
}

func (b *Browser) nativeSourceClosureLocked() *protocol.BrowserNativeSourceClosure {
	s := &b.evidence
	out := &protocol.BrowserNativeSourceClosure{Method: "unavailable", Documents: []protocol.BrowserNativeDocumentExpectation{}}
	if !b.supportsCDP() {
		return out
	}
	out.Method = "chromium_cdp_commits_v1"
	out.DiscoveryComplete = !b.authoredIdentityReadPending && !s.nativeDiscoveryIncomplete && len(s.nativeSources) > 0 && s.clock.lifetime != nil && s.clock.lifetime.Closed && !s.incomplete && !s.cancelled && !s.clock.loss && s.droppedObservations == 0
	matched := map[string]bool{}
	for state, page := range s.nativeSources {
		if state == nil || state.discoveryIncomplete || state.truncated || !state.bindingReady {
			out.DiscoveryComplete = false
		}
		if state == nil {
			continue
		}
		for loader, frame := range state.expectedLoaders {
			hash := sha256.Sum256([]byte(s.generation + "/" + frame + "/" + loader))
			item := protocol.BrowserNativeDocumentExpectation{NativeID: hex.EncodeToString(hash[:])}
			token := state.loaderTokens[loader]
			matches := 0
			for _, d := range s.documents {
				if token == "" || d.page != page || d.token != token {
					continue
				}
				matches++
				item.DocumentID = d.documentID
				item.LifecycleID = d.lifecycleID
				item.LowerNS = d.creationLowerNS
				// A joined native context close is an independent terminal boundary even
				// when the departing document could not deliver its pagehide receipt.
				item.Terminal = s.clock.lifetime != nil && s.clock.lifetime.Closed
				matched[d.lifecycleID] = true
			}
			if matches != 1 || state.ambiguousLoaders[loader] {
				out.DiscoveryComplete = false
				item.DocumentID = ""
				item.LifecycleID = ""
				item.Terminal = false
			}
			if len(out.Documents) >= protocol.MaxBrowserDocumentObservations {
				out.DiscoveryComplete = false
				continue
			}
			out.Documents = append(out.Documents, item)
		}
	}
	for _, d := range s.documents {
		if !matched[d.lifecycleID] {
			out.DiscoveryComplete = false
		}
	}
	sort.Slice(out.Documents, func(i, j int) bool { return out.Documents[i].NativeID < out.Documents[j].NativeID })
	return out
}

// A navigation command's returned native request identifies the document it
// created. This can tighten its lower bound without trusting a URL or target
// timestamp. Auto navigation, history restoration and ambiguous races retain
// the broader context creation bound.
func (b *Browser) noteCausalDocumentResponse(ctx context.Context, action *protocol.Action, response playwright.Response) {
	if action == nil || response == nil || !b.documentMetricsEnabled {
		return
	}
	switch action.Type {
	case "browser_navigate", "browser_reload", "browser_open_window":
	default:
		return
	}
	request := response.Request()
	if request == nil || !request.IsNavigationRequest() {
		return
	}
	frame := request.Frame()
	before := playwright.APEXFrameNavigationCommit(frame)
	if before.Sequence == 0 || before.Request != request {
		return
	}
	b.mu.Lock()
	generation, finalizer, lower := b.evidence.generation, b.evidence.finalizer, b.evidence.actionCreationLowerNS
	b.mu.Unlock()
	if lower <= 0 {
		return
	}
	identity, err := b.readNativeAuthoredIdentity(ctx, frame, finalizer)
	if err != nil {
		return
	}
	after := playwright.APEXFrameNavigationCommit(frame)
	if before.Sequence != after.Sequence || after.Request != request {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation != generation {
		return
	}
	var match *browserDocumentState
	for _, d := range b.evidence.documents {
		if d.frame == frame && d.token == identity.document && d.lifecycleToken == identity.lifecycle && !d.restored {
			if match != nil {
				return
			}
			match = d
		}
	}
	if match != nil {
		match.creationLowerNS = lower
	}
}

func (b *Browser) markNativeDiscoveryIncomplete(generation string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.evidence.generation == generation {
		b.evidence.nativeDiscoveryIncomplete = true
	}
}
