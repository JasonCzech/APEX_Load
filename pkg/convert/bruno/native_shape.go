package bruno

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/scrub"
)

func validateNativeLayerShape(file sourceFile, role string) error {
	var document map[string]any
	if err := yaml.Unmarshal(file.Data, &document); err != nil {
		return err
	}
	if document == nil {
		return errorsNewNativeShape("document root must be an object")
	}
	if err := validateNativeInfoShape(document); err != nil {
		return err
	}

	containerName := "request"
	if role == "request" {
		containerName = "http"
	}
	container, err := nativeShapeOptionalMap(document, containerName, containerName)
	if err != nil {
		return err
	}
	if role == "request" && container == nil {
		return errorsNewNativeShape("http must be an object")
	}
	if container == nil {
		container = map[string]any{}
	}

	if err := validateNativeHeaderShape(nativeLookup(container, "headers"), containerName+".headers"); err != nil {
		return err
	}
	for _, field := range []struct {
		parent   map[string]any
		name     string
		location string
	}{
		{document, "vars", "vars"},
		{document, "variables", "variables"},
		{container, "vars", containerName + ".vars"},
		{container, "variables", containerName + ".variables"},
	} {
		if err := validateNativeVariableShape(nativeLookup(field.parent, field.name), field.location); err != nil {
			return err
		}
	}
	if err := validateNativeAuthShape(nativeLookup(container, "auth"), containerName+".auth"); err != nil {
		return err
	}
	for _, field := range []struct {
		parent   map[string]any
		location string
	}{
		{document, "settings"},
		{container, containerName + ".settings"},
	} {
		if raw, present := nativeLookupPresent(field.parent, "settings"); present {
			settings, ok := raw.(map[string]any)
			if !ok {
				return errorsNewNativeShape(field.location + " must be an object")
			}
			if err := validateNativeSettingsShape(settings, field.location); err != nil {
				return err
			}
		}
	}
	for _, field := range []struct {
		parent   map[string]any
		location string
	}{
		{document, "runtime"},
		{container, containerName + ".runtime"},
	} {
		if raw, present := nativeLookupPresent(field.parent, "runtime"); present {
			runtime, ok := raw.(map[string]any)
			if !ok {
				return errorsNewNativeShape(field.location + " must be an object")
			}
			if err := validateNativeHookListShape(nativeLookup(runtime, "scripts"), field.location+".scripts"); err != nil {
				return err
			}
		}
	}
	if err := validateNativeHookListShape(nativeLookup(container, "scripts"), containerName+".scripts"); err != nil {
		return err
	}
	if role == "request" {
		return validateNativeHTTPShape(container)
	}
	return nil
}

func validateNativeInfoShape(document map[string]any) error {
	raw, present := nativeLookupPresent(document, "info")
	if !present {
		return nil
	}
	info, ok := raw.(map[string]any)
	if !ok {
		return errorsNewNativeShape("info must be an object")
	}
	for _, name := range []string{"name", "uid", "type"} {
		if value, exists := nativeLookupPresent(info, name); exists && !nativeStringScalar(value) {
			return errorsNewNativeShape("info." + name + " must be a string")
		}
	}
	if value, exists := nativeLookupPresent(info, "seq"); exists && !nativeIntegerScalar(value) {
		return errorsNewNativeShape("info.seq must be an integer")
	}
	return nil
}

