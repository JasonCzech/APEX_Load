package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
)

func (b *Browser) dispatchAction(ctx context.Context, action *protocol.Action, result *protocol.Result) (error, bool) {
	switch action.Type {
	case "browser_navigate":
		if action.Target == "" {
			return nil, failBrowserAction(result, "browser_navigate requires target")
		}
		return b.navigate(ctx, action, b.resolveURL(action.Target)), false
	case "browser_open_window":
		return b.dispatchOpenWindow(ctx, action), false
	case "browser_wait":
		return b.runWait(ctx, action), false
	case "browser_ws_wait":
		return b.dispatchWSWait(ctx, action, result)
	case "browser_ws_assert":
		return b.dispatchWSAssert(action, result)
	case "browser_click":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_click requires selector or target")
		}
		if b.touchMode {
			return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
				return b.dispatchTap(ctx, action, s)
			}), false
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.clickAction(ctx, action, s)
		}), false
	case "browser_tap":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_tap requires selector or target")
		}
		return b.dispatchTap(ctx, action, selector), false
	case "browser_long_press":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_long_press requires selector or target")
		}
		durationMS := positiveActionParamInt(action.Params, "duration_ms", 500)
		return b.dispatchLongPress(ctx, action, selector, time.Duration(durationMS)*time.Millisecond), false
	case "browser_swipe":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_swipe requires selector or target")
		}
		direction := actionParamString(action.Params, "direction", "left")
		distance := positiveActionParamInt(action.Params, "distance", 300)
		return b.dispatchSwipe(ctx, action, selector, direction, distance), false
	case "browser_fill":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_fill requires selector or target")
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.fillAction(ctx, action, s, action.Value)
		}), false
	case "browser_select":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_select requires selector or target")
		}
		if action.Value == "" {
			return nil, failBrowserAction(result, "browser_select requires value")
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.selectAction(ctx, action, s, action.Value)
		}), false
	case "browser_dblclick":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_dblclick requires selector or target")
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.dblclickAction(ctx, action, s)
		}), false
	case "browser_hover":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_hover requires selector or target")
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.hoverAction(ctx, action, s)
		}), false
	case "browser_drag_and_drop":
		source := b.actionSelector(action)
		target := actionParamString(action.Params, "target", "")
		if source == "" || target == "" {
			return nil, failBrowserAction(result, "browser_drag_and_drop requires selector/target and target param")
		}
		return b.dragAndDropAction(ctx, action, source, target), false
	case "browser_set_files":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_set_files requires selector or target")
		}
		files := actionFiles(action)
		if len(files) == 0 {
			return nil, failBrowserAction(result, "browser_set_files requires value or params.files")
		}
		return b.setInputFilesAction(ctx, action, selector, files), false
	case "browser_press_key":
		key := actionKey(action)
		if key == "" {
			return nil, failBrowserAction(result, "browser_press_key requires value or params.key")
		}
		return b.pressKeyAction(ctx, action, b.actionSelector(action), key), false
	case "browser_evaluate_js":
		return b.dispatchEvaluateJS(ctx, action, result)
	case "browser_switch_frame":
		return b.dispatchSwitchFrame(ctx, action), false
	case "browser_switch_window":
		return b.dispatchSwitchWindow(action), false
	case "browser_handle_dialog":
		return b.dispatchHandleDialog(action, result)
	case "browser_screenshot":
		// Capture is handled with the rest of the page state below.
		return nil, false
	default:
		return nil, failBrowserAction(result, fmt.Sprintf("unsupported browser action type: %s", action.Type))
	}
}

func (b *Browser) dispatchOpenWindow(ctx context.Context, action *protocol.Action) error {
	rawURL := strings.TrimSpace(firstNonEmptyString(action.Target, actionParamString(action.Params, "url", "")))
	if rawURL == "" {
		return fmt.Errorf("browser_open_window requires target or params.url")
	}
	if b.pwContext == nil {
		return fmt.Errorf("browser context is not initialized")
	}
	resolvedURL := b.resolveURL(rawURL)
	if b.downloadFilterMode == "allow_list" && b.shouldBlockFilteredRequest(resolvedURL) {
		return fmt.Errorf("browser_open_window target %q is outside the configured URL allow-list", resolvedURL)
	}
	page, err := b.pwContext.NewPage()
	if err != nil {
		return err
	}
	b.registerPage(page, true)
	b.currentFrameSelector = ""
	timeout := b.actionTimeout(action)
	if _, err := page.Goto(resolvedURL, playwright.PageGotoOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
	}); err != nil {
		return err
	}
	b.currentURL = resolvedURL
	return b.waitVisibleWithTimeout(ctx, "body", timeout)
}

// dispatchHandleDialog arms a one-shot dialog policy consumed by the next
// dialog the page raises (the action that triggers it follows this one).
func (b *Browser) dispatchHandleDialog(action *protocol.Action, result *protocol.Result) (error, bool) {
	policy := strings.ToLower(strings.TrimSpace(actionParamString(action.Params, "action", "accept")))
	if policy != "accept" && policy != "dismiss" {
		return nil, failBrowserAction(result, fmt.Sprintf("browser_handle_dialog params.action must be accept or dismiss, got %q", policy))
	}
	b.dialogMu.Lock()
	// Keep the runtime default strict for hand-authored scripts. Recorder-origin
	// dialog imports set match_mode:"contains" explicitly because browser dialog
	// text often includes dynamic prefixes/suffixes captured at record time.
	b.pendingDialog = &dialogPolicy{
		action:          policy,
		expectedMessage: actionParamString(action.Params, "expected_message", ""),
		expectedType:    actionParamString(action.Params, "dialog_type", ""),
		matchMode:       actionParamString(action.Params, "match_mode", "exact"),
		promptText:      actionParamString(action.Params, "prompt_text", ""),
	}
	b.dialogMu.Unlock()
	return nil, false
}

func (b *Browser) dispatchEvaluateJS(ctx context.Context, action *protocol.Action, result *protocol.Result) (error, bool) {
	script := action.Target
	if script == "" {
		return nil, failBrowserAction(result, "browser_evaluate_js requires script in target")
	}
	var jsResult any
	actionErr := b.evaluatePageExpression(ctx, script, &jsResult)
	if actionErr == nil && jsResult != nil {
		if strResult, ok := jsResult.(string); ok {
			result.Extractions["result"] = strResult
		} else if byteResult, err := json.Marshal(jsResult); err == nil {
			result.Extractions["result"] = string(byteResult)
		}
	}
	return actionErr, false
}

func positiveActionParamInt(params map[string]any, key string, defaultValue int) int {
	value := actionParamInt(params, key, defaultValue)
	if value <= 0 {
		return defaultValue
	}
	return value
}

func failBrowserAction(result *protocol.Result, message string) bool {
	result.ErrorMessage = message
	result.Duration = time.Since(result.StartTime)
	return true
}
