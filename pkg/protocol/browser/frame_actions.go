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
		if err := b.waitLocatorVisible(locator, b.actionTimeout(action)); err != nil {
			return err
		}
		return locator.Click()
	}
	return b.click(ctx, selector)
}

func (b *Browser) fillAction(ctx context.Context, action *protocol.Action, selector, value string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		if err := b.waitLocatorVisible(locator, b.actionTimeout(action)); err != nil {
			return err
		}
		return locator.Fill(value)
	}
	return b.fill(ctx, selector, value)
}

func (b *Browser) selectAction(ctx context.Context, action *protocol.Action, selector, value string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		if err := b.waitLocatorVisible(locator, b.actionTimeout(action)); err != nil {
			return err
		}
		_, err := locator.SelectOption(playwright.SelectOptionValues{ValuesOrLabels: playwright.StringSlice(value)})
		return err
	}
	return b.selectOption(ctx, selector, value)
}

func (b *Browser) dblclickAction(action *protocol.Action, selector string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		if err := b.waitLocatorVisible(locator, b.actionTimeout(action)); err != nil {
			return err
		}
		return locator.Dblclick()
	}
	return b.currentPage().Dblclick(selector)
}

func (b *Browser) hoverAction(action *protocol.Action, selector string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		if err := b.waitLocatorVisible(locator, b.actionTimeout(action)); err != nil {
			return err
		}
		return locator.Hover()
	}
	return b.currentPage().Hover(selector)
}

func (b *Browser) dragAndDropAction(action *protocol.Action, source, target string) error {
	sourceLocator, sourceFramed := b.actionLocator(action, source)
	targetLocator, targetFramed := b.actionLocator(action, target)
	if sourceFramed || targetFramed {
		if !sourceFramed || !targetFramed {
			return fmt.Errorf("browser_drag_and_drop frame targeting requires source and target in the same frame context")
		}
		if err := b.waitLocatorVisible(sourceLocator, b.actionTimeout(action)); err != nil {
			return err
		}
		if err := b.waitLocatorVisible(targetLocator, b.actionTimeout(action)); err != nil {
			return err
		}
		return sourceLocator.DragTo(targetLocator)
	}
	return b.currentPage().DragAndDrop(source, target)
}

func (b *Browser) setInputFilesAction(action *protocol.Action, selector string, files []string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		return locator.SetInputFiles(files)
	}
	return b.currentPage().SetInputFiles(selector, files)
}

func (b *Browser) pressKeyAction(action *protocol.Action, selector string, key string) error {
	if selector != "" {
		if locator, ok := b.actionLocator(action, selector); ok {
			if err := b.waitLocatorVisible(locator, b.actionTimeout(action)); err != nil {
				return err
			}
			return locator.Press(key)
		}
		return b.currentPage().Press(selector, key)
	}
	return b.currentPage().Keyboard().Press(key)
}

func (b *Browser) waitVisibleForAction(ctx context.Context, action *protocol.Action, selector string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		return b.waitLocatorVisible(locator, b.actionTimeout(action))
	}
	return b.waitVisible(ctx, selector)
}

func (b *Browser) waitHiddenForAction(ctx context.Context, action *protocol.Action, selector string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := float64(b.actionTimeout(action).Milliseconds())
		return locator.WaitFor(playwright.LocatorWaitForOptions{
			State:   playwright.WaitForSelectorStateHidden,
			Timeout: playwright.Float(timeout),
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

func (b *Browser) waitLocatorVisible(locator playwright.Locator, timeout time.Duration) error {
	timeoutMs := float64(timeout.Milliseconds())
	return locator.WaitFor(playwright.LocatorWaitForOptions{Timeout: playwright.Float(timeoutMs)})
}

func (b *Browser) actionTimeout(action *protocol.Action) time.Duration {
	if action != nil && action.WaitFor != nil && action.WaitFor.TimeoutMs > 0 {
		return time.Duration(action.WaitFor.TimeoutMs) * time.Millisecond
	}
	return 10 * time.Second
}

func (b *Browser) dispatchSwitchFrame(action *protocol.Action) error {
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
		if err := b.waitLocatorVisible(locator, b.actionTimeout(action)); err != nil {
			return err
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