func validateNativeHeaderShape(value any, location string) error {
	if value == nil {
		return nil
	}
	switch headers := value.(type) {
	case []any:
		for index, raw := range headers {
			item, ok := raw.(map[string]any)
			if !ok {
				return errorsNewNativeShape(fmt.Sprintf("%s[%d] must be an object", location, index))
			}
			name, ok := nativeLookup(item, "name").(string)
			if !ok || strings.TrimSpace(name) == "" {
				return errorsNewNativeShape(fmt.Sprintf("%s[%d].name must be a non-empty string", location, index))
			}
			if brunoTemplatePattern.MatchString(name) {
				return errorsNewNativeShape(fmt.Sprintf("%s[%d].name cannot contain templates", location, index))
			}
			if rawValue, present := nativeLookupPresent(item, "value"); present &&
				!nativeScalarValue(rawValue) {
				return errorsNewNativeShape(fmt.Sprintf("%s[%d].value must be a scalar", location, index))
			}
			if rawDisabled, present := nativeLookupPresent(item, "disabled"); present &&
				!nativeBooleanScalar(rawDisabled) {
				return errorsNewNativeShape(fmt.Sprintf("%s[%d].disabled must be a boolean", location, index))
			}
		}
	case map[string]any:
		for name, rawValue := range headers {
			if strings.TrimSpace(name) == "" {
				return errorsNewNativeShape(location + " contains an empty header name")
			}
			if brunoTemplatePattern.MatchString(name) {
				return errorsNewNativeShape(location + " header names cannot contain templates")
			}
			if !nativeScalarValue(rawValue) {
				return errorsNewNativeShape(fmt.Sprintf("%s.%s must be a scalar", location, name))
			}
		}
	default:
		return errorsNewNativeShape(location + " must be an array or object")
	}
	return nil
}

func validateNativeVariableShape(value any, location string) error {
	if value == nil {
		return nil
	}
	switch variables := value.(type) {
	case []any:
		for index, raw := range variables {
			item, ok := raw.(map[string]any)
			if !ok {
				return errorsNewNativeShape(fmt.Sprintf("%s[%d] must be an object", location, index))
			}
			name, ok := nativeLookup(item, "name").(string)
			if !ok || strings.TrimSpace(name) == "" {
				return errorsNewNativeShape(fmt.Sprintf("%s[%d].name must be a non-empty string", location, index))
			}
			if rawValue, present := nativeLookupPresent(item, "value"); present &&
				!nativeScalarValue(rawValue) {
				return errorsNewNativeShape(fmt.Sprintf("%s[%d].value must be a scalar", location, index))
			}
			disabled := false
			if rawDisabled, present := nativeLookupPresent(item, "disabled"); present &&
				!nativeBooleanScalar(rawDisabled) {
				return errorsNewNativeShape(fmt.Sprintf("%s[%d].disabled must be a boolean", location, index))
			} else if present {
				disabled = nativeBool(rawDisabled)
			}
			_ = disabled
		}
	case map[string]any:
		for name, rawValue := range variables {
			if strings.TrimSpace(name) == "" {
				return errorsNewNativeShape(location + " contains an empty variable name")
			}
			if !nativeScalarValue(rawValue) {
				return errorsNewNativeShape(fmt.Sprintf("%s.%s must be a scalar", location, name))
			}
		}
	default:
		return errorsNewNativeShape(location + " must be an array or object")
	}
	return nil
}

func validateNativeAuthShape(value any, location string) error {
	if value == nil {
		return nil
	}
	if _, ok := value.(string); ok {
		return nil
	}
	auth, ok := value.(map[string]any)
	if !ok {
		return errorsNewNativeShape(location + " must be a string or object")
	}
	rawMode, present := nativeLookupPresent(auth, "type")
	if !present {
		return errorsNewNativeShape(location + ".type is required for mapping-form auth")
	}
	mode, ok := rawMode.(string)
	if !ok || strings.TrimSpace(mode) == "" {
		return errorsNewNativeShape(location + ".type must be a non-empty string")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	config := auth
	if nested, present := nativeLookupPresent(auth, mode); present && mode != "" {
		var ok bool
		config, ok = nested.(map[string]any)
		if !ok {
			return errorsNewNativeShape(location + "." + mode + " must be an object")
		}
	}
	_ = config
	return nil
}

func validateNativeSettingsShape(settings map[string]any, location string) error {
	for _, field := range []string{"encodeUrl", "followRedirects"} {
		if raw, present := nativeLookupPresent(settings, field); present && !nativeBooleanScalar(raw) {
			return errorsNewNativeShape(location + "." + field + " must be a boolean")
		}
	}
	for _, field := range []string{"timeout", "maxRedirects"} {
		if raw, present := nativeLookupPresent(settings, field); present && !nativeIntegerScalar(raw) {
			return errorsNewNativeShape(location + "." + field + " must be an integer")
		}
	}
	return nil
}

func validateNativeHookListShape(value any, location string) error {
	if value == nil {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return errorsNewNativeShape(location + " must be an array")
	}
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return errorsNewNativeShape(fmt.Sprintf("%s[%d] must be an object", location, index))
		}
		for _, field := range []string{"type", "code"} {
			if value, present := nativeLookupPresent(item, field); present && !nativeStringScalar(value) {
				return errorsNewNativeShape(fmt.Sprintf("%s[%d].%s must be a string", location, index, field))
			}
		}
	}
	return nil
}

