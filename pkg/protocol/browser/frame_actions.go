package browser

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
)

func (b *Browser) clickAction(ctx context.Context, action *protocol.Action, selector string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := b.actionTimeout(action)
		if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
			return err
		}
		return locator.Click(playwright.LocatorClickOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
	}
	return b.click(ctx, action, selector)
}

func (b *Browser) fillAction(ctx context.Context, action *protocol.Action, selector, value string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := b.actionTimeout(action)
		if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
			return err
		}
		return locator.Fill(value, playwright.LocatorFillOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
	}
	return b.fill(ctx, action, selector, value)
}

func (b *Browser) selectAction(ctx context.Context, action *protocol.Action, selector, value string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := b.actionTimeout(action)
		if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
			return err
		}
		_, err := locator.SelectOption(playwright.SelectOptionValues{ValuesOrLabels: playwright.StringSlice(value)}, playwright.LocatorSelectOptionOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
		return err
	}
	return b.selectOption(ctx, action, selector, value)
}

func (b *Browser) dblclickAction(ctx context.Context, action *protocol.Action, selector string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := b.actionTimeout(action)
		if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
			return err
		}
		return locator.Dblclick(playwright.LocatorDblclickOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
	}
	return b.currentPage().Dblclick(selector, playwright.PageDblclickOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
	})
}

func (b *Browser) hoverAction(ctx context.Context, action *protocol.Action, selector string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := b.actionTimeout(action)
		if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
			return err
		}
		return locator.Hover(playwright.LocatorHoverOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
	}
	return b.currentPage().Hover(selector, playwright.PageHoverOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
	})
}

func (b *Browser) dragAndDropAction(ctx context.Context, action *protocol.Action, source, target string) error {
	sourceLocator, sourceFramed := b.actionLocator(action, source)
	targetLocator, targetFramed := b.actionLocator(action, target)
	if sourceFramed || targetFramed {
		if !sourceFramed || !targetFramed {
			return fmt.Errorf("browser_drag_and_drop frame targeting requires source and target in the same frame context")
		}
		timeout := b.actionTimeout(action)
		if err := b.waitLocatorVisible(ctx, sourceLocator, timeout); err != nil {
			return err
		}
		if err := b.waitLocatorVisible(ctx, targetLocator, timeout); err != nil {
			return err
		}
		return sourceLocator.DragTo(targetLocator, playwright.LocatorDragToOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
	}
	return b.currentPage().DragAndDrop(source, target, playwright.PageDragAndDropOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
	})
}

func (b *Browser) setInputFilesAction(ctx context.Context, action *protocol.Action, selector string, files []string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		return locator.SetInputFiles(files, playwright.LocatorSetInputFilesOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
		})
	}
	return b.currentPage().SetInputFiles(selector, files, playwright.PageSetInputFilesOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
	})
}

func (b *Browser) pressKeyAction(ctx context.Context, action *protocol.Action, selector string, key string) error {
	if selector != "" {
		if locator, ok := b.actionLocator(action, selector); ok {
			timeout := b.actionTimeout(action)
			if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
				return err
			}
			return locator.Press(key, playwright.LocatorPressOptions{
				Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
			})
		}
		return b.currentPage().Press(selector, key, playwright.PagePressOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
		})
	}
	return b.currentPage().Keyboard().Press(key)
}

func (b *Browser) waitVisibleForAction(ctx context.Context, action *protocol.Action, selector string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		return b.waitLocatorVisible(ctx, locator, b.actionTimeout(action))
	}
	return b.waitVisibleWithTimeout(ctx, selector, b.actionTimeout(action))
}

