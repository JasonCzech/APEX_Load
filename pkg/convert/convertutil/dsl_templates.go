package convertutil

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var (
	dslTemplateTokenRE = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
	dslFileTemplateRE  = regexp.MustCompile(`\{\{\s*file\.([^{}\s]+)\s*\}\}`)
)

// DeclaredFileDataSources returns the set of file data-source names in script
// that satisfy pkg/dsl file-reference validation.
func DeclaredFileDataSources(script *dsl.Script) map[string]bool {
	declared := map[string]bool{}
	if script == nil {
		return declared
	}
	for _, source := range script.DataSources {
		name := strings.TrimSpace(source.Name)
		if name == "" || !strings.EqualFold(strings.TrimSpace(source.Type), "file") {
			continue
		}
		if strings.TrimSpace(source.InlineData) != "" ||
			strings.TrimSpace(source.Path) != "" ||
			strings.TrimSpace(source.StorageRef) != "" ||
			strings.TrimSpace(source.StagedAsset) != "" {
			declared[name] = true
		}
	}
	return declared
}

// HasUnsupportedRunTemplate reports whether value contains a {{run.*}} token
// other than {{run.id}} (which pkg/dsl rejects everywhere in a Script).
func HasUnsupportedRunTemplate(value string) bool {
	if !strings.Contains(value, "{{") {
		return false
	}
	for _, match := range dslTemplateTokenRE.FindAllStringSubmatch(value, -1) {
		expr := strings.TrimSpace(match[1])
		if strings.HasPrefix(expr, "run.") && expr != "run.id" {
			return true
		}
	}
	return false
}

// HasAnyRunTemplate reports whether value contains any {{run.*}} token,
// including {{run.id}} (which pkg/dsl rejects in script metadata, action names,
// and map keys).
func HasAnyRunTemplate(value string) bool {
	if !strings.Contains(value, "{{") {
		return false
	}
	for _, match := range dslTemplateTokenRE.FindAllStringSubmatch(value, -1) {
		if strings.HasPrefix(strings.TrimSpace(match[1]), "run.") {
			return true
		}
	}
	return false
}

// HasUndeclaredFileTemplateRef reports whether value contains a {{file.NAME}}
// token whose NAME is not present in declaredFiles.
func HasUndeclaredFileTemplateRef(value string, declaredFiles map[string]bool) bool {
	if !strings.Contains(value, "{{") {
		return false
	}
	for _, match := range dslFileTemplateRE.FindAllStringSubmatch(value, -1) {
		if len(match) > 1 {
			ref := strings.TrimSpace(match[1])
			if ref != "" && !declaredFiles[ref] {
				return true
			}
		}
	}
	return false
}

func neutralizeMatchingTemplates(value string, shouldNeutralize func(full, inner string) bool) string {
	if !strings.Contains(value, "{{") {
		return value
	}
	out := value
	for pass := 0; pass < 8; pass++ {
		changed := false
		next := dslTemplateTokenRE.ReplaceAllStringFunc(out, func(segment string) string {
			sub := dslTemplateTokenRE.FindStringSubmatch(segment)
			if len(sub) < 2 || !shouldNeutralize(segment, sub[1]) {
				return segment
			}
			changed = true
			return "{ {" + sub[1] + "} }"
		})
		out = next
		if !changed || !strings.Contains(out, "{{") {
			break
		}
	}
	return out
}

// NeutralizeScriptMetadataString neutralizes {{run.*}} tokens (including
// {{run.id}}) for strings placed in script.Metadata, action.Name, or map keys,
// while leaving {{file.*}} and all literal content untouched.
func NeutralizeScriptMetadataString(value string) string {
	return neutralizeMatchingTemplates(value, func(_, inner string) bool {
		return strings.HasPrefix(strings.TrimSpace(inner), "run.")
	})
}

// NeutralizeActionMetadataString neutralizes unsupported {{run.*}} tokens
// (other than {{run.id}}) for strings placed in action.Metadata values,
// preserving {{run.id}}, {{file.*}}, and literal content verbatim.
func NeutralizeActionMetadataString(value string) string {
	return neutralizeMatchingTemplates(value, func(_, inner string) bool {
		expr := strings.TrimSpace(inner)
		return strings.HasPrefix(expr, "run.") && expr != "run.id"
	})
}

// NeutralizeActionSurfaceString neutralizes unsupported {{run.*}} tokens (other
// than {{run.id}}) and undeclared {{file.*}} tokens on non-executable review
// action fields (Value, Params, Target, Selector, Body, Headers, Auth).
func NeutralizeActionSurfaceString(value string, declaredFiles map[string]bool) string {
	return neutralizeMatchingTemplates(value, func(full, inner string) bool {
		expr := strings.TrimSpace(inner)
		if strings.HasPrefix(expr, "run.") && expr != "run.id" {
			return true
		}
		if m := dslFileTemplateRE.FindStringSubmatch(full); len(m) > 1 {
			ref := strings.TrimSpace(m[1])
			if ref != "" && !declaredFiles[ref] {
				return true
			}
		}
		return false
	})
}

func neutralizeMapKeyForActionSurface(key string, declaredFiles map[string]bool) string {
	return NeutralizeActionSurfaceString(NeutralizeScriptMetadataString(key), declaredFiles)
}

// NeutralizeStringMapForActionSurface sanitizes a map[string]string (such as
// action.Headers, MultipartPart.Headers, or SOAP.Values) while preserving its
// map[string]string Go type.
func NeutralizeStringMapForActionSurface(values map[string]string, declaredFiles map[string]bool) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[neutralizeMapKeyForActionSurface(k, declaredFiles)] = NeutralizeActionSurfaceString(v, declaredFiles)
	}
	return out
}

// NeutralizeActionMetadataValue normalizes arbitrary metadata containers so
// typed maps/slices (map[string]string, []map[string]any) become JSON-shaped
// runtime containers ([]any, map[string]any), structs/typed slices have all
// {{run.*}} tokens neutralized, and unsupported {{run.*}} tokens cannot fail
// dsl.ValidateScriptIssues.
func NeutralizeActionMetadataValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return NeutralizeActionMetadataString(typed)
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			out[i] = NeutralizeActionMetadataString(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = NeutralizeActionMetadataValue(item)
		}
		return out
	case []map[string]any:
		if structHasAnyRunTemplate(typed) {
			out := make([]any, len(typed))
			for i, item := range typed {
				out[i] = NeutralizeActionMetadataValue(item)
			}
			return out
		}
		out := make([]map[string]any, len(typed))
		for i, item := range typed {
			if m, ok := NeutralizeActionMetadataValue(item).(map[string]any); ok {
				out[i] = m
			} else {
				out[i] = item
			}
		}
		return out
	case map[string]string:
		if structHasAnyRunTemplate(typed) {
			out := make(map[string]any, len(typed))
			for k, v := range typed {
				out[NeutralizeScriptMetadataString(k)] = NeutralizeActionMetadataString(v)
			}
			return out
		}
		return typed
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[NeutralizeScriptMetadataString(k)] = NeutralizeActionMetadataValue(v)
		}
		return out
	default:
		return NeutralizeScriptMetadataDeep(value)
	}
}

