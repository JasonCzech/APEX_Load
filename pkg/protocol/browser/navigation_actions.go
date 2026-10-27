package browser

import (
	"context"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

func (b *Browser) historyBackAction(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	beforeURL := page.URL()
	beforeSeq := b.recordNavPreSequence(page)
	waitUntil, err := navigationWaitUntil(action)
	if err != nil {
		return err
	}
	response, err := page.GoBack(playwright.PageGoBackOptions{
		Timeout:   playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
		WaitUntil: waitUntil,
	})
	if err != nil {
		return err
	}
	afterURL := page.URL()
	afterCommit := playwright.APEXFrameNavigationCommit(page.MainFrame())
	if response == nil && afterURL == beforeURL && (afterCommit.Sequence <= beforeSeq || afterCommit.Failed) {
		return fmt.Errorf("browser_back did not change page URL; no previous history entry")
	}
	b.currentURL = afterURL
	return b.noteDocumentResponse(ctx, action, result, page, response)
}

func (b *Browser) historyForwardAction(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	beforeURL := page.URL()
	beforeSeq := b.recordNavPreSequence(page)
	waitUntil, err := navigationWaitUntil(action)
	if err != nil {
		return err
	}
	response, err := page.GoForward(playwright.PageGoForwardOptions{
		Timeout:   playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
		WaitUntil: waitUntil,
	})
	if err != nil {
		return err
	}
	afterURL := page.URL()
	afterCommit := playwright.APEXFrameNavigationCommit(page.MainFrame())
	if response == nil && afterURL == beforeURL && (afterCommit.Sequence <= beforeSeq || afterCommit.Failed) {
		return fmt.Errorf("browser_forward did not change page URL; no next history entry")
	}
	b.currentURL = afterURL
	return b.noteDocumentResponse(ctx, action, result, page, response)
}

func (b *Browser) reloadAction(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	b.recordNavPreSequence(page)
	waitUntil, err := navigationWaitUntil(action)
	if err != nil {
		return err
	}
	response, err := page.Reload(playwright.PageReloadOptions{
		Timeout:   playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
		WaitUntil: waitUntil,
	})
	if err != nil {
		return err
	}
	b.currentURL = page.URL()
	return b.noteDocumentResponse(ctx, action, result, page, response)
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
