package browser

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

func (b *Browser) runWait(ctx context.Context, action *protocol.Action) error {
	if action.WaitFor != nil {
		waitValue := action.WaitFor.Value
		if waitValue == "" {
			waitValue = b.actionSelector(action)
		}
		waitCtx, cancel := context.WithTimeout(ctx, b.waitTimeout(action.WaitFor.TimeoutMs))
		defer cancel()

		switch strings.ToLower(action.WaitFor.Type) {
		case "", "selector", "visible", "ready":
			if waitValue == "" {
				return fmt.Errorf("browser_wait requires selector, target, or wait_for value")
			}
			if predicate, ok, err := browserWaitElementCountPredicate(action); err != nil {
				return err
			} else if ok {
				return b.waitElementCountForAction(waitCtx, action, waitValue, predicate)
			}
			return b.waitVisibleForAction(waitCtx, action, waitValue)
		case "hidden":
			if waitValue == "" {
				return fmt.Errorf("browser_wait hidden requires selector or wait_for value")
			}
			if predicate, ok, err := browserWaitElementCountPredicate(action); err != nil {
				return err
			} else if ok {
				return b.waitElementCountForAction(waitCtx, action, waitValue, predicate)
			}
			return b.waitHiddenForAction(waitCtx, action, waitValue)
		case "element_text":
			text, ok := action.Params["text"].(string)
			if waitValue == "" || !ok || text == "" {
				return fmt.Errorf("browser_wait element_text requires a selector and params.text")
			}
			locator, framed := b.actionLocator(action, waitValue)
			if !framed {
				if b.currentPage() == nil {
					return fmt.Errorf("browser page is not initialized")
				}
				locator = b.currentPage().Locator(waitValue)
			}
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				if err := waitCtx.Err(); err != nil {
					return fmt.Errorf("browser_wait stopped waiting for element text: %w", err)
				}
				value, err := locator.TextContent(playwright.LocatorTextContentOptions{Timeout: playwright.Float(b.timeoutMS(waitCtx, 200*time.Millisecond))})
				if err == nil && strings.Contains(value, text) {
					visible, err := locator.IsVisible()
					if err == nil && visible && waitCtx.Err() == nil {
						return nil
					}
				}
				select {
				case <-waitCtx.Done():
					return fmt.Errorf("browser_wait timed out waiting for element text: %w", waitCtx.Err())
				case <-ticker.C:
				}
			}
		case "text":
			if waitValue == "" {
				return fmt.Errorf("browser_wait text requires wait_for value")
			}
			return b.waitForJS(waitCtx, fmt.Sprintf(`document.body && document.body.innerText.includes(%q)`, waitValue))
		case "url_contains":
			if waitValue == "" {
				return fmt.Errorf("browser_wait url_contains requires wait_for value")
			}
			return b.waitForJS(waitCtx, fmt.Sprintf(`window.location.href.includes(%q)`, waitValue))
		case "load":
			return b.currentPage().WaitForLoadState(playwright.PageWaitForLoadStateOptions{
				State:   playwright.LoadStateLoad,
				Timeout: playwright.Float(b.timeoutMS(waitCtx, b.waitTimeout(action.WaitFor.TimeoutMs))),
			})
		case "networkidle":
			return b.currentPage().WaitForLoadState(playwright.PageWaitForLoadStateOptions{
				State:   playwright.LoadStateNetworkidle,
				Timeout: playwright.Float(b.timeoutMS(waitCtx, b.waitTimeout(action.WaitFor.TimeoutMs))),
			})
		case "domcontentloaded":
			return b.currentPage().WaitForLoadState(playwright.PageWaitForLoadStateOptions{
				State:   playwright.LoadStateDomcontentloaded,
				Timeout: playwright.Float(b.timeoutMS(waitCtx, b.waitTimeout(action.WaitFor.TimeoutMs))),
			})
		case "duration", "sleep":
			delay, err := parseWaitDelay(waitValue)
			if err != nil {
				return err
			}
			return b.sleep(waitCtx, delay)
		default:
			if waitValue == "" {
				return fmt.Errorf("browser_wait requires selector, target, or wait_for value")
			}
			return b.waitVisible(waitCtx, waitValue)
		}
	}

	selector := b.actionSelector(action)
	if selector != "" {
		waitCtx, cancel := context.WithTimeout(ctx, b.actionTimeout(action))
		defer cancel()
		return b.waitVisibleForAction(waitCtx, action, selector)
	}
	if action.Target == "" {
		return fmt.Errorf("browser_wait requires wait_for, selector, or target")
	}
	delay, err := parseWaitDelay(action.Target)
	if err != nil {
		waitCtx, cancel := context.WithTimeout(ctx, b.actionTimeout(action))
		defer cancel()
		return b.waitVisibleWithTimeout(waitCtx, action.Target, b.actionTimeout(action))
	}
	return b.sleep(ctx, delay)
}

type browserElementCountPredicate struct {
	Operator string
	Count    int
}

