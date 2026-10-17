package http

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

type registeredVerification struct {
	check  protocol.Verification
	paused bool
}
type verificationObservation struct {
	mu         sync.Mutex
	checks     []protocol.Verification
	matched    []bool
	seen       []bool
	incomplete bool
}
type verificationContextKey struct{}

func ValidateVerification(v protocol.Verification) error {
	return httpcontract.ValidateVerification(v)
}
func (h *HTTP) ConfigureVerification(v protocol.Verification) error {
	if err := ValidateVerification(v); err != nil {
		return err
	}
	h.verificationMu.Lock()
	defer h.verificationMu.Unlock()
	if h.verifications == nil {
		h.verifications = map[string]registeredVerification{}
	}
	if v.Operation == "register" {
		if len(h.verifications) >= 256 {
			if _, ok := h.verifications[v.ID]; !ok {
				return fmt.Errorf("verification limit exceeded")
			}
		}
		h.verifications[v.ID] = registeredVerification{check: v}
		return nil
	}
	current, ok := h.verifications[v.ID]
	if !ok {
		return fmt.Errorf("verification ID is not registered")
	}
	current.paused = v.Operation == "pause"
	h.verifications[v.ID] = current
	return nil
}
func (h *HTTP) verificationContext(ctx context.Context) (context.Context, *verificationObservation) {
	if ctx.Value(metaRefreshContextKey{}) != nil {
		o, _ := ctx.Value(verificationContextKey{}).(*verificationObservation)
		return ctx, o
	}

	h.verificationMu.RLock()
	defer h.verificationMu.RUnlock()
	o := &verificationObservation{}
	ids := make([]string, 0, len(h.verifications))
	for id := range h.verifications {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		v := h.verifications[id]
		if !v.paused {
			o.checks = append(o.checks, v.check)
		}
	}
	if len(o.checks) == 0 {
		return ctx, nil
	}
	o.matched = make([]bool, len(o.checks))
	o.seen = make([]bool, len(o.checks))
	return context.WithValue(ctx, verificationContextKey{}, o), o
}
func observeVerification(ctx context.Context, headers http.Header, body []byte, resource, complete bool) {
	o, _ := ctx.Value(verificationContextKey{}).(*verificationObservation)
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, c := range o.checks {
		scope := strings.ToLower(c.Scope)
		if (resource && scope == "noresource") || (!resource && scope == "resource") {
			continue
		}
		o.seen[i] = true
		text := string(body)
		if scope == "headers" {
			text = ""
		}
		if scope == "headers" || scope == "all" {
			var b strings.Builder
			_ = headers.Write(&b)
			text = b.String() + "\r\n" + text
		}
		if !complete && scope != "headers" {
			o.incomplete = true
		}
		needle := c.Text
		if c.CaseInsensitive {
			text = strings.ToLower(text)
			needle = strings.ToLower(needle)
		}
		if strings.Contains(text, needle) {
			o.matched[i] = true
		}
	}
}
func (o *verificationObservation) err() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.incomplete {
		return fmt.Errorf("source verification response exceeded inspection bounds")
	}
	for i, c := range o.checks {
		if o.seen[i] && (o.matched[i] == c.FailIfFound) {
			return fmt.Errorf("source verification %q failed", c.ID)
		}
	}
	return nil
}

type verificationReplayBody struct {
	io.Reader
	io.Closer
}

func observeVerificationRedirect(ctx context.Context, resp *http.Response, resource bool) {
	if resp == nil || ctx.Value(verificationContextKey{}) == nil {
		return
	}
	original := resp.Body
	body, err := io.ReadAll(io.LimitReader(original, int64(httpCacheMaxBodyBytes)+1))
	resp.Body = &verificationReplayBody{Reader: io.MultiReader(bytes.NewReader(body), original), Closer: original}
	complete := err == nil && len(body) <= httpCacheMaxBodyBytes
	if complete {
		decoded, _, e := decodeHTTPResponseContent(body, resp.Header, cloneHTTPHeaders(resp.Header), int64(httpCacheMaxBodyBytes))
		if e == nil {
			body = decoded
		} else {
			complete = false
		}
	}
	observeVerification(ctx, resp.Header, body, resource, complete)
}

func (h *HTTP) VerificationSnapshot() []protocol.Verification {
	h.verificationMu.RLock()
	defer h.verificationMu.RUnlock()
	checks := []protocol.Verification{}
	for _, r := range h.verifications {
		checks = append(checks, r.check)
		if r.paused {
			checks = append(checks, protocol.Verification{ID: r.check.ID, Operation: "pause"})
		}
	}
	sort.SliceStable(checks, func(i, j int) bool { return checks[i].ID < checks[j].ID })
	return checks
}
