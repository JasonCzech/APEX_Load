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
		options := locatorClickOptions(action)
		options.Timeout = playwright.Float(b.timeoutMS(ctx, timeout))
		return locator.Click(options)
	}
	return b.click(ctx, action, selector)
}

func (b *Browser) checkAction(ctx context.Context, action *protocol.Action, selector string, checked bool) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := b.actionTimeout(action)
		if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
			return err
		}
		if checked {
			return locator.Check(playwright.LocatorCheckOptions{
				Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
			})
		}
		return locator.Uncheck(playwright.LocatorUncheckOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
	}
	timeout := b.actionTimeout(action)
	if checked {
		return b.currentPage().Check(selector, playwright.PageCheckOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
	}
	return b.currentPage().Uncheck(selector, playwright.PageUncheckOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
	})
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

func (b *Browser) selectAction(ctx context.Context, action *protocol.Action, selector string, values []string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := b.actionTimeout(action)
		if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
			return err
		}
		_, err := locator.SelectOption(playwright.SelectOptionValues{ValuesOrLabels: playwright.StringSlice(values...)}, playwright.LocatorSelectOptionOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
		return err
	}
	return b.selectOption(ctx, action, selector, values)
}

func (b *Browser) scrollAction(ctx context.Context, action *protocol.Action, selector string, deltaX, deltaY int) error {
	arg := map[string]any{"x": deltaX, "y": deltaY}
	if selector == "" {
		page := b.currentPage()
		if page == nil {
			return fmt.Errorf("browser page is not initialized")
		}
		_, err := page.Evaluate(`delta => {
			window.scrollBy(delta.x, delta.y);
			return { x: window.scrollX, y: window.scrollY };
		}`, arg)
		return err
	}
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := b.actionTimeout(action)
		if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
			return err
		}
		_, err := locator.Evaluate(`(el, delta) => {
			el.scrollBy(delta.x, delta.y);
			return { x: el.scrollLeft, y: el.scrollTop };
		}`, arg, playwright.LocatorEvaluateOptions{
			Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
		})
		return err
	}
	timeout := b.actionTimeout(action)
	if err := b.waitVisibleWithTimeout(ctx, selector, timeout); err != nil {
		return err
	}
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	locator := page.Locator(selector)
	_, err := locator.Evaluate(`(el, delta) => {
		el.scrollBy(delta.x, delta.y);
		return { x: el.scrollLeft, y: el.scrollTop };
	}`, arg, playwright.LocatorEvaluateOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
	})
	return err
}

func (b *Browser) dblclickAction(ctx context.Context, action *protocol.Action, selector string) error {
	if locator, ok := b.actionLocator(action, selector); ok {
		timeout := b.actionTimeout(action)
		if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
			return err
		}
		options := locatorDblclickOptions(action)
		options.Timeout = playwright.Float(b.timeoutMS(ctx, timeout))
		return locator.Dblclick(options)
	}
	options := pageDblclickOptions(action)
	options.Timeout = playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action)))
	return b.currentPage().Dblclick(selector, options)
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
	frameSelectors := b.actionFrameSelectors(action)
	page := b.currentPage()
	if len(frameSelectors) == 0 || page == nil {
		return nil, false
	}
	frame := page.FrameLocator(frameSelectors[0])
	for _, frameSelector := range frameSelectors[1:] {
		frame = frame.FrameLocator(frameSelector)
	}
	return frame.Locator(selector), true
}

func (b *Browser) actionFrameSelectors(action *protocol.Action) []string {
	if action != nil {
		for _, key := range []string{"frame_selectors", "iframe_selectors", "frame_path", "iframe_path"} {
			if selectors := frameSelectorPathFromValue(action.Params[key]); len(selectors) > 0 {
				return selectors
			}
		}
		if selector := firstActionString(action.Params, "frame_selector", "iframe_selector"); selector != "" {
			return []string{selector}
		}
		if frame := firstActionString(action.Params, "frame", "iframe", "frame_context"); frame != "" {
			return []string{normalizeFrameSelector(frame)}
		}
		if action.Metadata != nil {
			for _, key := range []string{"frame_selectors", "iframe_selectors", "frame_path", "iframe_path", "source_frame_path"} {
				if selectors := frameSelectorPathFromValue(action.Metadata[key]); len(selectors) > 0 {
					return selectors
				}
			}
			if selector := stringAny(action.Metadata["frame_selector"]); selector != "" {
				return []string{selector}
			}
			if frame := firstNonEmptyString(stringAny(action.Metadata["source_frame_context"]), stringAny(action.Metadata["frame_context"])); frame != "" {
				return []string{normalizeFrameSelector(frame)}
			}
		}
	}
	return frameSelectorPathFromValue(b.currentFrameSelector)
}