func validateNativeHTTPShape(httpBlock map[string]any) error {
	for _, field := range []string{"method", "url"} {
		if value, present := nativeLookupPresent(httpBlock, field); present && !nativeStringScalar(value) {
			return errorsNewNativeShape("http." + field + " must be a string")
		}
	}
	if rawParams, present := nativeLookupPresent(httpBlock, "params"); present {
		params, ok := rawParams.([]any)
		if !ok {
			return errorsNewNativeShape("http.params must be an array")
		}
		for index, raw := range params {
			item, ok := raw.(map[string]any)
			if !ok {
				return errorsNewNativeShape(fmt.Sprintf("http.params[%d] must be an object", index))
			}
			name, ok := nativeLookup(item, "name").(string)
			if !ok || strings.TrimSpace(name) == "" {
				return errorsNewNativeShape(fmt.Sprintf("http.params[%d].name must be a non-empty string", index))
			}
			if rawType, present := nativeLookupPresent(item, "type"); present && !nativeStringScalar(rawType) {
				return errorsNewNativeShape(fmt.Sprintf("http.params[%d].type must be a string", index))
			}
			disabled := false
			if rawDisabled, present := nativeLookupPresent(item, "disabled"); present &&
				!nativeBooleanScalar(rawDisabled) {
				return errorsNewNativeShape(fmt.Sprintf("http.params[%d].disabled must be a boolean", index))
			} else if present {
				disabled = nativeBool(rawDisabled)
			}
			if rawValue, present := nativeLookupPresent(item, "value"); present {
				if !nativeScalarValue(rawValue) {
					return errorsNewNativeShape(fmt.Sprintf("http.params[%d].value must be a scalar", index))
				}
				value := anyString(rawValue)
				if !disabled && (nativeSensitiveURLValue(name, value) || scrub.URLTokenSensitive(value)) &&
					!nativeStringScalar(rawValue) {
					return errorsNewNativeShape(fmt.Sprintf("http.params[%d].value must be a string for a sensitive URL parameter", index))
				}
			}
		}
	}
	rawBody, present := nativeLookupPresent(httpBlock, "body")
	if !present || rawBody == nil {
		return nil
	}
	if _, ok := rawBody.(string); ok {
		return nil
	}
	body, ok := rawBody.(map[string]any)
	if !ok {
		return errorsNewNativeShape("http.body must be a string or object")
	}
	bodyType := ""
	if rawType, present := nativeLookupPresent(body, "type"); present {
		var ok bool
		bodyType, ok = rawType.(string)
		if !ok {
			return errorsNewNativeShape("http.body.type must be a string")
		}
		bodyType = strings.ToLower(strings.TrimSpace(bodyType))
	}
	if bodyType == "json" {
		if rawData, present := nativeLookupPresent(body, "data"); present {
			if _, encoded := rawData.(string); !encoded {
				if err := validateNativeStructuredJSONCredentials(rawData, nil); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if bodyType != "form-urlencoded" && bodyType != "form_urlencoded" &&
		bodyType != "multipart-form" && bodyType != "multipart_form" {
		return nil
	}
	rawData, present := nativeLookupPresent(body, "data")
	if !present {
		return nil
	}
	items, ok := rawData.([]any)
	if !ok {
		return errorsNewNativeShape("http.body.data must be an array for " + bodyType)
	}
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return errorsNewNativeShape(fmt.Sprintf("http.body.data[%d] must be an object", index))
		}
		name, ok := nativeLookup(item, "name").(string)
		if !ok || strings.TrimSpace(name) == "" {
			return errorsNewNativeShape(fmt.Sprintf("http.body.data[%d].name must be a non-empty string", index))
		}
		disabled := false
		if rawDisabled, present := nativeLookupPresent(item, "disabled"); present &&
			!nativeBooleanScalar(rawDisabled) {
			return errorsNewNativeShape(fmt.Sprintf("http.body.data[%d].disabled must be a boolean", index))
		} else if present {
			disabled = nativeBool(rawDisabled)
		}
		if rawValue, present := nativeLookupPresent(item, "value"); present {
			if !nativeScalarValue(rawValue) {
				return errorsNewNativeShape(fmt.Sprintf("http.body.data[%d].value must be a scalar", index))
			}
			if !disabled && nativeSensitiveBodyFieldName(name) && !nativeStringScalar(rawValue) {
				return errorsNewNativeShape(fmt.Sprintf("http.body.data[%d].value must be a string for sensitive field %q", index, name))
			}
		}
	}
	return nil
}

func validateNativeStructuredJSONCredentials(current any, context []string) error {
	switch typed := current.(type) {
	case map[string]any:
		namedCredential := structuredNamedCredential(typed)
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := typed[key]
			nextContext := append(append([]string(nil), context...), key)
			secretKey := key
			fullContext := append([]string{"http", "body", "data"}, context...)
			if strings.EqualFold(key, "value") &&
				isStructuredCredentialName(namedCredential, fullContext, "request") {
				secretKey = namedCredential
			}
			secretField := isSecretKey(secretKey, fullContext, "request") ||
				nativeJSONCredentialContainerKey(key)
			if strings.EqualFold(key, "value") &&
				nativeCredentialContainerContext(context) {
				secretField = true
			}
			if secretField {
				switch child.(type) {
				case map[string]any, []any:
					if nativeJSONCredentialContainerKey(key) {
						break
					}
					return errorsNewNativeShape(fmt.Sprintf(
						"composite JSON credential field %q cannot be governed without changing its wire shape",
						nativeJSONPointer(nextContext),
					))
				default:
					if child == nil {
						break
					}
					if _, stringValue := child.(string); !stringValue {
						return errorsNewNativeShape(fmt.Sprintf(
							"non-string JSON credential field %q cannot be governed without changing its wire type",
							nativeJSONPointer(nextContext),
						))
					}
				}
			}
			if err := validateNativeStructuredJSONCredentials(child, nextContext); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			nextContext := append(append([]string(nil), context...), strconv.Itoa(index))
			if err := validateNativeStructuredJSONCredentials(child, nextContext); err != nil {
				return err
			}
		}
	}
	return nil
}