// NeutralizeActionSurfaceValue normalizes action parameter values so
// undeclared {{file.*}} and invalid/out-of-scope {{run.*}} tokens cannot fail
// dsl.ValidateScriptIssues. Typed map[string]string and []map[string]any
// values inside Params are normalized to map[string]any and []any so valid
// {{run.id}} values remain in runtime-interpolated containers.
func NeutralizeActionSurfaceValue(value any, declaredFiles map[string]bool) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return NeutralizeActionSurfaceString(typed, declaredFiles)
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			out[i] = NeutralizeActionSurfaceString(item, declaredFiles)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = NeutralizeActionSurfaceValue(item, declaredFiles)
		}
		return out
	case []map[string]any:
		if structHasAnyRunTemplate(typed) {
			out := make([]any, len(typed))
			for i, item := range typed {
				out[i] = NeutralizeActionSurfaceValue(item, declaredFiles)
			}
			return out
		}
		out := make([]map[string]any, len(typed))
		for i, item := range typed {
			if m, ok := NeutralizeActionSurfaceValue(item, declaredFiles).(map[string]any); ok {
				out[i] = m
			} else {
				out[i] = item
			}
		}
		return out
	case map[string]string:
		if structHasAnyRunTemplate(typed) {
			out := make(map[string]any, len(typed))
			for k, v := range typed {
				out[neutralizeMapKeyForActionSurface(k, declaredFiles)] = NeutralizeActionSurfaceString(v, declaredFiles)
			}
			return out
		}
		return NeutralizeStringMapForActionSurface(typed, declaredFiles)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			safeKey := neutralizeMapKeyForActionSurface(k, declaredFiles)
			if safeKey == "body_multipart" && v != nil {
				out[safeKey] = sanitizeRawBodyMultipartMap(v, declaredFiles)
				continue
			}
			out[safeKey] = NeutralizeActionSurfaceValue(v, declaredFiles)
		}
		return out
	case dsl.Action:
		cp := typed
		SanitizeActionDSLTemplates(&cp, declaredFiles)
		return cp
	case *dsl.Action:
		if typed == nil {
			return nil
		}
		cp := *typed
		SanitizeActionDSLTemplates(&cp, declaredFiles)
		return &cp
	case []dsl.Action:
		out := make([]dsl.Action, len(typed))
		for i := range typed {
			out[i] = typed[i]
			SanitizeActionDSLTemplates(&out[i], declaredFiles)
		}
		return out
	case []*dsl.Action:
		out := make([]*dsl.Action, len(typed))
		for i, item := range typed {
			if item != nil {
				cp := *item
				SanitizeActionDSLTemplates(&cp, declaredFiles)
				out[i] = &cp
			}
		}
		return out
	case dsl.MultipartBody:
		cp := typed
		sanitizeMultipartBodyDSLTemplates(&cp, declaredFiles)
		return cp
	case *dsl.MultipartBody:
		if typed == nil {
			return nil
		}
		cp := *typed
		sanitizeMultipartBodyDSLTemplates(&cp, declaredFiles)
		return &cp
	case []dsl.MultipartPart:
		mb := dsl.MultipartBody{Parts: append([]dsl.MultipartPart(nil), typed...)}
		sanitizeMultipartBodyDSLTemplates(&mb, declaredFiles)
		return mb.Parts
	case dsl.MultipartPart:
		mb := dsl.MultipartBody{Parts: []dsl.MultipartPart{typed}}
		sanitizeMultipartBodyDSLTemplates(&mb, declaredFiles)
		return mb.Parts[0]
	case dsl.HeaderList:
		out := append(dsl.HeaderList(nil), typed...)
		sanitizeHeaderListDSLTemplates(out, declaredFiles)
		return out
	case []dsl.HeaderEntry:
		out := append([]dsl.HeaderEntry(nil), typed...)
		sanitizeHeaderListDSLTemplates(out, declaredFiles)
		return out
	case dsl.AuthConfig:
		cp := typed
		sanitizeAuthConfigDSLTemplates(&cp, declaredFiles)
		return cp
	case *dsl.AuthConfig:
		if typed == nil {
			return nil
		}
		cp := *typed
		sanitizeAuthConfigDSLTemplates(&cp, declaredFiles)
		return &cp
	default:
		return NeutralizeStructSurfaceDeep(value, declaredFiles)
	}
}

func sanitizeRawBodyMultipartMap(raw any, declaredFiles map[string]bool) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return NeutralizeActionSurfaceValue(raw, declaredFiles)
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		safeKey := neutralizeMapKeyForActionSurface(k, declaredFiles)
		if safeKey == "parts" {
			out[safeKey] = sanitizeRawMultipartPartsSlice(v, declaredFiles)
			continue
		}
		out[safeKey] = NeutralizeActionSurfaceValue(v, declaredFiles)
	}
	return out
}

func sanitizeRawMultipartPartsSlice(raw any, declaredFiles map[string]bool) any {
	switch parts := raw.(type) {
	case []any:
		out := make([]any, len(parts))
		for i, item := range parts {
			out[i] = sanitizeRawMultipartPartMap(item, declaredFiles)
		}
		return out
	case []map[string]any:
		out := make([]any, len(parts))
		for i, item := range parts {
			out[i] = sanitizeRawMultipartPartMap(item, declaredFiles)
		}
		return out
	default:
		return NeutralizeActionSurfaceValue(raw, declaredFiles)
	}
}

func sanitizeRawMultipartPartMap(raw any, declaredFiles map[string]bool) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return NeutralizeActionSurfaceValue(raw, declaredFiles)
	}
	out := make(map[string]any, len(m))
	encoding, _ := m["value_encoding"].(string)
	isBase64 := strings.EqualFold(strings.TrimSpace(encoding), "base64")
	origVal, hasVal := m["value"].(string)
	for k, v := range m {
		safeKey := neutralizeMapKeyForActionSurface(k, declaredFiles)
		if safeKey == "value_encoding" {
			if s, ok := v.(string); ok {
				out[safeKey] = NeutralizeActionSurfaceString(NeutralizeScriptMetadataString(s), declaredFiles)
				continue
			}
		}
		if safeKey == "value" && isBase64 {
			if s, ok := v.(string); ok {
				if strings.Contains(s, "{{") {
					out[safeKey] = neutralizeMatchingTemplates(s, func(_, _ string) bool { return true })
				} else {
					out[safeKey] = NeutralizeActionSurfaceString(NeutralizeScriptMetadataString(s), declaredFiles)
				}
				continue
			}
		}
		out[safeKey] = NeutralizeActionSurfaceValue(v, declaredFiles)
	}
	if hasVal && isBase64 {
		if newVal, _ := out["value"].(string); newVal != origVal {
			out["value_encoding"] = ""
		}
	}
	return out
}

