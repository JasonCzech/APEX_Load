package browser

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func actionParamInt(params map[string]any, key string, defaultValue int) int {
	if params == nil {
		return defaultValue
	}
	value, ok := params[key]
	if !ok {
		return defaultValue
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return defaultValue
}

func actionParamString(params map[string]any, key, defaultValue string) string {
	if params == nil {
		return defaultValue
	}
	value, ok := params[key]
	if !ok {
		return defaultValue
	}
	if typed, ok := value.(string); ok && strings.TrimSpace(typed) != "" {
		return strings.TrimSpace(typed)
	}
	return defaultValue
}

func actionFiles(action *protocol.Action) []string {
	if action == nil {
		return nil
	}
	files := make([]string, 0)
	appendFile := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			files = append(files, value)
		}
	}
	appendFile(action.Value)
	if action.Params == nil {
		return files
	}
	for _, key := range []string{"file", "path"} {
		if value := actionParamString(action.Params, key, ""); value != "" {
			appendFile(value)
		}
	}
	switch value := action.Params["files"].(type) {
	case string:
		appendFile(value)
	case []string:
		for _, file := range value {
			appendFile(file)
		}
	case []any:
		for _, file := range value {
			if path, ok := file.(string); ok {
				appendFile(path)
			}
		}
	}
	return files
}

func actionKey(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	if key := strings.TrimSpace(action.Value); key != "" {
		return key
	}
	return firstActionString(action.Params, "key", "key_name", "keyName")
}

func (b *Browser) screenshotSelector(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	if action.Selector != "" {
		return action.Selector
	}
	switch action.Type {
	case "browser_click", "browser_fill", "browser_select", "browser_wait", "browser_screenshot":
		return action.Target
	default:
		return ""
	}
}

func (b *Browser) actionSelector(action *protocol.Action) string {
	if action.Selector != "" {
		return action.Selector
	}
	return action.Target
}

func (b *Browser) waitForJS(ctx context.Context, expression string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var ready bool
		if err := b.evaluatePageExpression(ctx, expression, &ready); err == nil && ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (b *Browser) afterActionDelay(ctx context.Context) error {
	if b.slowMo <= 0 {
		return nil
	}
	return b.sleep(ctx, b.slowMo)
}

func (b *Browser) sleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (b *Browser) eventOffsets() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.consoleEntries), len(b.networkEntries)
}

func (b *Browser) consoleEntriesSince(offset int) []protocol.ConsoleEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if offset <= 0 {
		return cloneConsoleEntries(b.consoleEntries)
	}
	if offset >= len(b.consoleEntries) {
		return nil
	}
	return cloneConsoleEntries(b.consoleEntries[offset:])
}

func (b *Browser) networkEntriesSince(offset int) []protocol.NetworkEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if offset <= 0 {
		return cloneNetworkEntries(b.networkEntries)
	}
	if offset >= len(b.networkEntries) {
		return nil
	}
	return cloneNetworkEntries(b.networkEntries[offset:])
}

func (b *Browser) waitTimeout(timeoutMs int) time.Duration {
	if timeoutMs <= 0 {
		return 10 * time.Second
	}
	return time.Duration(timeoutMs) * time.Millisecond
}

func parseWaitDelay(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("wait duration is required")
	}
	if strings.IndexFunc(raw, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		return time.ParseDuration(raw + "ms")
	}
	return time.ParseDuration(raw)
}

func cloneConsoleEntries(entries []protocol.ConsoleEntry) []protocol.ConsoleEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.ConsoleEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneNetworkEntries(entries []protocol.NetworkEntry) []protocol.NetworkEntry {
	return protocol.CloneNetworkEntries(entries)
}

func cloneDOMSummary(summary *protocol.DOMSummary) *protocol.DOMSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	if len(summary.VisibleText) > 0 {
		cloned.VisibleText = append([]string(nil), summary.VisibleText...)
	}
	return &cloned
}