func browserWaitElementCountPredicate(action *protocol.Action) (browserElementCountPredicate, bool, error) {
	if action == nil || action.Params == nil {
		return browserElementCountPredicate{}, false, nil
	}
	count, hasCount := actionParamIntValue(action.Params, "count")
	operator := strings.TrimSpace(actionParamString(action.Params, "operator", ""))
	if !hasCount && operator == "" {
		return browserElementCountPredicate{}, false, nil
	}
	if !hasCount {
		return browserElementCountPredicate{}, false, fmt.Errorf("browser_wait params.count is required when params.operator is set")
	}
	if count < 0 {
		return browserElementCountPredicate{}, false, fmt.Errorf("browser_wait params.count must be >= 0")
	}
	if operator == "" {
		operator = ">="
	}
	switch operator {
	case "=", "==", "===", "!=", "!==", ">", ">=", "<", "<=":
	default:
		return browserElementCountPredicate{}, false, fmt.Errorf("browser_wait params.operator must be one of =, ==, ===, !=, !==, >, >=, <, <=")
	}
	return browserElementCountPredicate{Operator: operator, Count: count}, true, nil
}

func (p browserElementCountPredicate) matches(actual int) bool {
	switch p.Operator {
	case "=", "==", "===":
		return actual == p.Count
	case "!=", "!==":
		return actual != p.Count
	case ">":
		return actual > p.Count
	case ">=":
		return actual >= p.Count
	case "<":
		return actual < p.Count
	case "<=":
		return actual <= p.Count
	default:
		return false
	}
}

func (b *Browser) waitElementCountForAction(ctx context.Context, action *protocol.Action, selector string, predicate browserElementCountPredicate) error {
	page := b.currentPage()
	var locator playwright.Locator
	if framed, ok := b.actionLocator(action, selector); ok {
		locator = framed
	} else {
		if page == nil {
			return fmt.Errorf("browser_wait count predicate failed: no active page")
		}
		locator = page.Locator(selector)
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	lastCount := -1
	for {
		count, err := locator.Count()
		if err != nil {
			return fmt.Errorf("browser_wait count predicate could not count selector %q: %w", selector, err)
		}
		lastCount = count
		if predicate.matches(count) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("browser_wait timed out waiting for selector %q count %s %d (last count %d): %w", selector, predicate.Operator, predicate.Count, lastCount, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitActionUsesActionSelector(action *protocol.Action) bool {
	if action == nil {
		return false
	}
	if action.WaitFor == nil {
		return true
	}
	if strings.TrimSpace(action.WaitFor.Value) != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(action.WaitFor.Type)) {
	case "", "selector", "visible", "ready":
		return true
	default:
		return false
	}
}

func (b *Browser) runPostActionSync(ctx context.Context, action *protocol.Action) error {
	sync := browserSyncPolicy(action)
	if sync == nil {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, b.waitTimeout(sync.TimeoutMs))
	defer cancel()
	syncType := strings.ToLower(strings.TrimSpace(sync.Type))
	if syncType == "" {
		syncType = "network_idle"
	}
	switch syncType {
	case "network_idle", "networkidle":
		return b.currentPage().WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State:   playwright.LoadStateNetworkidle,
			Timeout: playwright.Float(b.timeoutMS(waitCtx, b.waitTimeout(sync.TimeoutMs))),
		})
	case "load":
		return b.currentPage().WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State:   playwright.LoadStateLoad,
			Timeout: playwright.Float(b.timeoutMS(waitCtx, b.waitTimeout(sync.TimeoutMs))),
		})
	case "domcontentloaded":
		return b.currentPage().WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State:   playwright.LoadStateDomcontentloaded,
			Timeout: playwright.Float(b.timeoutMS(waitCtx, b.waitTimeout(sync.TimeoutMs))),
		})
	case "element_visible", "visible":
		value := sync.Value
		if value == "" {
			value = b.actionSelector(action)
		}
		if value == "" {
			return fmt.Errorf("sync element_visible requires value, selector, or target")
		}
		return b.waitVisibleForAction(waitCtx, action, value)
	case "duration", "sleep":
		delay, err := parseWaitDelay(sync.Value)
		if err != nil {
			return err
		}
		return b.sleep(waitCtx, delay)
	case "dom_stable":
		stable := time.Duration(sync.StableMs) * time.Millisecond
		if stable <= 0 {
			stable = 500 * time.Millisecond
		}
		return b.waitForDOMStable(waitCtx, stable)
	default:
		// An unrecognized sync type is a no-op, not a forced network_idle wait:
		// network_idle never resolves on sites with long-poll/streaming/websocket
		// traffic, so falling back to it would newly hang or time out actions that
		// previously proceeded. Skip silently-but-logged instead.
		slog.Warn("unknown browser sync type; skipping (no wait)", "sync_type", sync.Type, "action", action.Name)
		return nil
	}
}

func browserSyncPolicy(action *protocol.Action) *protocol.BrowserSyncPolicy {
	if action == nil {
		return nil
	}
	if action.Sync != nil {
		return action.Sync
	}
	if action.Params == nil {
		return nil
	}
	if value := actionParamString(action.Params, "sync", ""); value != "" {
		return &protocol.BrowserSyncPolicy{Type: value}
	}
	if value := actionParamString(action.Params, "wait_for_after", ""); value != "" {
		return &protocol.BrowserSyncPolicy{Type: value}
	}
	return nil
}

func (b *Browser) waitForDOMStable(ctx context.Context, stableWindow time.Duration) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	lastSignature := ""
	stableSince := time.Now()
	for {
		var signature string
		_ = b.evaluatePageExpression(ctx, `(() => {
			const body = document.body;
			return [
				document.readyState,
				body ? body.innerText.length : 0,
				document.querySelectorAll('*').length,
				location.href,
			].join('|');
		})()`, &signature)
		if signature != "" && signature == lastSignature {
			if time.Since(stableSince) >= stableWindow {
				return nil
			}
		} else {
			lastSignature = signature
			stableSince = time.Now()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