// ActionHasInvalidDSLTemplates reports whether an executable action carries
// unsupported/out-of-scope {{run.*}} tokens or undeclared {{file.*}}
// references on any executable surface inspected by dsl.ValidateScriptIssues.
func ActionHasInvalidDSLTemplates(action dsl.Action, declaredFiles map[string]bool) bool {
	checkStr := func(s string) bool {
		return HasUnsupportedRunTemplate(s) || HasUndeclaredFileTemplateRef(s, declaredFiles)
	}
	checkNoRunOrUndeclaredFile := func(s string) bool {
		return HasAnyRunTemplate(s) || HasUndeclaredFileTemplateRef(s, declaredFiles)
	}
	if checkStr(action.Target) || checkStr(action.Selector) || checkStr(action.Value) || checkStr(action.Body) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") && strings.TrimSpace(action.Body) != "" {
		if checkStr(action.Body) || (strings.Contains(action.Body, "{{") && !dsl.IsTrustedFullBodyBase64Template(strings.TrimSpace(action.Body))) {
			return true
		}
	}
	if action.BodyMultipart != nil {
		for _, part := range action.BodyMultipart.Parts {
			if checkNoRunOrUndeclaredFile(part.ValueEncoding) ||
				checkStr(part.Name) ||
				checkStr(part.Filename) ||
				checkStr(part.ContentType) ||
				checkStr(part.Value) {
				return true
			}
			if strings.EqualFold(strings.TrimSpace(part.ValueEncoding), "base64") && (HasAnyRunTemplate(part.Value) || strings.Contains(part.Value, "{{")) {
				return true
			}
			for hk, hv := range part.Headers {
				if checkNoRunOrUndeclaredFile(hk) || checkStr(hv) {
					return true
				}
			}
		}
	}
	for k, v := range action.Headers {
		if checkNoRunOrUndeclaredFile(k) || checkStr(v) {
			return true
		}
	}
	for _, h := range action.HeaderList {
		if checkNoRunOrUndeclaredFile(h.Name) || checkStr(h.Value) {
			return true
		}
	}
	if action.Auth != nil {
		if checkNoRunOrUndeclaredFile(action.Auth.Type) ||
			checkStr(action.Auth.Username) ||
			checkStr(action.Auth.Password) ||
			checkStr(action.Auth.Token) ||
			checkStr(action.Auth.Domain) {
			return true
		}
		for _, h := range action.Auth.Hosts {
			if checkNoRunOrUndeclaredFile(h) {
				return true
			}
		}
	}
	if action.SOAP != nil {
		if checkStr(action.SOAP.Action) ||
			checkStr(action.SOAP.Operation) ||
			checkStr(action.SOAP.BodyMode) {
			return true
		}
	}
	if action.WaitFor != nil {
		if HasAnyRunTemplate(action.WaitFor.Type) || HasUnsupportedRunTemplate(action.WaitFor.Value) {
			return true
		}
	}
	if action.Sync != nil {
		if HasAnyRunTemplate(action.Sync.Type) || HasUnsupportedRunTemplate(action.Sync.Value) {
			return true
		}
	}
	if action.Attribution != nil {
		if HasUnsupportedRunTemplate(action.Attribution.HierarchyPath) || HasUnsupportedRunTemplate(action.Attribution.PageName) {
			return true
		}
	}
	for _, ex := range action.Extractors {
		if HasUnsupportedRunTemplate(ex.Expression) || (ex.DefaultValue != nil && HasUnsupportedRunTemplate(*ex.DefaultValue)) {
			return true
		}
		exCopy := ex
		exCopy.Expression, exCopy.DefaultValue = "", nil
		if structHasAnyRunTemplate(exCopy) {
			return true
		}
	}
	for _, as := range action.Assertions {
		if HasUnsupportedRunTemplate(as.Value) || anyValueHasInvalidDSLTemplates(as.Expected, declaredFiles) {
			return true
		}
		asCopy := as
		asCopy.Expected, asCopy.Value = nil, ""
		if structHasAnyRunTemplate(asCopy) {
			return true
		}
	}
	isNativeJS := dsl.CanonicalActionType(action.Type) == "native_javascript"
	isHTTPSeq := strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence")
	for k, v := range action.Params {
		if checkNoRunOrUndeclaredFile(k) {
			return true
		}
		if isNativeJS && k == "source" {
			if s, ok := v.(string); ok {
				if checkStr(s) {
					return true
				}
				continue
			}
		}
		if isHTTPSeq && k == "requests" {
			if httpSequenceRequestsHaveInvalidDSLTemplates(v, declaredFiles) {
				return true
			}
			continue
		}
		if anyValueHasInvalidDSLTemplates(v, declaredFiles) {
			return true
		}
	}
	return false
}

func httpSequenceRequestsHaveInvalidDSLTemplates(raw any, declaredFiles map[string]bool) bool {
	checkReqMap := func(m map[string]any) bool {
		for k, v := range m {
			if HasAnyRunTemplate(k) || HasUndeclaredFileTemplateRef(k, declaredFiles) {
				return true
			}
			if k == "metadata" {
				if structHasUnsupportedRunTemplate(v) || reflectHasStringMatching(reflect.ValueOf(v), make(map[metadataReflectVisit]struct{}), 0, func(s string) bool {
					return HasUndeclaredFileTemplateRef(s, declaredFiles)
				}) {
					return true
				}
				continue
			}
			if k == "name" || k == "transaction" || k == "method" || k == "body_encoding" || k == "body_template_encoding" || k == "body_file" || k == "object_ref" {
				if s, ok := v.(string); ok {
					if HasUnsupportedRunTemplate(s) || HasUndeclaredFileTemplateRef(s, declaredFiles) {
						return true
					}
					continue
				}
			}
			if k == "headers" {
				if hm, ok := v.(map[string]string); ok {
					for hk, hv := range hm {
						if HasAnyRunTemplate(hk) || HasUndeclaredFileTemplateRef(hk, declaredFiles) ||
							HasUnsupportedRunTemplate(hv) || HasUndeclaredFileTemplateRef(hv, declaredFiles) {
							return true
						}
					}
					continue
				}
			}
		}
		if payload, err := json.Marshal(m); err == nil {
			var child dsl.Action
			if json.Unmarshal(payload, &child) == nil {
				if ActionHasInvalidDSLTemplates(child, declaredFiles) {
					return true
				}
			}
		}
		for k, v := range m {
			if k == "metadata" || k == "name" || k == "transaction" || k == "method" || k == "body_encoding" || k == "body_template_encoding" || k == "body_file" || k == "object_ref" || k == "type" || k == "pacing_ids" || k == "http_version" || k == "think_time" || k == "capture" || k == "headers" {
				continue
			}
			if anyValueHasInvalidDSLTemplates(v, declaredFiles) {
				return true
			}
		}
		return false
	}
	switch typed := raw.(type) {
	case []dsl.Action:
		for _, item := range typed {
			if ActionHasInvalidDSLTemplates(item, declaredFiles) {
				return true
			}
		}
		return false
	case []*dsl.Action:
		for _, item := range typed {
			if item != nil && ActionHasInvalidDSLTemplates(*item, declaredFiles) {
				return true
			}
		}
		return false
	case []map[string]any:
		for _, item := range typed {
			if checkReqMap(item) {
				return true
			}
		}
		return false
	case []any:
		for _, item := range typed {
			switch child := item.(type) {
			case map[string]any:
				if checkReqMap(child) {
					return true
				}
			case dsl.Action:
				if ActionHasInvalidDSLTemplates(child, declaredFiles) {
					return true
				}
			case *dsl.Action:
				if child != nil && ActionHasInvalidDSLTemplates(*child, declaredFiles) {
					return true
				}
			default:
				if anyValueHasInvalidDSLTemplates(item, declaredFiles) {
					return true
				}
			}
		}
		return false
	default:
		return anyValueHasInvalidDSLTemplates(raw, declaredFiles)
	}
}