func (b *Browser) waitHiddenForAction(ctx context.Context, action *protocol.Action, selector string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := b.actionTimeout(action)
		return locator.WaitFor(playwright.LocatorWaitForOptions{
			State:   playwright.WaitForSelectorStateHidden,
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
	}
	return b.waitHidden(ctx, selector)
}

func (b *Browser) actionLocator(action *protocol.Action, selector string) (playwright.Locator, bool) {
	frameSelector := b.actionFrameSelector(action)
	page := b.currentPage()
	if strings.TrimSpace(frameSelector) == "" || page == nil {
		return nil, false
	}
	return page.FrameLocator(frameSelector).Locator(selector), true
}

func (b *Browser) actionFrameSelector(action *protocol.Action) string {
	if action != nil {
		if selector := firstActionString(action.Params, "frame_selector", "iframe_selector"); selector != "" {
			return selector
		}
		if frame := firstActionString(action.Params, "frame", "iframe", "frame_context"); frame != "" {
			return normalizeFrameSelector(frame)
		}
		if action.Metadata != nil {
			if selector := stringAny(action.Metadata["frame_selector"]); selector != "" {
				return selector
			}
			if frame := firstNonEmptyString(stringAny(action.Metadata["source_frame_context"]), stringAny(action.Metadata["frame_context"])); frame != "" {
				return normalizeFrameSelector(frame)
			}
		}
	}
	return b.currentFrameSelector
}

func normalizeFrameSelector(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "iframe") || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "[") || strings.HasPrefix(lower, "css=") || strings.HasPrefix(lower, "xpath=") || strings.HasPrefix(value, "//") {
		return value
	}
	escaped := cssAttrValue(value)
	return fmt.Sprintf(`iframe[name="%s"], iframe[id="%s"], iframe[title="%s"]`, escaped, escaped, escaped)
}

func cssAttrValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func (b *Browser) waitLocatorVisible(ctx context.Context, locator playwright.Locator, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return locator.WaitFor(playwright.LocatorWaitForOptions{Timeout: playwright.Float(b.timeoutMS(ctx, timeout))})
}

func (b *Browser) actionTimeout(action *protocol.Action) time.Duration {
	if action != nil && action.WaitFor != nil && action.WaitFor.TimeoutMs > 0 {
		return time.Duration(action.WaitFor.TimeoutMs) * time.Millisecond
	}
	if action != nil && action.Params != nil {
		if timeoutMs := actionParamInt(action.Params, "timeout_ms", 0); timeoutMs > 0 {
			return time.Duration(timeoutMs) * time.Millisecond
		}
	}
	return 10 * time.Second
}

func (b *Browser) dispatchSwitchFrame(ctx context.Context, action *protocol.Action) error {
	selector := b.actionSelector(action)
	if selector == "" {
		selector = firstActionString(action.Params, "frame_selector", "iframe_selector")
	}
	if selector == "" {
		selector = normalizeFrameSelector(firstActionString(action.Params, "frame", "iframe", "frame_context"))
	}
	if strings.TrimSpace(selector) == "" {
		b.currentFrameSelector = ""
		return nil
	}
	if page := b.currentPage(); page != nil {
		locator := page.FrameLocator(selector).Locator("body")
		if err := b.waitLocatorVisible(ctx, locator, b.actionTimeout(action)); err != nil {
			return fmt.Errorf("browser_switch_frame: frame %q not found or never became ready: %w", selector, err)
		}
	}
	b.currentFrameSelector = selector
	return nil
}

func (b *Browser) dispatchSwitchWindow(action *protocol.Action) error {
	pages := b.contextPages()
	if len(pages) == 0 {
		return fmt.Errorf("browser_switch_window found no open pages")
	}
	if indexRaw := firstActionString(action.Params, "index", "page_index", "window_index"); indexRaw != "" {
		index, err := strconv.Atoi(indexRaw)
		if err != nil || index < 0 || index >= len(pages) {
			return fmt.Errorf("browser_switch_window index %q is out of range", indexRaw)
		}
		b.registerPage(pages[index], true)
		b.currentFrameSelector = ""
		return nil
	}
	target := strings.TrimSpace(action.Target)
	if target == "" {
		target = firstActionString(action.Params, "url_contains", "title_contains", "name")
	}
	if target == "" {
		b.registerPage(pages[len(pages)-1], true)
		b.currentFrameSelector = ""
		return nil
	}
	for _, page := range pages {
		title := ""
		if pageTitle, err := page.Title(); err == nil {
			title = pageTitle
		}
		if strings.Contains(page.URL(), target) || strings.Contains(title, target) {
			b.registerPage(page, true)
			b.currentFrameSelector = ""
			return nil
		}
	}
	return fmt.Errorf("browser_switch_window could not find page matching %q", target)
}

func (b *Browser) contextPages() []playwright.Page {
	if b.pwContext != nil {
		return b.pwContext.Pages()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]playwright.Page(nil), b.pages...)
}

func firstActionString(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := actionParamString(params, key, ""); value != "" {
			return value
		}
	}
	return ""
}

func stringAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
