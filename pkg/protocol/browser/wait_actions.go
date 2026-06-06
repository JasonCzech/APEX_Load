package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
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
			return b.waitVisible(waitCtx, waitValue)
		case "hidden":
			if waitValue == "" {
				return fmt.Errorf("browser_wait hidden requires selector or wait_for value")
			}
			return b.waitHidden(waitCtx, waitValue)
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
			return b.pwPage.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateLoad})
		case "networkidle":
			return b.pwPage.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateNetworkidle})
		case "domcontentloaded":
			return b.pwPage.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateDomcontentloaded})
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
		waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return b.waitVisible(waitCtx, selector)
	}
	if action.Target == "" {
		return fmt.Errorf("browser_wait requires wait_for, selector, or target")
	}
	delay, err := parseWaitDelay(action.Target)
	if err != nil {
		return b.waitVisible(ctx, action.Target)
	}
	return b.sleep(ctx, delay)
}
