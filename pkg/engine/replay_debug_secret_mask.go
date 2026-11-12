package engine

import (
	"reflect"
	"regexp"
	"strings"
)

// Unredacted debugger replay shows runtime values as the protocol saw them,
// with one exception: values resolved from the project's secret store and
// SOAP WS-Security UsernameToken passwords (including dynamically computed
// PasswordDigest tokens). Every debugger output (progress events, controller
// callbacks, the returned report and error) passes through maskDebugSecrets,
// which replaces those exact values and the reversible spellings the runtime
// matcher derives for them. Other raw values, including credential-looking text
// the ordinary scrubbers would redact, stay visible.

var wssePasswordElementPattern = regexp.MustCompile(`(?is)(<(?:[A-Za-z0-9_.-]+:)?Password(?:\s[^>]*)?>)([^<]*)(</(?:[A-Za-z0-9_.-]+:)?Password\s*>)`)

func scrubWSSEPasswordElements(text string) string {
	if !strings.Contains(text, "Password") && !strings.Contains(text, "password") {
		return text
	}
	return wssePasswordElementPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := wssePasswordElementPattern.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		openTag, inner, closeTag := parts[1], parts[2], parts[3]
		if strings.HasSuffix(strings.TrimSpace(strings.TrimSuffix(openTag, ">")), "/") {
			return match
		}
		lowerOpen := strings.ToLower(openTag)
		isWSSE := strings.HasPrefix(lowerOpen, "<wsse:password") ||
			strings.Contains(lowerOpen, "wssecurity") ||
			strings.Contains(lowerOpen, "usernametoken") ||
			strings.Contains(lowerOpen, "passwordtext") ||
			strings.Contains(lowerOpen, "passworddigest") ||
			strings.Contains(strings.ToLower(text), "usernametoken") ||
			strings.Contains(strings.ToLower(text), "wssecurity")
		if !isWSSE {
			return match
		}
		trimmed := strings.TrimSpace(inner)
		if trimmed == "" || trimmed == "[REDACTED]" {
			return match
		}
		return openTag + "[REDACTED]" + closeTag
	})
}

func scrubDebugSecretText(text string, mask *RedactionConfig) string {
	masked := text
	if mask != nil {
		masked = scrubRuntimeSecretText(masked, mask)
	}
	return scrubWSSEPasswordElements(masked)
}

// maxDebugSecretMaskDepth bounds the walk. Replay evidence has no pointer
// cycles; a value nested deeper than this is dropped rather than published
// unmasked.
const maxDebugSecretMaskDepth = 64

// newDebugSecretMask returns a non-nil mask for debugger replay so WS-Security
// password tokens are always scrubbed even when no secret-store values are referenced.
func newDebugSecretMask(managedSecretValues []string) (*RedactionConfig, error) {
	values := make([]string, 0, len(managedSecretValues))
	for _, value := range managedSecretValues {
		if value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return &RedactionConfig{}, nil
	}
	return WithSecretValueRedactionChecked(nil, values)
}

// maskDebugSecrets returns value with managed secrets replaced. Every pointer,
// slice, map and interface it rewrites is copied, so evidence the engine or an
// earlier consumer still holds is never mutated. Screenshots are dropped when
// managed secrets are active: they can show a secret as pixels the text matcher
// cannot find.
func maskDebugSecrets[T any](mask *RedactionConfig, value T) T {
	if mask == nil {
		return value
	}
	masked, changed := maskDebugSecretValue(mask, reflect.ValueOf(&value).Elem(), 0)
	if !changed {
		return value
	}
	return masked.Interface().(T)
}

