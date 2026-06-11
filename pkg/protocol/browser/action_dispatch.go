package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func (b *Browser) dispatchAction(ctx context.Context, action *protocol.Action, result *protocol.Result) (error, bool) {
	switch action.Type {
	case "browser_navigate":
		if action.Target == "" {
			return nil, failBrowserAction(result, "browser_navigate requires target")
		}
		return b.navigate(ctx, b.resolveURL(action.Target)), false
	case "browser_wait":
		return b.runWait(ctx, action), false
	case "browser_click":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_click requires selector or target")
		}
		if b.touchMode {
			return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
				return b.dispatchTap(ctx, s)
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
		return b.dispatchTap(ctx, selector), false
	case "browser_long_press":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_long_press requires selector or target")
		}
		durationMS := positiveActionParamInt(action.Params, "duration_ms", 500)
		return b.dispatchLongPress(ctx, selector, time.Duration(durationMS)*time.Millisecond), false
	case "browser_swipe":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_swipe requires selector or target")
		}
		direction := actionParamString(action.Params, "direction", "left")
		distance := positiveActionParamInt(action.Params, "distance", 300)
		return b.dispatchSwipe(ctx, selector, direction, distance), false
	case "browser_fill":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_fill requires selector or target")
		}
		if action.Value == "" {
			return nil, failBrowserAction(result, "browser_fill requires value")
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
			return b.dblclickAction(action, s)
		}), false
	case "browser_hover":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_hover requires selector or target")
		}
		return b.runWithSelectorSelfHeal(ctx, action, result, selector, func(s string) error {
			return b.hoverAction(action, s)
		}), false
	case "browser_drag_and_drop":
		source := b.actionSelector(action)
		target := actionParamString(action.Params, "target", "")
		if source == "" || target == "" {
			return nil, failBrowserAction(result, "browser_drag_and_drop requires selector/target and target param")
		}
		return b.dragAndDropAction(action, source, target), false
	case "browser_set_files":
		selector := b.actionSelector(action)
		if selector == "" {
			return nil, failBrowserAction(result, "browser_set_files requires selector or target")
		}
		files := actionFiles(action)
		if len(files) == 0 {
			return nil, failBrowserAction(result, "browser_set_files requires value or params.files")
		}
		return b.setInputFilesAction(action, selector, files), false
	case "browser_press_key":
		key := actionKey(action)
		if key == "" {
			return nil, failBrowserAction(result, "browser_press_key requires value or params.key")
		}
		return b.pressKeyAction(action, b.actionSelector(action), key), false
	case "browser_evaluate_js":
		return b.dispatchEvaluateJS(ctx, action, result)
	case "browser_switch_frame":
		return b.dispatchSwitchFrame(action), false
	case "browser_switch_window":
		return b.dispatchSwitchWindow(action), false
	case "browser_screenshot":
		// Capture is handled with the rest of the page state below.
		return nil, false
	default:
		return nil, failBrowserAction(result, fmt.Sprintf("unsupported browser action type: %s", action.Type))
	}
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