func frameSelectorPathFromValue(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return normalizeFrameSelectorPath(typed)
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringAny(item); text != "" {
				items = append(items, text)
			}
		}
		return normalizeFrameSelectorPath(items)
	case string:
		value := strings.TrimSpace(typed)
		if value == "" {
			return nil
		}
		parts := strings.Split(value, ">>")
		return normalizeFrameSelectorPath(parts)
	default:
		if text := stringAny(value); text != "" {
			return normalizeFrameSelectorPath([]string{text})
		}
		return nil
	}
}

func normalizeFrameSelectorPath(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		selector := normalizeFrameSelector(value)
		if selector != "" {
			out = append(out, selector)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

func locatorClickOptions(action *protocol.Action) playwright.LocatorClickOptions {
	options := playwright.LocatorClickOptions{}
	if button := mouseButtonParam(action); button != nil {
		options.Button = button
	}
	options.Modifiers = keyboardModifiersParam(action)
	return options
}

func pageClickOptions(action *protocol.Action) playwright.PageClickOptions {
	options := playwright.PageClickOptions{}
	if button := mouseButtonParam(action); button != nil {
		options.Button = button
	}
	options.Modifiers = keyboardModifiersParam(action)
	return options
}

func locatorDblclickOptions(action *protocol.Action) playwright.LocatorDblclickOptions {
	options := playwright.LocatorDblclickOptions{}
	if button := mouseButtonParam(action); button != nil {
		options.Button = button
	}
	options.Modifiers = keyboardModifiersParam(action)
	return options
}

func pageDblclickOptions(action *protocol.Action) playwright.PageDblclickOptions {
	options := playwright.PageDblclickOptions{}
	if button := mouseButtonParam(action); button != nil {
		options.Button = button
	}
	options.Modifiers = keyboardModifiersParam(action)
	return options
}

func mouseButtonParam(action *protocol.Action) *playwright.MouseButton {
	if action == nil || action.Params == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(actionParamString(action.Params, "button", ""))) {
	case "right", "secondary":
		return playwright.MouseButtonRight
	case "middle", "auxiliary", "aux":
		return playwright.MouseButtonMiddle
	case "left", "primary":
		return playwright.MouseButtonLeft
	default:
		return nil
	}
}

func keyboardModifiersParam(action *protocol.Action) []playwright.KeyboardModifier {
	if action == nil || action.Params == nil {
		return nil
	}
	values := stringSliceParam(action.Params["modifiers"])
	if len(values) == 0 {
		return nil
	}
	out := make([]playwright.KeyboardModifier, 0, len(values))
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "alt":
			out = append(out, *playwright.KeyboardModifierAlt)
		case "control", "ctrl":
			out = append(out, *playwright.KeyboardModifierControl)
		case "controlormeta", "control_or_meta", "cmdorctrl", "commandorcontrol":
			out = append(out, *playwright.KeyboardModifierControlOrMeta)
		case "meta", "cmd", "command":
			out = append(out, *playwright.KeyboardModifierMeta)
		case "shift":
			out = append(out, *playwright.KeyboardModifierShift)
		}
	}
	return out
}

func stringSliceParam(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringAny(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		parts := strings.Split(typed, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		if text := stringAny(value); text != "" {
			return []string{text}
		}
		return nil
	}
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
	selectors := b.actionFrameSelectors(action)
	if len(selectors) == 0 {
		selector := b.actionSelector(action)
		if selector == "" {
			selector = firstActionString(action.Params, "frame_selector", "iframe_selector")
		}
		if selector == "" {
			selector = normalizeFrameSelector(firstActionString(action.Params, "frame", "iframe", "frame_context"))
		}
		selectors = frameSelectorPathFromValue(selector)
	}
	if len(selectors) == 0 {
		b.currentFrameSelector = ""
		return nil
	}
	if page := b.currentPage(); page != nil {
		frame := page.FrameLocator(selectors[0])
		for _, selector := range selectors[1:] {
			frame = frame.FrameLocator(selector)
		}
		locator := frame.Locator("body")
		if err := b.waitLocatorVisible(ctx, locator, b.actionTimeout(action)); err != nil {
			return fmt.Errorf("browser_switch_frame: frame path %q not found or never became ready: %w", strings.Join(selectors, " >> "), err)
		}
	}
	b.currentFrameSelector = strings.Join(selectors, " >> ")
	return nil
}

func (b *Browser) dispatchSwitchWindow(ctx context.Context, action *protocol.Action) error {
	timeout := b.actionTimeout(action)
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	switchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error
	for {
		if err := b.trySwitchWindow(action); err != nil {
			lastErr = err
		} else {
			return nil
		}
		select {
		case <-switchCtx.Done():
			if lastErr != nil {
				return lastErr
			}
			return switchCtx.Err()
		case <-ticker.C:
		}
	}
}

func (b *Browser) trySwitchWindow(action *protocol.Action) error {
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
	if target != "" {
		if index, err := strconv.Atoi(target); err == nil {
			if index < 0 || index >= len(pages) {
				return fmt.Errorf("browser_switch_window target index %q is out of range", target)
			}
			b.registerPage(pages[index], true)
			b.currentFrameSelector = ""
			return nil
		}
	}
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
