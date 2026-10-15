package engine

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// Before a run starts, each storage_state_pool observer opens its session on
// the session's preflight page. One sent to sign in has a missing or expired
// session: the run fails before ramp-up and names those observers (their
// position and username, never a secret), rather than letting them fail every
// iteration.

const (
	// BrowserSessionPreflightTimeout bounds a generator's whole preflight. A
	// distributed preflight runs while its assignment is staged, so it stays
	// well inside the generators' two-minute stage lifetime: an observer not
	// checked by then fails the preflight.
	BrowserSessionPreflightTimeout = 60 * time.Second
	// maxConcurrentSessionPreflights bounds the browser contexts a preflight
	// opens at once.
	maxConcurrentSessionPreflights = 8
	// sessionPreflightObserverTimeout bounds one observer's preflight.
	sessionPreflightObserverTimeout = 30 * time.Second
	// maxReportedSessionPreflightFailures bounds the observers a failure names.
	maxReportedSessionPreflightFailures = 10
	// maxSessionPreflightCauseBytes bounds the cause quoted per observer: the
	// first line of its error (a Playwright error's call log follows it).
	maxSessionPreflightCauseBytes = 200
)

// HasBrowserSessionPreflight reports whether the script's observers open
// their sessions on a preflight page before the run.
func HasBrowserSessionPreflight(script *dsl.Script) bool {
	if script == nil || script.Config.Browser.Session == nil {
		return false
	}
	session := script.Config.Browser.Session
	return session.Strategy == dsl.BrowserSessionStorageStatePool && session.PreflightURL != ""
}

// PreflightBrowserSessions checks every group's observer sessions on this
// generator once: a local run while it initializes, a distributed one when
// its assignment is staged (a coordinated start's lead is too short to open
// browsers). Later calls return the first result.
func (mge *MultiGroupEngine) PreflightBrowserSessions(ctx context.Context) error {
	if mge == nil {
		return fmt.Errorf("multi-group engine is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	mge.browserSessionPreflightOnce.Do(func() {
		checkCtx, cancel := context.WithTimeout(ctx, BrowserSessionPreflightTimeout)
		defer cancel()
		err := mge.preflightBrowserSessions(checkCtx)
		if ctx.Err() != nil {
			// A stopped run reports its own cancellation, not observers that
			// could not start because of it.
			err = ctx.Err()
		}
		mge.mu.Lock()
		mge.browserSessionPreflightErr = err
		mge.mu.Unlock()
	})
	mge.mu.Lock()
	defer mge.mu.Unlock()
	return mge.browserSessionPreflightErr
}

func (mge *MultiGroupEngine) preflightBrowserSessions(ctx context.Context) error {
	mge.mu.Lock()
	groups := append([]GroupConfig(nil), mge.groups...)
	mge.mu.Unlock()
	for _, group := range groups {
		if err := preflightGroupBrowserSessions(ctx, group); err != nil {
			return err
		}
	}
	return nil
}

func preflightGroupBrowserSessions(ctx context.Context, group GroupConfig) error {
	if group.VUsers <= 0 || !HasBrowserSessionPreflight(group.Script) {
		return nil
	}
	session := group.Script.Config.Browser.Session
	base := browserSessionPreflightConfig(group)
	failures := make([]string, group.VUsers)
	slots := make(chan struct{}, maxConcurrentSessionPreflights)
	var wg sync.WaitGroup
	for local := range group.VUsers {
		binding, err := browserIdentityForOrdinal(group.Script, group.ParameterAllocation.Offset+local)
		if err != nil {
			wg.Wait()
			return fmt.Errorf("group %q browser session preflight: %w", group.Name, err)
		}
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			failures[local] = preflightBrowserSession(ctx, group, base, local, binding, session.PreflightURL)
		}()
	}
	wg.Wait()
	var problems []string
	for _, failure := range failures {
		if failure != "" {
			problems = append(problems, failure)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	if extra := len(problems) - maxReportedSessionPreflightFailures; extra > 0 {
		problems = append(problems[:maxReportedSessionPreflightFailures], fmt.Sprintf("%d more", extra))
	}
	return fmt.Errorf("group %q browser session preflight: %s", group.Name, strings.Join(problems, "; "))
}

// browserSessionPreflightConfig is the group's load-run browser config with
// HAR capture off: a preflight page is not run evidence.
func browserSessionPreflightConfig(group GroupConfig) map[string]any {
	base := buildGroupProtoConfig(group, "load")
	base["browser_har_capture"] = browserprotocol.HARCaptureOff
	delete(base, "browser_har_capture_policy")
	return base
}

// preflightBrowserSession opens one observer's session on the preflight page
// and reports why it cannot run, or "".
func preflightBrowserSession(ctx context.Context, group GroupConfig, base map[string]any, local int, binding *browserIdentityBinding, preflightURL string) string {
	label := fmt.Sprintf("observer %d (%s)", binding.ordinal+1, binding.identity.Username)
	if ctx.Err() != nil {
		return fmt.Sprintf("%s was not checked within the preflight's %d seconds", label, int(BrowserSessionPreflightTimeout/time.Second))
	}
	cause := func(err error) string {
		if err == nil {
			return ""
		}
		text, _, _ := strings.Cut(scrubSensitiveTextWithConfig(err.Error(), group.RedactionConfig), "\n")
		text = strings.TrimSpace(text)
		if text == "" {
			return ""
		}
		if len(text) > maxSessionPreflightCauseBytes {
			text = strings.ToValidUTF8(text[:maxSessionPreflightCauseBytes], "") + "..."
		}
		return " (" + text + ")"
	}
	config, err := withBrowserIdentityConfig(vuserProtoConfig(base, group.VUserIDOffset+local), binding, group.Script)
	if err != nil {
		return scrubSensitiveTextWithConfig(err.Error(), group.RedactionConfig)
	}
	proto, err := protocol.Get(group.Script.Protocol)
	if err != nil {
		return label + " has no browser protocol"
	}
	ctx, cancel := context.WithTimeout(ctx, sessionPreflightObserverTimeout)
	defer cancel()
	defer func() {
		teardownCtx, cancelTeardown := teardownContext()
		defer cancelTeardown()
		_ = callProtocolTeardown(teardownCtx, proto, group.RedactionConfig)
	}()
	if err := callProtocolInit(ctx, proto, config, group.RedactionConfig); err != nil {
		return label + " could not start its browser" + cause(err)
	}
	result, err := proto.Execute(ctx, &protocol.Action{Name: "Session preflight", Type: "browser_navigate", Target: preflightURL})
	if err == nil && result == nil {
		err = fmt.Errorf("no result")
	}
	if err != nil {
		return label + " could not open the preflight page" + cause(err)
	}
	if browserprotocol.SalesforceSignInURL(result.PageURL) {
		host := ""
		if parsed, err := url.Parse(result.PageURL); err == nil {
			host = " at " + parsed.Hostname()
		}
		return label + " was sent to sign in" + host + ": export its storage state again"
	}
	if !result.Success {
		return label + " could not open the preflight page" + cause(fmt.Errorf("%s", result.ErrorMessage))
	}
	return ""
}
