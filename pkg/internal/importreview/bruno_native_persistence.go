package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

const BrunoNativeImporterIdentity = "apexload-bruno-native-importer"

func RestoreBrunoNativeCompactProvenance(projected, source *dsl.Script) {
	if projected == nil || source == nil ||
		projected.Metadata == nil || projected.Metadata.Import == nil ||
		source.Metadata == nil || source.Metadata.Import == nil {
		return
	}
	if source.GeneratedBy != BrunoNativeImporterIdentity ||
		!strings.EqualFold(strings.TrimSpace(source.Metadata.Import.Source), "bruno") {
		return
	}
	nativeAction := false
	for _, action := range source.Actions {
		if strings.EqualFold(strings.TrimSpace(AnyStringValue(action.Metadata["source_profile"])), "bruno_native") {
			nativeAction = true
			break
		}
	}
	if !nativeAction {
		return
	}
	projectedPreserved := projected.Metadata.Import.Preserved
	sourcePreserved := source.Metadata.Import.Preserved
	if projectedPreserved == nil || sourcePreserved == nil {
		return
	}

	if mode, ok := sourcePreserved["effective_auth"].(string); ok && IsBrunoNativeAuthMode(mode) {
		projectedPreserved["effective_auth"] = mode
	}
	for _, key := range []string{"diagnostic_ids", "finding_ids"} {
		value, exists := sourcePreserved[key]
		if !exists {
			continue
		}
		if ids, ok := BrunoNativeFindingIDs(value); ok {
			projectedPreserved[key] = ids
		} else {
			delete(projectedPreserved, key)
		}
	}

	sourceFields, sourceOK := sourcePreserved["effective_fields"].(map[string]any)
	projectedFields, projectedOK := projectedPreserved["effective_fields"].(map[string]any)
	if !sourceOK || !projectedOK {
		return
	}
	if mode, ok := sourceFields["auth_mode"].(string); ok && IsBrunoNativeAuthMode(mode) {
		projectedFields["auth_mode"] = mode
	}
}

func IsBrunoNativeAuthMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "inherit", "none", "noauth", "no_auth", "basic", "bearer",
		"digest", "oauth2", "oauth_2", "apikey", "api_key", "awsv4", "ntlm",
		"wsse", "unsupported":
		return true
	default:
		return false
	}
}

func BrunoNativeFindingIDs(value any) ([]string, bool) {
	var values []string
	switch typed := value.(type) {
	case []string:
		values = typed
	case []any:
		values = make([]string, 0, len(typed))
		for _, item := range typed {
			Text, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, Text)
		}
	default:
		return nil, false
	}
	for _, value := range values {
		const prefix = "bruno-native-"
		if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+24 {
			return nil, false
		}
		for _, char := range value[len(prefix):] {
			if !strings.ContainsRune("0123456789abcdef", char) {
				return nil, false
			}
		}
	}
	out := make([]string, len(values))
	copy(out, values)
	return out, true
}

func AnyStringValue(value any) string {
	Text, _ := value.(string)
	return Text
}
