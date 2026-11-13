package engine

import (
	"errors"
	"reflect"
	"strings"
)

// Unredacted debugger replay shows runtime values as the protocol saw them,
// with one exception: values resolved from the project's secret store. Every
// debugger output (progress events, controller callbacks, the returned report
// and error) passes through maskDebugSecrets, which replaces those exact values
// and the reversible spellings the runtime matcher derives for them. Other raw
// values, including credential-looking text the ordinary scrubbers would
// redact, stay visible.

// maxDebugSecretMaskDepth bounds the walk. Replay evidence has no pointer
// cycles; a value nested deeper than this is dropped rather than published
// unmasked.
const maxDebugSecretMaskDepth = 64

// newDebugSecretMask returns nil when there is nothing to mask.
func newDebugSecretMask(managedSecretValues []string) (*RedactionConfig, error) {
	values := make([]string, 0, len(managedSecretValues))
	for _, value := range managedSecretValues {
		if value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	return WithSecretValueRedactionChecked(nil, values)
}

// maskDebugSecrets returns value with managed secrets replaced. Every pointer,
// slice, map and interface it rewrites is copied, so evidence the engine or an
// earlier consumer still holds is never mutated. Screenshots are dropped: they
// can show a secret as pixels the text matcher cannot find.
func maskDebugSecrets[T any](mask *RedactionConfig, value T) T {
	// A mask with nothing to match (a dynamic-registry layer no protocol has
	// registered into yet) leaves the value, screenshots included, as is.
	if mask == nil || !hasRuntimeSecretReplacers(mask) {
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
		masked := maskDebugSecretText(text, mask)
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
			masked := maskDebugSecretText(text, mask)
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

// maskDebugSecretText replaces secret-store values and the reversible
// spellings the runtime matcher derives for them. A WS-Security
// PasswordDigest is a keyed hash no value matcher can find, so a digest
// computed from a secret-store value is withheld with its nonce first
// (maskPlatformDerivedWSSecurityDigests over the mask's values only, which
// leaves a digest of a script-literal password visible).
func maskDebugSecretText(text string, mask *RedactionConfig) string {
	return scrubRuntimeSecretText(maskPlatformDerivedWSSecurityDigests(text, mask), mask)
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
		if isDebugScreenshotField(field) {
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

// debugMaskedRuntimeCredentialScopes are the platform-managed per-action scopes
// whose values embed a secret-store value in a spelling the bare-value matcher
// cannot derive (Basic pairs, SASL payloads, URL userinfo composites).
// debugMaskedRuntimeCredentialScopePrefixes name the runtime-minted scopes: the
// token an http_oauth2_token action fetched ("oauth:<save_as>"). Both are
// platform secrets the unredacted debugger never shows. Values observed under a
// sensitive name in a response ("extraction:observed") stay visible (10/30
// decision), and script-sourced scopes are never masked here. Secrets a
// protocol mints through RuntimeSecretRegistrar (Kafka OAuth, browser JWTs) live
// in the run's dynamic registry, which the debugger mask carries as a layer
// (attachDebugSecretMaskRegistry).
var debugMaskedRuntimeCredentialScopes = []string{
	runtimeCredentialActionScope,
	runtimeCredentialOAuthURLScope,
}

var debugMaskedRuntimeCredentialScopePrefixes = []string{
	"oauth:",
}

func isDebugMaskedRuntimeCredentialScope(name string) bool {
	for _, masked := range debugMaskedRuntimeCredentialScopes {
		if name == masked {
			return true
		}
	}
	for _, prefix := range debugMaskedRuntimeCredentialScopePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

var errDebugSecretMaskCapacity = errors.New("debugger secret masking capacity exceeded")

// extendDebugSecretMask adds the debug-masked scopes' values to the VUser's
// cumulative debugger mask. The set only grows: the returned report still
// carries events from actions whose scope values have since been replaced.
func (vu *VUser) extendDebugSecretMask(scopes map[string][]string) error {
	if vu == nil || !vu.unredactedDebug {
		return nil
	}
	added := false
	for name, values := range scopes {
		if !isDebugMaskedRuntimeCredentialScope(name) {
			continue
		}
		for _, value := range values {
			if value == "" {
				continue
			}
			if _, exists := vu.debugSecretMaskValues[value]; exists {
				continue
			}
			if vu.debugSecretMaskValues == nil {
				vu.debugSecretMaskValues = make(map[string]struct{})
			}
			vu.debugSecretMaskValues[value] = struct{}{}
			added = true
		}
	}
	if !added {
		return nil
	}
	mask, err := newDebugSecretMask(sortedRuntimeSecretValues(vu.debugSecretMaskValues))
	if err != nil {
		return errDebugSecretMaskCapacity
	}
	vu.debugSecretMask = mask
	vu.attachDebugSecretMaskRegistry()
	return nil
}

// attachDebugSecretMaskRegistry layers the run's dynamic secret registry under
// the debugger mask, so a secret a protocol registers through
// RuntimeSecretRegistrar is masked from the moment it is registered, even when
// the protocol registers it from its own goroutine: the registry publishes an
// immutable overlay the mask reads atomically. It runs on the VUser's own
// goroutine (setup and extendDebugSecretMask).
func (vu *VUser) attachDebugSecretMaskRegistry() {
	if vu == nil || !vu.unredactedDebug {
		return
	}
	reg := vu.RedactionConfig.findDynamicRegistry()
	if reg == nil {
		return
	}
	if vu.debugSecretMask != nil && vu.debugSecretMask.findDynamicRegistry() == reg {
		return
	}
	base := vu.debugSecretMask
	if base == nil {
		base = &RedactionConfig{}
	}
	if attached := attachDynamicSecretRegistry(base, reg); attached != nil && !attached.publicationUnavailable() {
		vu.debugSecretMask = attached
	}
}