func anyValueHasInvalidDSLTemplates(value any, declaredFiles map[string]bool) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return HasUnsupportedRunTemplate(typed) || HasUndeclaredFileTemplateRef(typed, declaredFiles)
	case []string:
		for _, s := range typed {
			if HasUnsupportedRunTemplate(s) || HasUndeclaredFileTemplateRef(s, declaredFiles) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if anyValueHasInvalidDSLTemplates(item, declaredFiles) {
				return true
			}
		}
	case []map[string]any:
		for _, item := range typed {
			if anyValueHasInvalidDSLTemplates(item, declaredFiles) {
				return true
			}
		}
	case map[string]string:
		for k, v := range typed {
			if HasAnyRunTemplate(k) || HasUnsupportedRunTemplate(v) || HasUndeclaredFileTemplateRef(k, declaredFiles) || HasUndeclaredFileTemplateRef(v, declaredFiles) {
				return true
			}
		}
	case map[string]any:
		for k, v := range typed {
			if HasAnyRunTemplate(k) || HasUndeclaredFileTemplateRef(k, declaredFiles) || anyValueHasInvalidDSLTemplates(v, declaredFiles) {
				return true
			}
		}
	case dsl.Action:
		return ActionHasInvalidDSLTemplates(typed, declaredFiles)
	case *dsl.Action:
		return typed != nil && ActionHasInvalidDSLTemplates(*typed, declaredFiles)
	case []dsl.Action:
		for _, item := range typed {
			if ActionHasInvalidDSLTemplates(item, declaredFiles) {
				return true
			}
		}
	case []*dsl.Action:
		for _, item := range typed {
			if item != nil && ActionHasInvalidDSLTemplates(*item, declaredFiles) {
				return true
			}
		}
	case dsl.MultipartBody:
		return ActionHasInvalidDSLTemplates(dsl.Action{BodyMultipart: &typed}, declaredFiles)
	case *dsl.MultipartBody:
		return typed != nil && ActionHasInvalidDSLTemplates(dsl.Action{BodyMultipart: typed}, declaredFiles)
	case []dsl.MultipartPart:
		return ActionHasInvalidDSLTemplates(dsl.Action{BodyMultipart: &dsl.MultipartBody{Parts: typed}}, declaredFiles)
	case dsl.MultipartPart:
		return ActionHasInvalidDSLTemplates(dsl.Action{BodyMultipart: &dsl.MultipartBody{Parts: []dsl.MultipartPart{typed}}}, declaredFiles)
	case dsl.HeaderList:
		return ActionHasInvalidDSLTemplates(dsl.Action{HeaderList: typed}, declaredFiles)
	case []dsl.HeaderEntry:
		return ActionHasInvalidDSLTemplates(dsl.Action{HeaderList: dsl.HeaderList(typed)}, declaredFiles)
	case dsl.AuthConfig:
		return ActionHasInvalidDSLTemplates(dsl.Action{Auth: &typed}, declaredFiles)
	case *dsl.AuthConfig:
		return typed != nil && ActionHasInvalidDSLTemplates(dsl.Action{Auth: typed}, declaredFiles)
	default:
		return structHasAnyRunOrUndeclaredFileTemplate(value, declaredFiles)
	}
	return false
}

func structHasUnsupportedRunTemplate(value any) bool {
	return reflectHasStringMatching(reflect.ValueOf(value), make(map[metadataReflectVisit]struct{}), 0, HasUnsupportedRunTemplate)
}

func structHasAnyRunTemplate(value any) bool {
	return reflectHasStringMatching(reflect.ValueOf(value), make(map[metadataReflectVisit]struct{}), 0, HasAnyRunTemplate)
}

func structHasAnyRunOrUndeclaredFileTemplate(value any, declaredFiles map[string]bool) bool {
	return reflectHasStringMatching(reflect.ValueOf(value), make(map[metadataReflectVisit]struct{}), 0, func(s string) bool {
		return HasAnyRunTemplate(s) || HasUndeclaredFileTemplateRef(s, declaredFiles)
	})
}

func reflectHasStringMatching(
	v reflect.Value,
	seen map[metadataReflectVisit]struct{},
	depth int,
	matches func(string) bool,
) bool {
	if !v.IsValid() || depth > 64 {
		return false
	}
	switch v.Kind() {
	case reflect.String:
		return matches(v.String())
	case reflect.Interface:
		if v.IsNil() {
			return false
		}
		return reflectHasStringMatching(v.Elem(), seen, depth+1, matches)
	case reflect.Pointer:
		if v.IsNil() {
			return false
		}
		visit := metadataReflectVisit{kind: v.Kind(), typeName: v.Type(), address: v.Pointer()}
		if _, ok := seen[visit]; ok {
			return false
		}
		seen[visit] = struct{}{}
		return reflectHasStringMatching(v.Elem(), seen, depth+1, matches)
	case reflect.Slice:
		if v.IsNil() {
			return false
		}
		visit := metadataReflectVisit{kind: v.Kind(), typeName: v.Type(), address: v.Pointer(), length: v.Len()}
		if _, ok := seen[visit]; ok {
			return false
		}
		seen[visit] = struct{}{}
		for i := 0; i < v.Len(); i++ {
			if reflectHasStringMatching(v.Index(i), seen, depth+1, matches) {
				return true
			}
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if reflectHasStringMatching(v.Index(i), seen, depth+1, matches) {
				return true
			}
		}
	case reflect.Map:
		if v.IsNil() {
			return false
		}
		visit := metadataReflectVisit{kind: v.Kind(), typeName: v.Type(), address: v.Pointer()}
		if _, ok := seen[visit]; ok {
			return false
		}
		seen[visit] = struct{}{}
		iter := v.MapRange()
		for iter.Next() {
			if reflectHasStringMatching(iter.Key(), seen, depth+1, matches) ||
				reflectHasStringMatching(iter.Value(), seen, depth+1, matches) {
				return true
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath == "" {
				if reflectHasStringMatching(v.Field(i), seen, depth+1, matches) {
					return true
				}
			}
		}
	}
	return false
}

func sanitizeMultipartBodyDSLTemplates(mb *dsl.MultipartBody, declaredFiles map[string]bool) {
	if mb == nil {
		return
	}
	for i := range mb.Parts {
		part := &mb.Parts[i]
		part.Name = NeutralizeActionSurfaceString(part.Name, declaredFiles)
		part.Filename = NeutralizeActionSurfaceString(part.Filename, declaredFiles)
		part.ContentType = NeutralizeActionSurfaceString(part.ContentType, declaredFiles)
		part.ValueEncoding = NeutralizeActionSurfaceString(NeutralizeScriptMetadataString(part.ValueEncoding), declaredFiles)
		origPartVal := part.Value
		if strings.EqualFold(strings.TrimSpace(part.ValueEncoding), "base64") {
			if strings.Contains(part.Value, "{{") {
				part.Value = neutralizeMatchingTemplates(part.Value, func(_, _ string) bool { return true })
			} else {
				part.Value = NeutralizeActionSurfaceString(NeutralizeScriptMetadataString(part.Value), declaredFiles)
			}
		} else {
			part.Value = NeutralizeActionSurfaceString(part.Value, declaredFiles)
		}
		if part.Value != origPartVal && strings.EqualFold(strings.TrimSpace(part.ValueEncoding), "base64") {
			part.ValueEncoding = ""
		}
		if part.Headers != nil {
			part.Headers = NeutralizeStringMapForActionSurface(part.Headers, declaredFiles)
		}
	}
}

func sanitizeHeaderListDSLTemplates(headers []dsl.HeaderEntry, declaredFiles map[string]bool) {
	for i := range headers {
		headers[i].Name = neutralizeMapKeyForActionSurface(headers[i].Name, declaredFiles)
		headers[i].Value = NeutralizeActionSurfaceString(headers[i].Value, declaredFiles)
	}
}

func sanitizeAuthConfigDSLTemplates(auth *dsl.AuthConfig, declaredFiles map[string]bool) {
	if auth == nil {
		return
	}
	auth.Type = neutralizeMapKeyForActionSurface(auth.Type, declaredFiles)
	auth.Username = NeutralizeActionSurfaceString(auth.Username, declaredFiles)
	auth.Password = NeutralizeActionSurfaceString(auth.Password, declaredFiles)
	auth.Token = NeutralizeActionSurfaceString(auth.Token, declaredFiles)
	auth.Domain = NeutralizeActionSurfaceString(auth.Domain, declaredFiles)
	for i := range auth.Hosts {
		auth.Hosts[i] = neutralizeMapKeyForActionSurface(auth.Hosts[i], declaredFiles)
	}
}

func sanitizeSOAPConfigDSLTemplates(soap *dsl.SOAPActionConfig, declaredFiles map[string]bool) {
	if soap == nil {
		return
	}
	soap.Action = NeutralizeActionSurfaceString(soap.Action, declaredFiles)
	soap.Operation = NeutralizeActionSurfaceString(soap.Operation, declaredFiles)
	soap.BodyMode = NeutralizeActionSurfaceString(soap.BodyMode, declaredFiles)
}

func sanitizeConditionDSLTemplates(cond *dsl.Condition) {
	if cond == nil {
		return
	}
	cond.Expr = NeutralizeScriptMetadataString(cond.Expr)
	cond.Op = NeutralizeScriptMetadataString(cond.Op)
	if strings.TrimSpace(cond.Expr) != "" {
		cond.Left = NeutralizeScriptMetadataString(cond.Left)
		cond.Right = NeutralizeScriptMetadataString(cond.Right)
	} else {
		cond.Left = NeutralizeActionMetadataString(cond.Left)
		cond.Right = NeutralizeActionMetadataString(cond.Right)
	}
}

// SanitizeActionNonWireDSLTemplates neutralizes {{run.*}} on non-wire action
// fields (Name, Method, BodyEncoding, Metadata, Control headers) and normalizes
// typed maps inside Params so valid runtime {{run.id}} values do not fail
// dsl.ValidateScriptIssues.
func SanitizeActionNonWireDSLTemplates(action *dsl.Action) {
	if action == nil {
		return
	}
	action.Name = NeutralizeScriptMetadataString(action.Name)
	action.Type = NeutralizeScriptMetadataString(action.Type)
	action.Method = NeutralizeScriptMetadataString(action.Method)
	action.BodyEncoding = NeutralizeScriptMetadataString(action.BodyEncoding)
	action.BodyTemplateEncoding = NeutralizeScriptMetadataString(action.BodyTemplateEncoding)
	action.BodyFile = NeutralizeScriptMetadataString(action.BodyFile)
	action.ObjectRef = NeutralizeScriptMetadataString(action.ObjectRef)
	action.ThinkTime.Type = NeutralizeScriptMetadataString(action.ThinkTime.Type)
	action.ThinkTime.Distribution = NeutralizeScriptMetadataString(action.ThinkTime.Distribution)
	for i := range action.PacingIDs {
		action.PacingIDs[i] = NeutralizeScriptMetadataString(action.PacingIDs[i])
	}
	if action.WaitFor != nil {
		action.WaitFor.Type = NeutralizeScriptMetadataString(action.WaitFor.Type)
	}
	if action.Sync != nil {
		action.Sync.Type = NeutralizeScriptMetadataString(action.Sync.Type)
	}
	if action.Auth != nil {
		action.Auth.Type = NeutralizeScriptMetadataString(action.Auth.Type)
		for i := range action.Auth.Hosts {
			action.Auth.Hosts[i] = NeutralizeScriptMetadataString(action.Auth.Hosts[i])
		}
	}
	if len(action.Metadata) > 0 && structHasAnyRunTemplate(action.Metadata) {
		if m, ok := NeutralizeActionMetadataValue(action.Metadata).(map[string]any); ok {
			action.Metadata = m
		}
	}
	if len(action.Params) > 0 {
		action.Params = normalizeParamsContainerTypes(action.Params)
	}
	if action.Control != nil {
		sanitizeConditionDSLTemplates(action.Control.Condition)
		if action.Control.ForEach != nil {
			action.Control.ForEach.Source = NeutralizeScriptMetadataString(action.Control.ForEach.Source)
			action.Control.ForEach.Var = NeutralizeScriptMetadataString(action.Control.ForEach.Var)
			sanitizeConditionDSLTemplates(action.Control.ForEach.Condition)
		}
		for i := range action.Control.Branches {
			action.Control.Branches[i].Name = NeutralizeScriptMetadataString(action.Control.Branches[i].Name)
		}
		for i := range action.Control.Cases {
			sanitizeConditionDSLTemplates(action.Control.Cases[i].Condition)
		}
	}
}

func normalizeParamsContainerTypes(params map[string]any) map[string]any {
	if !structHasAnyRunTemplate(params) {
		return params
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		if k == "requests" {
			out[k] = v
			continue
		}
		out[k] = normalizeParamRuntimeValue(v)
	}
	return out
}

func normalizeParamRuntimeValue(value any) any {
	if !structHasAnyRunTemplate(value) {
		return value
	}
	switch typed := value.(type) {
	case map[string]string:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = v
		}
		return out
	case []map[string]any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeParamsContainerTypes(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeParamRuntimeValue(item)
		}
		return out
	case map[string]any:
		return normalizeParamsContainerTypes(typed)
	default:
		return value
	}
}

