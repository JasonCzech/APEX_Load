package browser

import (
	"math"
	"net/url"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

func browserRequestFrame(request playwright.Request) (frame playwright.Frame) {
	defer func() {
		if recover() != nil {
			frame = nil
		}
	}()
	return request.Frame()
}
func (b *Browser) beginRequestEvidence(request playwright.Request, expectedGeneration ...string) {
	if request == nil || len(expectedGeneration) > 0 && !b.browserEvidenceGenerationCurrent(expectedGeneration[0]) {
		return
	}
	frame := browserRequestFrame(request)
	navigation := request.IsNavigationRequest()
	previous := request.RedirectedFrom()
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.evidence
	if s.generation == "" || b.closing || len(expectedGeneration) > 0 && expectedGeneration[0] != s.generation {
		return
	}
	if s.requests[request] != nil {
		return
	}
	// Every request of the page counts toward its join ordinal, tracked or not.
	join := b.resourceJoinLocked(request, frame)
	s.issued++
	s.inFlight++
	if len(s.requests) >= browserRequestIdentityLimit {
		s.droppedRequests++
		s.incomplete = true
		return
	}
	evidence := protocol.BrowserResourceEvidence{Version: protocol.BrowserEvidenceVersion, SourceGeneration: s.generation, RequestID: s.nextID("r"), FrameID: b.frameIdentityLocked(frame), InitiatingActionID: s.currentAction, IdentityUnavailableReason: "document_unavailable", TimingSource: "playwright", CacheUnavailableReason: "not_exposed", InitiatorUnavailableReason: "not_exposed", ServiceWorkerUnavailableReason: "no_observation"}
	// Playwright's native request/frame relation cannot prove the initiating
	// document through a concurrent navigation. Preserve that absence instead
	// of joining a request to the frame's current URL or document at completion.
	if evidence.FrameID == "" {
		evidence.IdentityUnavailableReason = "frame_unavailable"
	}
	if navigation {
		evidence.NavigationID = s.nextID("n")
	}
	if prior := s.requests[previous]; prior != nil && prior.evidence.RedirectHop < 64 {
		evidence.RedirectFromRequestID = prior.evidence.RequestID
		evidence.RedirectChainID = prior.evidence.RedirectChainID
		evidence.RedirectHop = prior.evidence.RedirectHop + 1
		if navigation {
			evidence.NavigationID = prior.evidence.NavigationID
		}
	}
	if evidence.RedirectChainID == "" {
		evidence.RedirectChainID = evidence.RequestID
	}
	s.requests[request] = &browserRequestIdentity{evidence: evidence, join: join}
}
func (b *Browser) completeRequestEvidence(request playwright.Request, finished bool, expectedGeneration ...string) (*protocol.BrowserResourceEvidence, resourceJoin) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.evidence
	if s.generation == "" || b.closing || len(expectedGeneration) > 0 && expectedGeneration[0] != s.generation {
		return nil, resourceJoin{}
	}
	identity := s.requests[request]
	if identity != nil && identity.finished {
		return nil, resourceJoin{}
	}
	if s.inFlight > 0 {
		s.inFlight--
	}
	if identity == nil {
		return nil, resourceJoin{}
	}
	identity.finished = true
	v := identity.evidence
	v.ObservationActionID = s.currentAction
	v.ObservedAt = time.Now().UTC()
	v.State = "failed"
	if finished {
		v.State = "finished"
	}
	s.retired = append(s.retired, request)
	// Reserve half the inventory for new requests, retaining bounded native
	// redirect links across completion without retaining an entire long session.
	for len(s.retired) > browserRequestIdentityLimit/2 {
		delete(s.requests, s.retired[0])
		s.retired[0] = nil
		s.retired = s.retired[1:]
	}
	return &v, identity.join
}
func browserTimingNumber(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 86400000 {
		return nil
	}
	return &v
}
func browserResourcePhases(t *playwright.RequestTiming) []protocol.BrowserResourcePhase {
	names := []string{"dns", "connect", "tls", "request_wait", "response"}
	var bounds [5][2]float64
	for i := range bounds {
		bounds[i] = [2]float64{-1, -1}
	}
	if t != nil {
		bounds = [5][2]float64{{t.DomainLookupStart, t.DomainLookupEnd}, {t.ConnectStart, t.ConnectEnd}, {t.SecureConnectionStart, t.ConnectEnd}, {t.RequestStart, t.ResponseStart}, {t.ResponseStart, t.ResponseEnd}}
	}
	phases := make([]protocol.BrowserResourcePhase, 0, 5)
	for i, pair := range bounds {
		start, end := browserTimingNumber(pair[0]), browserTimingNumber(pair[1])
		reason := ""
		if start == nil || end == nil {
			reason = "not_exposed"
		}
		if start != nil && end != nil && *end < *start {
			start, end = nil, nil
			reason = "malformed"
		}
		phases = append(phases, protocol.BrowserResourcePhase{Name: names[i], StartMS: start, EndMS: end, UnavailableReason: reason})
	}
	return phases
}
func browserResourceOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
func populateResourceEvidence(v *protocol.BrowserResourceEvidence, request playwright.Request, response playwright.Response, sizes *playwright.RequestSizesResult) *protocol.BrowserResourceEvidence {
	if v == nil {
		return nil
	}
	v.Origin = browserResourceOrigin(request.URL())
	timing := request.Timing()
	v.Phases = browserResourcePhases(timing)
	if timing != nil && !math.IsNaN(timing.StartTime) && !math.IsInf(timing.StartTime, 0) && timing.StartTime > 0 && timing.StartTime <= 253402300799999 {
		start := timing.StartTime
		v.EpochStartMS = &start
	}
	if sizes != nil {
		a, c, d, e := int64(sizes.RequestHeadersSize), int64(sizes.RequestBodySize), int64(sizes.ResponseHeadersSize), int64(sizes.ResponseBodySize)
		if a >= 0 && c >= 0 && d >= 0 && e >= 0 {
			v.Bytes = protocol.BrowserResourceBytes{RequestHeaders: &a, RequestBody: &c, ResponseHeaders: &d, ResponseEncodedBody: &e}
		} else {
			v.Bytes.UnavailableReason = "malformed"
		}
	} else {
		v.Bytes.UnavailableReason = "not_exposed"
	}
	if response != nil {
		sw := response.FromServiceWorker()
		v.ServiceWorker = &sw
		v.ServiceWorkerUnavailableReason = ""
	}
	return v.Clone()
}