func maskDebugSecretValue(mask *RedactionConfig, value reflect.Value, depth int) (reflect.Value, bool) {
	if depth > maxDebugSecretMaskDepth {
		return reflect.Zero(value.Type()), true
	}
	switch value.Kind() {
	case reflect.String:
		text := value.String()
		masked := scrubDebugSecretText(text, mask)
		if masked == text {
			return value, false
		}
		return reflect.ValueOf(masked).Convert(value.Type()), true
	case reflect.Pointer:
		if value.IsNil() {
			return value, false
		}
		elem, changed := maskDebugSecretValue(mask, value.Elem(), depth+1)
		if !changed {
			return value, false
		}
		copied := reflect.New(value.Type().Elem())
		copied.Elem().Set(elem)
		return copied, true
	case reflect.Interface:
		if value.IsNil() {
			return value, false
		}
		elem, changed := maskDebugSecretValue(mask, value.Elem(), depth+1)
		if !changed {
			return value, false
		}
		copied := reflect.New(value.Type()).Elem()
		copied.Set(elem)
		return copied, true
	case reflect.Struct:
		return maskDebugSecretStruct(mask, value, depth)
	case reflect.Slice:
		if value.IsNil() {
			return value, false
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			text := string(value.Bytes())
			masked := scrubDebugSecretText(text, mask)
			if masked == text {
				return value, false
			}
			return reflect.ValueOf([]byte(masked)).Convert(value.Type()), true
		}
		var copied reflect.Value
		for index := 0; index < value.Len(); index++ {
			elem, changed := maskDebugSecretValue(mask, value.Index(index), depth+1)
			if !changed {
				continue
			}
			if !copied.IsValid() {
				copied = reflect.MakeSlice(value.Type(), value.Len(), value.Len())
				reflect.Copy(copied, value)
			}
			copied.Index(index).Set(elem)
		}
		if !copied.IsValid() {
			return value, false
		}
		return copied, true
	case reflect.Array:
		var copied reflect.Value
		for index := 0; index < value.Len(); index++ {
			elem, changed := maskDebugSecretValue(mask, value.Index(index), depth+1)
			if !changed {
				continue
			}
			if !copied.IsValid() {
				copied = reflect.New(value.Type()).Elem()
				copied.Set(value)
			}
			copied.Index(index).Set(elem)
		}
		if !copied.IsValid() {
			return value, false
		}
		return copied, true
	case reflect.Map:
		if value.IsNil() {
			return value, false
		}
		changed := false
		keys := make([]reflect.Value, 0, value.Len())
		values := make([]reflect.Value, 0, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key, keyChanged := maskDebugSecretValue(mask, iter.Key(), depth+1)
			elem, elemChanged := maskDebugSecretValue(mask, iter.Value(), depth+1)
			changed = changed || keyChanged || elemChanged
			keys = append(keys, key)
			values = append(values, elem)
		}
		if !changed {
			return value, false
		}
		copied := reflect.MakeMapWithSize(value.Type(), len(keys))
		for index := range keys {
			copied.SetMapIndex(keys[index], values[index])
		}
		return copied, true
	default:
		return value, false
	}
}

// maskDebugSecretStruct rewrites exported fields only; unexported fields keep
// their engine-owned values in the copy.
func maskDebugSecretStruct(mask *RedactionConfig, value reflect.Value, depth int) (reflect.Value, bool) {
	var copied reflect.Value
	fields := value.Type()
	for index := 0; index < fields.NumField(); index++ {
		field := fields.Field(index)
		if !field.IsExported() {
			continue
		}
		current := value.Field(index)
		var elem reflect.Value
		var changed bool
		if hasRuntimeSecretReplacers(mask) && isDebugScreenshotField(field) {
			elem, changed = reflect.Zero(field.Type), current.Len() > 0
		} else {
			elem, changed = maskDebugSecretValue(mask, current, depth+1)
		}
		if !changed {
			continue
		}
		if !copied.IsValid() {
			copied = reflect.New(value.Type()).Elem()
			copied.Set(value)
		}
		copied.Field(index).Set(elem)
	}
	if !copied.IsValid() {
		return value, false
	}
	return copied, true
}

func isDebugScreenshotField(field reflect.StructField) bool {
	return field.Type.Kind() == reflect.String && (field.Name == "Screenshot" || field.Name == "ScreenshotBase64")
}

func (vu *VUser) maskDebugSecrets(action ReplayDebugAction) ReplayDebugAction {
	if vu == nil {
		return action
	}
	return maskDebugSecrets(vu.debugSecretMask, action)
}