// SanitizeActionDSLTemplates neutralizes invalid/out-of-scope {{run.*}} tokens
// and undeclared {{file.*}} references across all fields of action.
func SanitizeActionDSLTemplates(action *dsl.Action, declaredFiles map[string]bool) {
	if action == nil {
		return
	}
	SanitizeActionNonWireDSLTemplates(action)
	action.Target = NeutralizeActionSurfaceString(action.Target, declaredFiles)
	action.Selector = NeutralizeActionSurfaceString(action.Selector, declaredFiles)
	action.Value = NeutralizeActionSurfaceString(action.Value, declaredFiles)
	origBody := action.Body
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") &&
		strings.Contains(action.Body, "{{") &&
		!dsl.IsTrustedFullBodyBase64Template(strings.TrimSpace(action.Body)) {
		action.Body = neutralizeMatchingTemplates(action.Body, func(_, _ string) bool { return true })
	} else {
		action.Body = NeutralizeActionSurfaceString(action.Body, declaredFiles)
	}
	if action.Body != origBody {
		if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") {
			action.BodyEncoding = ""
		}
		if strings.EqualFold(strings.TrimSpace(action.BodyTemplateEncoding), "json") {
			action.BodyTemplateEncoding = ""
		}
	}
	if action.BodyMultipart != nil {
		sanitizeMultipartBodyDSLTemplates(action.BodyMultipart, declaredFiles)
	}
	if action.Headers != nil {
		action.Headers = NeutralizeStringMapForActionSurface(action.Headers, declaredFiles)
	}
	if len(action.HeaderList) > 0 {
		sanitizeHeaderListDSLTemplates(action.HeaderList, declaredFiles)
	}
	if action.Auth != nil {
		sanitizeAuthConfigDSLTemplates(action.Auth, declaredFiles)
	}
	if action.SOAP != nil {
		sanitizeSOAPConfigDSLTemplates(action.SOAP, declaredFiles)
	}
	if action.WaitFor != nil {
		action.WaitFor.Value = NeutralizeActionMetadataString(action.WaitFor.Value)
	}
	if action.Sync != nil {
		action.Sync.Value = NeutralizeActionMetadataString(action.Sync.Value)
	}
	if action.Attribution != nil {
		action.Attribution.PageName = NeutralizeActionMetadataString(action.Attribution.PageName)
		action.Attribution.HierarchyPath = NeutralizeActionMetadataString(action.Attribution.HierarchyPath)
	}
	for i := range action.Extractors {
		ex := &action.Extractors[i]
		expr := NeutralizeActionMetadataString(ex.Expression)
		var defVal *string
		if ex.DefaultValue != nil {
			s := NeutralizeActionMetadataString(*ex.DefaultValue)
			defVal = &s
		}
		if sanitized, ok := NeutralizeScriptMetadataDeep(*ex).(dsl.Extractor); ok {
			*ex = sanitized
		}
		ex.Expression = expr
		ex.DefaultValue = defVal
	}
	for i := range action.Assertions {
		as := &action.Assertions[i]
		val := NeutralizeActionMetadataString(as.Value)
		exp := NeutralizeActionMetadataValue(as.Expected)
		if sanitized, ok := NeutralizeScriptMetadataDeep(*as).(dsl.Assertion); ok {
			*as = sanitized
		}
		as.Value = val
		as.Expected = exp
	}
	if len(action.Params) > 0 && structHasAnyRunOrUndeclaredFileTemplate(action.Params, declaredFiles) {
		isHTTPSeq := strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence")
		var savedRequests any
		var hadRequests bool
		if isHTTPSeq {
			savedRequests, hadRequests = action.Params["requests"]
		}
		if m, ok := NeutralizeActionSurfaceValue(action.Params, declaredFiles).(map[string]any); ok {
			action.Params = m
		}
		if isHTTPSeq && hadRequests {
			action.Params["requests"] = savedRequests
			sanitizeHTTPSequenceRequestsParam(action.Params, declaredFiles)
		}
	}
	if len(action.Metadata) > 0 && structHasAnyRunTemplate(action.Metadata) {
		if m, ok := NeutralizeActionMetadataValue(action.Metadata).(map[string]any); ok {
			action.Metadata = m
		}
	}
}

