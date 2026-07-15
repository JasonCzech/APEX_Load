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
	if value, ok := actionParamIntValue(params, key); ok {
		return value
	}
	return defaultValue
}

func actionParamIntValue(params map[string]any, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	value, ok := params[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func actionParamFloatValue(params map[string]any, key string) (float64, bool) {
	if params == nil {
		return 0, false
	}
	value, ok := params[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	default:
		return 0, false
	}
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

func actionParamBool(params map[string]any, key string, defaultValue bool) bool {
	if params == nil {
		return defaultValue
	}
	value, ok := params[key]
	if !ok {
		return defaultValue
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
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

func actionSelectValues(action *protocol.Action) []string {
	if action == nil {
		return nil
	}
	values := make([]string, 0)
	appendValue := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			values = append(values, value)
		}
	}
	appendValue(action.Value)
	if action.Params == nil {
		return values
	}
	for _, key := range []string{"value", "values"} {
		for _, value := range stringSliceParam(action.Params[key]) {
			appendValue(value)
		}
	}
	return values
}

func actionScrollDelta(action *protocol.Action) (int, int) {
	if action == nil {
		return 0, 0
	}
	deltaX := actionParamInt(action.Params, "delta_x", 0)
	deltaY := actionParamInt(action.Params, "delta_y", 0)
	if deltaX == 0 {
		deltaX = actionParamInt(action.Params, "x", 0)
	}
	if deltaY == 0 {
		deltaY = actionParamInt(action.Params, "y", 0)
	}
	return deltaX, deltaY
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
	// The network offset is absolute (total ever appended) so it survives eviction
	// of older entries by the ring/byte-budget cap; networkEntriesSince translates
	// it back to a local index.
	return b.consoleDropped + len(b.consoleEntries), b.networkEntriesDropped + len(b.networkEntries)
}

func (b *Browser) consoleEntriesSince(offset int) []protocol.ConsoleEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	local := offset - b.consoleDropped
	if local <= 0 {
		return cloneConsoleEntries(b.consoleEntries)
	}
	if local >= len(b.consoleEntries) {
		return nil
	}
	return cloneConsoleEntries(b.consoleEntries[local:])
}

func (b *Browser) networkEntriesSince(offset int) []protocol.NetworkEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	// offset is absolute (see eventOffsets); translate to a local index, clamping
	// when the captured start has since been evicted by the cap.
	local := offset - b.networkEntriesDropped
	if local <= 0 {
		return cloneNetworkEntries(b.networkEntries)
	}
	if local >= len(b.networkEntries) {
		return nil
	}
	return cloneNetworkEntries(b.networkEntries[local:])
}

func (b *Browser) waitTimeout(timeoutMs int) time.Duration {
	if timeoutMs <= 0 {
		return 10 * time.Second
	}
	if timeoutMs > int(MaxConfiguredBrowserDelay/time.Millisecond) {
		return MaxConfiguredBrowserDelay
	}
	return time.Duration(timeoutMs) * time.Millisecond
}

func (b *Browser) timeoutMS(ctx context.Context, fallback time.Duration) float64 {
	if fallback <= 0 {
		fallback = 10 * time.Second
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 1
		}
		if remaining < fallback {
			fallback = remaining
		}
	}
	if fallback < time.Millisecond {
		return 1
	}
	return float64(fallback.Milliseconds())
}

func parseWaitDelay(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("wait duration is required")
	}
	value := raw
	if strings.IndexFunc(raw, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		value = raw + "ms"
	}
	delay, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if delay < 0 || delay > MaxConfiguredBrowserDelay {
		return 0, fmt.Errorf("wait duration must be between 0 and %s", MaxConfiguredBrowserDelay)
	}
	return delay, nil
}

func validateBrowserActionTiming(action *protocol.Action) error {
	if action == nil {
		return fmt.Errorf("browser action is required")
	}
	if action.WaitFor != nil {
		if _, err := protocol.BoundedMilliseconds(action.WaitFor.TimeoutMs, 0, MaxConfiguredBrowserDelay); err != nil {
			return fmt.Errorf("browser wait_for.timeout_ms %w", err)
		}
	}
	if action.Sync != nil {
		for _, field := range []struct {
			name  string
			value int
		}{
			{name: "sync.timeout_ms", value: action.Sync.TimeoutMs},
			{name: "sync.stable_ms", value: action.Sync.StableMs},
		} {
			if _, err := protocol.BoundedMilliseconds(field.value, 0, MaxConfiguredBrowserDelay); err != nil {
				return fmt.Errorf("browser %s %w", field.name, err)
			}
		}
	}
	for _, key := range []string{"timeout_ms", "duration_ms", "wait_ms"} {
		if action.Params == nil {
			continue
		}
		value, present := action.Params[key]
		if !present {
			continue
		}
		if _, err := protocol.BoundedMilliseconds(value, 0, MaxConfiguredBrowserDelay); err != nil {
			return fmt.Errorf("browser params.%s %w", key, err)
		}
	}
	return nil
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
