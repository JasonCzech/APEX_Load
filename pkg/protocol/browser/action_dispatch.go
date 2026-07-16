package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
)

func (b *Browser) dispatchAction(ctx context.Context, action *protocol.Action, result *protocol.Result) (error, bool) {
	switch action.Type {
	case "browser_navigate":
		if action.Target == "" {
			return nil, failBrowserAction(result, "browser_navigate requires target")
		}
		b.resetWSEvents()
		return b.navigate(ctx, action, b.resolveURL(action.Target)), false
	case "browser_back":
		b.resetWSEvents()
		return b.historyBackAction(ctx, action), false
	case "browser_forward":
		b.resetWSEvents()
		return b.historyForwardAction(ctx, action), false
	case "browser_reload":
		b.resetWSEvents()
		return b.reloadAction(ctx, action), false
	case "browser_open_window":
		b.resetWSEvents()
		return b.dispatchOpenWindow(ctx, action), false
	case "browser_wait":
		if selector := b.actionSelector(action); selector != "" && waitActionUsesActionSelector(action) {
			return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
				waitAction := *action
				waitAction.Selector = s
				if strings.TrimSpace(waitAction.Target) == strings.TrimSpace(selector) {
					waitAction.Target = s
				}
				return b.runWait(ctx, &waitAction)
			}), false
		}
		return b.runWait(ctx, action), false
	case "browser_wait_response":
		return b.dispatchWaitResponse(ctx, action, result)
	case "browser_set_geolocation":
		return b.dispatchSetGeolocation(action, result)
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
	case "browser_download":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_download requires selector or target")
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.downloadAction(ctx, action, s, result)
		}), false
	case "browser_check":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_check requires selector or target")
		}
		checked := actionCheckedState(action)
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.checkAction(ctx, action, s, checked)
		}), false
	case "browser_tap":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_tap requires selector or target")
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.dispatchTap(ctx, action, s)
		}), false
	case "browser_long_press":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_long_press requires selector or target")
		}
		durationMS := positiveActionParamInt(action.Params, "duration_ms", 500)
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.dispatchLongPress(ctx, action, s, time.Duration(durationMS)*time.Millisecond)
		}), false
	case "browser_swipe":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_swipe requires selector or target")
		}
		direction := actionParamString(action.Params, "direction", "left")
		distance := positiveActionParamInt(action.Params, "distance", 300)
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.dispatchSwipe(ctx, action, s, direction, distance)
		}), false
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
		values := actionSelectValues(action)
		if len(values) == 0 {
			return nil, failBrowserAction(result, "browser_select requires value")
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.selectAction(ctx, action, s, values)
		}), false
	case "browser_scroll":
		deltaX, deltaY := actionScrollDelta(action)
		if deltaX == 0 && deltaY == 0 {
			return nil, failBrowserAction(result, "browser_scroll requires delta_x or delta_y")
		}
		selector := b.actionSelector(action)
		if selector == "" {
			return b.scrollAction(ctx, action, "", deltaX, deltaY), false
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.scrollAction(ctx, action, s, deltaX, deltaY)
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
		return b.runWithSelectorSelfHeal(ctx, action, result, source, func(s string) error {
			return b.dragAndDropAction(ctx, action, s, target)
		}), false
	case "browser_set_files":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_set_files requires selector or target")
		}
		files := actionFiles(action)
		if len(files) == 0 {
			return nil, failBrowserAction(result, "browser_set_files requires value or params.files")
		}
		inputFiles, err := loadBrowserInputFiles(files)
		if err != nil {
			return nil, failBrowserAction(result, err.Error())
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.setInputFilesAction(ctx, action, s, inputFiles)
		}), false
	case "browser_press_key":
		key := actionKey(action)
		if key == "" {
			return nil, failBrowserAction(result, "browser_press_key requires value or params.key")
		}
		selector := b.actionSelector(action)
		if selector != "" {
			return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
				return b.pressKeyAction(ctx, action, s, key)
			}), false
		}
		return b.pressKeyAction(ctx, action, "", key), false
	case "browser_evaluate_js":
		return b.dispatchEvaluateJS(ctx, action, result)
	case "browser_switch_frame":
		return b.dispatchSwitchFrame(ctx, action), false
	case "browser_switch_window":
		return b.dispatchSwitchWindow(ctx, action), false
	case "browser_handle_dialog":
		return b.dispatchHandleDialog(action, result)
	case "browser_screenshot":
		// Capture is handled with the rest of the page state below.
		return nil, false
	default:
		return nil, failBrowserAction(result, fmt.Sprintf("unsupported browser action type: %s", action.Type))
	}
}

const maxBrowserUploadFiles = 32

func loadBrowserInputFiles(paths []string) ([]playwright.InputFile, error) {
	if len(paths) > maxBrowserUploadFiles {
		return nil, fmt.Errorf("browser_set_files exceeds %d file limit", maxBrowserUploadFiles)
	}
	files := make([]playwright.InputFile, 0, len(paths))
	remaining := fileasset.MaxBytes
	for i, path := range paths {
		if remaining <= 0 {
			return nil, fmt.Errorf("browser_set_files exceeds %d byte aggregate limit", fileasset.MaxBytes)
		}
		content, err := fileasset.ReadPathLimited(path, remaining)
		if err != nil {
			return nil, fmt.Errorf("browser_set_files file %d: %w", i, err)
		}
		remaining -= int64(len(content))
		files = append(files, playwright.InputFile{
			Name:     filepath.Base(path),
			MimeType: mime.TypeByExtension(filepath.Ext(path)),
			Buffer:   content,
		})
	}
	return files, nil
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
	if err := protocol.ValidateTargetURL(resolvedURL, b.allowedTargetHosts); err != nil {
		return err
	}
	// Guard on hasDownloadFilters() (as the request-policy hook does): without it,
	// allow_list mode with NO configured hosts treats an empty allow-list as
	// "block everything" and rejects every open_window even though no filter is set.
	if b.hasDownloadFilters() && b.shouldBlockFilteredRequest(resolvedURL) {
		return fmt.Errorf("browser_open_window target %q is blocked by the configured URL filter", resolvedURL)
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
		wait:            time.Duration(positiveActionParamInt(action.Params, "wait_ms", 1000)) * time.Millisecond,
		// Strict by default (the dialog is expected on the next action). Scripts
		// that arm the handler ahead of the triggering action pass
		// expect_immediate:false so intermediate actions are not failed.
		expectImmediate: actionParamBool(action.Params, "expect_immediate", true),
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
		var value string
		hasValue := false
		if strResult, ok := jsResult.(string); ok {
			value = strResult
			hasValue = true
		} else if byteResult, err := json.Marshal(jsResult); err == nil {
			value = string(byteResult)
			hasValue = true
		}
		if hasValue {
			if err := protocol.StoreExtraction(result, "result", value); err != nil {
				result.Extractions = nil
				return fmt.Errorf("browser evaluate result rejected: %w", err), false
			}
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

func actionCheckedState(action *protocol.Action) bool {
	if action != nil && action.Params != nil {
		if checked, ok := action.Params["checked"].(bool); ok {
			return checked
		}
	}
	value := ""
	if action != nil {
		value = strings.ToLower(strings.TrimSpace(action.Value))
	}
	switch value {
	case "false", "0", "no", "off", "unchecked", "uncheck":
		return false
	default:
		return true
	}
}

func failBrowserAction(result *protocol.Result, message string) bool {
	result.ErrorMessage = message
	result.Duration = time.Since(result.StartTime)
	return true
}