// SanitizeScriptDSLTemplates runs a complete post-assembly DSL template hygiene
// pass across script-scope fields (Name, Config, Metadata, Parameters,
// DataSources) and all lifecycle action trees (Init, Actions, Teardown, nested
// control flow, and http_sequence requests). When an executable action has
// invalid {{run.*}} or undeclared {{file.*}} tokens on its wire surface and
// gateInvalidAction is non-nil, gateInvalidAction is invoked on the action
// before neutralizing the remaining invalid tokens.
func SanitizeScriptDSLTemplates(script *dsl.Script, gateInvalidAction func(action *dsl.Action)) {
	if script == nil {
		return
	}
	declaredFiles := DeclaredFileDataSources(script)

	initActions := script.Init
	mainActions := script.Actions
	teardownActions := script.Teardown
	defaultHeaders := script.Config.HTTP.DefaultHeaders
	defaultHeaderList := script.Config.HTTP.DefaultHeaderList

	outside := *script
	outside.Init = nil
	outside.Actions = nil
	outside.Teardown = nil
	outside.Config.HTTP.DefaultHeaders = nil
	outside.Config.HTTP.DefaultHeaderList = nil
	if sanitizedOutside, ok := NeutralizeScriptMetadataDeep(&outside).(*dsl.Script); ok && sanitizedOutside != nil {
		*script = *sanitizedOutside
	}

	usesHTTPHeaders := false
	switch strings.ToLower(strings.TrimSpace(script.Protocol)) {
	case "", "http", "soap", "java_http":
		usesHTTPHeaders = true
	}
	if defaultHeaders != nil {
		cleanHeaders := make(map[string]string, len(defaultHeaders))
		for k, v := range defaultHeaders {
			safeKey := NeutralizeScriptMetadataString(k)
			safeVal := NeutralizeScriptMetadataString(v)
			if usesHTTPHeaders {
				safeVal = NeutralizeActionMetadataString(v)
			}
			cleanHeaders[safeKey] = safeVal
		}
		script.Config.HTTP.DefaultHeaders = cleanHeaders
	}
	if len(defaultHeaderList) > 0 {
		cleanList := make(dsl.HeaderList, len(defaultHeaderList))
		for i, h := range defaultHeaderList {
			safeVal := NeutralizeScriptMetadataString(h.Value)
			if usesHTTPHeaders {
				safeVal = NeutralizeActionMetadataString(h.Value)
			}
			cleanList[i] = dsl.HeaderEntry{
				Name:  NeutralizeScriptMetadataString(h.Name),
				Value: safeVal,
			}
		}
		script.Config.HTTP.DefaultHeaderList = cleanList
	}

	script.Init = initActions
	script.Actions = mainActions
	script.Teardown = teardownActions

	sanitizeActionSliceDSLTemplates(script.Init, declaredFiles, gateInvalidAction, 0)
	sanitizeActionSliceDSLTemplates(script.Actions, declaredFiles, gateInvalidAction, 0)
	sanitizeActionSliceDSLTemplates(script.Teardown, declaredFiles, gateInvalidAction, 0)
}

func sanitizeActionSliceDSLTemplates(
	actions []dsl.Action,
	declaredFiles map[string]bool,
	gateInvalidAction func(action *dsl.Action),
	depth int,
) {
	if depth > 64 {
		return
	}
	for i := range actions {
		sanitizeSingleActionTreeDSLTemplates(&actions[i], declaredFiles, gateInvalidAction, depth)
	}
}

func sanitizeSingleActionTreeDSLTemplates(
	action *dsl.Action,
	declaredFiles map[string]bool,
	gateInvalidAction func(action *dsl.Action),
	depth int,
) {
	if action == nil || depth > 64 {
		return
	}
	if action.Control != nil {
		c := action.Control
		sanitizeActionSliceDSLTemplates(c.Steps, declaredFiles, gateInvalidAction, depth+1)
		sanitizeActionSliceDSLTemplates(c.Else, declaredFiles, gateInvalidAction, depth+1)
		sanitizeActionSliceDSLTemplates(c.Default, declaredFiles, gateInvalidAction, depth+1)
		for i := range c.Cases {
			sanitizeActionSliceDSLTemplates(c.Cases[i].Steps, declaredFiles, gateInvalidAction, depth+1)
		}
		for i := range c.Branches {
			sanitizeActionSliceDSLTemplates(c.Branches[i].Steps, declaredFiles, gateInvalidAction, depth+1)
		}
	}
	isControl := action.Control != nil || strings.HasPrefix(strings.ToLower(strings.TrimSpace(action.Type)), "control_")
	if action.Type != "source_review" && !isControl && ActionHasInvalidDSLTemplates(*action, declaredFiles) {
		if gateInvalidAction != nil {
			gateInvalidAction(action)
		}
	}
	SanitizeActionDSLTemplates(action, declaredFiles)
}

func sanitizeHTTPSequenceRequestsParam(params map[string]any, declaredFiles map[string]bool) {
	raw, ok := params["requests"]
	if !ok || raw == nil || !structHasAnyRunOrUndeclaredFileTemplate(raw, declaredFiles) {
		return
	}
	switch typed := raw.(type) {
	case []dsl.Action:
		out := make([]dsl.Action, len(typed))
		for i := range typed {
			out[i] = typed[i]
			SanitizeActionDSLTemplates(&out[i], declaredFiles)
		}
		params["requests"] = out
	case []*dsl.Action:
		out := make([]*dsl.Action, len(typed))
		for i, item := range typed {
			if item != nil {
				cp := *item
				SanitizeActionDSLTemplates(&cp, declaredFiles)
				out[i] = &cp
			}
		}
		params["requests"] = out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			switch child := item.(type) {
			case map[string]any:
				out[i] = sanitizeHTTPSequenceRequestMap(child, declaredFiles)
			case dsl.Action:
				SanitizeActionDSLTemplates(&child, declaredFiles)
				out[i] = child
			case *dsl.Action:
				if child != nil {
					cp := *child
					SanitizeActionDSLTemplates(&cp, declaredFiles)
					out[i] = &cp
				}
			default:
				out[i] = NeutralizeActionSurfaceValue(item, declaredFiles)
			}
		}
		params["requests"] = out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeHTTPSequenceRequestMap(item, declaredFiles)
		}
		params["requests"] = out
	default:
		params["requests"] = NeutralizeActionSurfaceValue(raw, declaredFiles)
	}
}

