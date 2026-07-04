package browser

import (
	"context"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
)

func (b *Browser) historyBackAction(ctx context.Context, action *protocol.Action) error {
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	beforeURL := page.URL()
	waitUntil, err := navigationWaitUntil(action)
	if err != nil {
		return err
	}
	if _, err := page.GoBack(playwright.PageGoBackOptions{
		Timeout:   playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
		WaitUntil: waitUntil,
	}); err != nil {
		return err
	}
	afterURL := page.URL()
	if afterURL == beforeURL {
		return fmt.Errorf("browser_back did not change page URL; no previous history entry")
	}
	b.currentURL = afterURL
	return nil
}

func (b *Browser) historyForwardAction(ctx context.Context, action *protocol.Action) error {
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	beforeURL := page.URL()
	waitUntil, err := navigationWaitUntil(action)
	if err != nil {
		return err
	}
	if _, err := page.GoForward(playwright.PageGoForwardOptions{
		Timeout:   playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
		WaitUntil: waitUntil,
	}); err != nil {
		return err
	}
	afterURL := page.URL()
	if afterURL == beforeURL {
		return fmt.Errorf("browser_forward did not change page URL; no next history entry")
	}
	b.currentURL = afterURL
	return nil
}

func (b *Browser) reloadAction(ctx context.Context, action *protocol.Action) error {
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	waitUntil, err := navigationWaitUntil(action)
	if err != nil {
		return err
	}
	if _, err := page.Reload(playwright.PageReloadOptions{
		Timeout:   playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
		WaitUntil: waitUntil,
	}); err != nil {
		return err
	}
	b.currentURL = page.URL()
	return nil
}

func navigationWaitUntil(action *protocol.Action) (*playwright.WaitUntilState, error) {
	waitUntil := ""
	if action != nil {
		waitUntil = strings.ToLower(strings.TrimSpace(actionParamString(action.Params, "wait_until", "")))
	}
	switch waitUntil {
	case "":
		return nil, nil
	case "load":
		return playwright.WaitUntilStateLoad, nil
	case "domcontentloaded":
		return playwright.WaitUntilStateDomcontentloaded, nil
	case "networkidle", "network_idle":
		return playwright.WaitUntilStateNetworkidle, nil
	case "commit":
		return playwright.WaitUntilStateCommit, nil
	default:
		return nil, fmt.Errorf("browser navigation params.wait_until must be load, domcontentloaded, networkidle, or commit, got %q", waitUntil)
	}
}