func nativeShapeOptionalMap(
	parent map[string]any,
	name, location string,
) (map[string]any, error) {
	raw, present := nativeLookupPresent(parent, name)
	if !present {
		return nil, nil
	}
	value, ok := raw.(map[string]any)
	if !ok {
		return nil, errorsNewNativeShape(location + " must be an object")
	}
	return value, nil
}

func nativeStringScalar(value any) bool {
	_, ok := value.(string)
	return ok
}

func nativeScalarValue(value any) bool {
	switch value.(type) {
	case nil, string, bool, int, int64, uint, uint64, float32, float64:
		return true
	default:
		return false
	}
}

func nativeBooleanScalar(value any) bool {
	switch typed := value.(type) {
	case bool:
		return true
	case string:
		_, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil
	default:
		return false
	}
}

func nativeIntegerScalar(value any) bool {
	switch typed := value.(type) {
	case int:
		return true
	case int64:
		return typed >= math.MinInt && typed <= math.MaxInt
	case uint:
		return uint64(typed) <= uint64(math.MaxInt)
	case uint64:
		return typed <= uint64(math.MaxInt)
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0) &&
			typed >= math.MinInt && typed <= math.MaxInt && math.Trunc(typed) == typed
	case string:
		_, err := strconv.Atoi(typed)
		return err == nil
	default:
		return false
	}
}