func sanitizeHTTPSequenceRequestMap(m map[string]any, declaredFiles map[string]bool) map[string]any {
	if !structHasAnyRunOrUndeclaredFileTemplate(m, declaredFiles) {
		return m
	}
	out := make(map[string]any, len(m))
	rawBodyEnc, _ := m["body_encoding"].(string)
	rawBodyTplEnc, _ := m["body_template_encoding"].(string)
	origBody, hasBodyStr := m["body"].(string)
	isBase64 := strings.EqualFold(strings.TrimSpace(rawBodyEnc), "base64")
	isJSONTpl := strings.EqualFold(strings.TrimSpace(rawBodyTplEnc), "json")

	for k, v := range m {
		safeKey := neutralizeMapKeyForActionSurface(k, declaredFiles)
		switch safeKey {
		case "name", "transaction", "type", "method", "object_ref", "body_encoding", "body_template_encoding", "body_file", "http_version", "pacing_ids", "think_time", "capture", "control":
			out[safeKey] = NeutralizeStructSurfaceDeep(v, declaredFiles)
			continue
		case "body":
			if s, ok := v.(string); ok {
				if isBase64 && strings.Contains(s, "{{") && !dsl.IsTrustedFullBodyBase64Template(strings.TrimSpace(s)) {
					out[safeKey] = neutralizeMatchingTemplates(s, func(_, _ string) bool { return true })
				} else {
					out[safeKey] = NeutralizeActionSurfaceString(s, declaredFiles)
				}
				continue
			}
		case "headers":
			if hm, ok := v.(map[string]string); ok {
				out[safeKey] = NeutralizeStringMapForActionSurface(hm, declaredFiles)
				continue
			}
			if hm, ok := v.(map[string]any); ok {
				cleanH := make(map[string]any, len(hm))
				for hk, hv := range hm {
					cleanH[neutralizeMapKeyForActionSurface(hk, declaredFiles)] = NeutralizeActionSurfaceValue(hv, declaredFiles)
				}
				out[safeKey] = cleanH
				continue
			}
		case "header_list":
			out[safeKey] = sanitizeRawHeaderListValue(v, declaredFiles)
			continue
		case "auth":
			out[safeKey] = sanitizeRawAuthValue(v, declaredFiles)
			continue
		case "wait_for", "sync":
			out[safeKey] = sanitizeRawWaitOrSyncValue(v, declaredFiles)
			continue
		case "extractors":
			out[safeKey] = sanitizeRawExtractorsValue(v, declaredFiles)
			continue
		case "assertions":
			out[safeKey] = sanitizeRawAssertionsValue(v, declaredFiles)
			continue
		case "metadata":
			out[safeKey] = NeutralizeActionSurfaceValue(NeutralizeActionMetadataValue(v), declaredFiles)
			continue
		case "body_multipart":
			out[safeKey] = sanitizeRawBodyMultipartMap(v, declaredFiles)
			continue
		}
		out[safeKey] = NeutralizeActionSurfaceValue(v, declaredFiles)
	}
	if hasBodyStr {
		if newBody, _ := out["body"].(string); newBody != origBody {
			if isBase64 {
				out["body_encoding"] = ""
			}
			if isJSONTpl {
				out["body_template_encoding"] = ""
			}
		}
	}
	if structHasAnyRunTemplate(out) && !canStrictDecodeActionMap(out) {
		if strictOut, ok := NeutralizeStructSurfaceDeep(out, declaredFiles).(map[string]any); ok {
			return strictOut
		}
	}
	return out
}

func canStrictDecodeActionMap(m map[string]any) bool {
	encoded, err := json.Marshal(m)
	if err != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var child dsl.Action
	return decoder.Decode(&child) == nil
}

func sanitizeRawHeaderListValue(raw any, declaredFiles map[string]bool) any {
	sanitizeEntryMap := func(m map[string]any) map[string]any {
		out := make(map[string]any, len(m))
		for k, v := range m {
			safeKey := neutralizeMapKeyForActionSurface(k, declaredFiles)
			if safeKey == "name" {
				if s, ok := v.(string); ok {
					out[safeKey] = neutralizeMapKeyForActionSurface(s, declaredFiles)
					continue
				}
			}
			out[safeKey] = NeutralizeActionSurfaceValue(v, declaredFiles)
		}
		return out
	}
	switch typed := raw.(type) {
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			if m, ok := item.(map[string]any); ok {
				out[i] = sanitizeEntryMap(m)
			} else {
				out[i] = NeutralizeActionSurfaceValue(item, declaredFiles)
			}
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeEntryMap(item)
		}
		return out
	default:
		return NeutralizeActionSurfaceValue(raw, declaredFiles)
	}
}

func sanitizeRawAuthValue(raw any, declaredFiles map[string]bool) any {
	if m, ok := raw.(map[string]any); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			safeKey := neutralizeMapKeyForActionSurface(k, declaredFiles)
			if safeKey == "type" || safeKey == "hosts" {
				out[safeKey] = NeutralizeStructSurfaceDeep(v, declaredFiles)
				continue
			}
			out[safeKey] = NeutralizeActionSurfaceValue(v, declaredFiles)
		}
		return out
	}
	return NeutralizeActionSurfaceValue(raw, declaredFiles)
}

func sanitizeRawWaitOrSyncValue(raw any, declaredFiles map[string]bool) any {
	if m, ok := raw.(map[string]any); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			safeKey := neutralizeMapKeyForActionSurface(k, declaredFiles)
			if safeKey == "value" {
				out[safeKey] = NeutralizeActionSurfaceValue(v, declaredFiles)
				continue
			}
			out[safeKey] = NeutralizeStructSurfaceDeep(v, declaredFiles)
		}
		return out
	}
	return NeutralizeStructSurfaceDeep(raw, declaredFiles)
}

func sanitizeRawExtractorsValue(raw any, declaredFiles map[string]bool) any {
	sanitizeExMap := func(m map[string]any) map[string]any {
		out := make(map[string]any, len(m))
		for k, v := range m {
			safeKey := neutralizeMapKeyForActionSurface(k, declaredFiles)
			if safeKey == "expression" || safeKey == "default_value" {
				out[safeKey] = NeutralizeActionSurfaceValue(v, declaredFiles)
				continue
			}
			out[safeKey] = NeutralizeStructSurfaceDeep(v, declaredFiles)
		}
		return out
	}
	sanitizeExStruct := func(ex dsl.Extractor) dsl.Extractor {
		expr := NeutralizeActionSurfaceString(ex.Expression, declaredFiles)
		var defVal *string
		if ex.DefaultValue != nil {
			s := NeutralizeActionSurfaceString(*ex.DefaultValue, declaredFiles)
			defVal = &s
		}
		if sanitized, ok := NeutralizeStructSurfaceDeep(ex, declaredFiles).(dsl.Extractor); ok {
			ex = sanitized
		}
		ex.Expression = expr
		ex.DefaultValue = defVal
		return ex
	}
	switch typed := raw.(type) {
	case []dsl.Extractor:
		out := make([]dsl.Extractor, len(typed))
		for i, item := range typed {
			out[i] = sanitizeExStruct(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			switch child := item.(type) {
			case map[string]any:
				out[i] = sanitizeExMap(child)
			case dsl.Extractor:
				out[i] = sanitizeExStruct(child)
			default:
				out[i] = NeutralizeStructSurfaceDeep(item, declaredFiles)
			}
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeExMap(item)
		}
		return out
	default:
		return NeutralizeStructSurfaceDeep(raw, declaredFiles)
	}
}

func sanitizeRawAssertionsValue(raw any, declaredFiles map[string]bool) any {
	sanitizeAsMap := func(m map[string]any) map[string]any {
		out := make(map[string]any, len(m))
		for k, v := range m {
			safeKey := neutralizeMapKeyForActionSurface(k, declaredFiles)
			if safeKey == "value" || safeKey == "expected" {
				out[safeKey] = NeutralizeActionSurfaceValue(v, declaredFiles)
				continue
			}
			out[safeKey] = NeutralizeStructSurfaceDeep(v, declaredFiles)
		}
		return out
	}
	sanitizeAsStruct := func(as dsl.Assertion) dsl.Assertion {
		val := NeutralizeActionSurfaceString(as.Value, declaredFiles)
		exp := NeutralizeActionSurfaceValue(as.Expected, declaredFiles)
		if sanitized, ok := NeutralizeStructSurfaceDeep(as, declaredFiles).(dsl.Assertion); ok {
			as = sanitized
		}
		as.Value = val
		as.Expected = exp
		return as
	}
	switch typed := raw.(type) {
	case []dsl.Assertion:
		out := make([]dsl.Assertion, len(typed))
		for i, item := range typed {
			out[i] = sanitizeAsStruct(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			switch child := item.(type) {
			case map[string]any:
				out[i] = sanitizeAsMap(child)
			case dsl.Assertion:
				out[i] = sanitizeAsStruct(child)
			default:
				out[i] = NeutralizeStructSurfaceDeep(item, declaredFiles)
			}
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeAsMap(item)
		}
		return out
	default:
		return NeutralizeStructSurfaceDeep(raw, declaredFiles)
	}
}

type metadataReflectVisit struct {
	kind     reflect.Kind
	typeName reflect.Type
	address  uintptr
	length   int
}

type metadataReflectMemoEntry struct {
	value   reflect.Value
	changed bool
}

// NeutralizeScriptMetadataDeep recursively neutralizes {{run.*}} tokens inside
// arbitrary exported struct fields, pointers, interfaces, slices, arrays, and
// maps stored under script.Metadata. It preserves exact Go types and uses
// copy-on-write so caller-owned values outside script.Metadata (such as
// ConvertResult.SourceReview or FidelityReport) remain verbatim.
func NeutralizeScriptMetadataDeep(value any) any {
	return neutralizeReflectDeep(value, NeutralizeScriptMetadataString)
}

// NeutralizeStructSurfaceDeep recursively neutralizes all {{run.*}} tokens and
// undeclared {{file.*}} tokens inside arbitrary structs, pointers, slices,
// arrays, and maps while preserving their exact Go types.
func NeutralizeStructSurfaceDeep(value any, declaredFiles map[string]bool) any {
	return neutralizeReflectDeep(value, func(s string) string {
		return NeutralizeActionSurfaceString(NeutralizeScriptMetadataString(s), declaredFiles)
	})
}

func neutralizeReflectDeep(value any, transformString func(string) string) any {
	if value == nil {
		return nil
	}
	v := reflect.ValueOf(value)
	inProgress := make(map[metadataReflectVisit]struct{})
	memo := make(map[metadataReflectVisit]metadataReflectMemoEntry)
	out, changed := neutralizeReflectValue(v, inProgress, memo, 0, transformString)
	if !changed {
		return value
	}
	return out.Interface()
}

func neutralizeReflectValue(
	v reflect.Value,
	inProgress map[metadataReflectVisit]struct{},
	memo map[metadataReflectVisit]metadataReflectMemoEntry,
	depth int,
	transformString func(string) string,
) (reflect.Value, bool) {
	if !v.IsValid() || depth > 64 {
		return v, false
	}
	switch v.Kind() {
	case reflect.String:
		raw := v.String()
		safe := transformString(raw)
		if safe == raw {
			return v, false
		}
		out := reflect.New(v.Type()).Elem()
		out.SetString(safe)
		return out, true

	case reflect.Interface:
		if v.IsNil() {
			return v, false
		}
		elemOut, changed := neutralizeReflectValue(v.Elem(), inProgress, memo, depth+1, transformString)
		if !changed {
			return v, false
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(elemOut)
		return out, true

	case reflect.Pointer:
		if v.IsNil() {
			return v, false
		}
		visit := metadataReflectVisit{kind: v.Kind(), typeName: v.Type(), address: v.Pointer()}
		if cached, ok := memo[visit]; ok {
			return cached.value, cached.changed
		}
		if _, active := inProgress[visit]; active {
			return v, false
		}
		inProgress[visit] = struct{}{}
		elemOut, changed := neutralizeReflectValue(v.Elem(), inProgress, memo, depth+1, transformString)
		delete(inProgress, visit)
		if !changed {
			memo[visit] = metadataReflectMemoEntry{value: v, changed: false}
			return v, false
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(elemOut)
		memo[visit] = metadataReflectMemoEntry{value: out, changed: true}
		return out, true

	case reflect.Slice:
		if v.IsNil() {
			return v, false
		}
		visit := metadataReflectVisit{kind: v.Kind(), typeName: v.Type(), address: v.Pointer(), length: v.Len()}
		if cached, ok := memo[visit]; ok {
			return cached.value, cached.changed
		}
		if _, active := inProgress[visit]; active {
			return v, false
		}
		inProgress[visit] = struct{}{}
		var out reflect.Value
		changed := false
		for i := 0; i < v.Len(); i++ {
			itemOut, itemChanged := neutralizeReflectValue(v.Index(i), inProgress, memo, depth+1, transformString)
			if itemChanged {
				if !changed {
					out = reflect.MakeSlice(v.Type(), v.Len(), v.Len())
					reflect.Copy(out, v)
					changed = true
				}
				out.Index(i).Set(itemOut)
			}
		}
		delete(inProgress, visit)
		if !changed {
			memo[visit] = metadataReflectMemoEntry{value: v, changed: false}
			return v, false
		}
		memo[visit] = metadataReflectMemoEntry{value: out, changed: true}
		return out, true

	case reflect.Array:
		var out reflect.Value
		changed := false
		for i := 0; i < v.Len(); i++ {
			itemOut, itemChanged := neutralizeReflectValue(v.Index(i), inProgress, memo, depth+1, transformString)
			if itemChanged {
				if !changed {
					out = reflect.New(v.Type()).Elem()
					out.Set(v)
					changed = true
				}
				out.Index(i).Set(itemOut)
			}
		}
		if !changed {
			return v, false
		}
		return out, true

	case reflect.Map:
		if v.IsNil() {
			return v, false
		}
		visit := metadataReflectVisit{kind: v.Kind(), typeName: v.Type(), address: v.Pointer()}
		if cached, ok := memo[visit]; ok {
			return cached.value, cached.changed
		}
		if _, active := inProgress[visit]; active {
			return v, false
		}
		inProgress[visit] = struct{}{}
		type mapPair struct {
			k       reflect.Value
			v       reflect.Value
			changed bool
		}
		var pairs []mapPair
		anyChanged := false
		iter := v.MapRange()
		for iter.Next() {
			mk := iter.Key()
			mv := iter.Value()
			ko, kc := neutralizeReflectValue(mk, inProgress, memo, depth+1, transformString)
			vo, vc := neutralizeReflectValue(mv, inProgress, memo, depth+1, transformString)
			if kc || vc {
				anyChanged = true
			}
			pairs = append(pairs, mapPair{k: ko, v: vo, changed: kc || vc})
		}
		delete(inProgress, visit)
		if !anyChanged {
			memo[visit] = metadataReflectMemoEntry{value: v, changed: false}
			return v, false
		}
		out := reflect.MakeMapWithSize(v.Type(), len(pairs))
		for _, p := range pairs {
			if !p.changed {
				out.SetMapIndex(p.k, p.v)
			}
		}
		for _, p := range pairs {
			if p.changed {
				out.SetMapIndex(p.k, p.v)
			}
		}
		memo[visit] = metadataReflectMemoEntry{value: out, changed: true}
		return out, true

	case reflect.Struct:
		var out reflect.Value
		changed := false
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath != "" {
				continue
			}
			fieldOut, fieldChanged := neutralizeReflectValue(v.Field(i), inProgress, memo, depth+1, transformString)
			if fieldChanged {
				if !changed {
					out = reflect.New(v.Type()).Elem()
					out.Set(v)
					changed = true
				}
				out.Field(i).Set(fieldOut)
			}
		}
		if !changed {
			return v, false
		}
		return out, true

	default:
		return v, false
	}
}
