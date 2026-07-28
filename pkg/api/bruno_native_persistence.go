package api

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	brunoconvert "github.com/jasonczech/apexload/pkg/convert/bruno"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const protectedBrunoNativeXMLBody = "<apexload-native-xml-body/>"
const brunoNativeImporterIdentity = "apexload-bruno-native-importer"

// preparePersistentBrunoNativeScript applies the ordinary durable-script
// persistence boundary without allowing any generic response projection to
// rewrite a Bruno XML payload. Imported credentials intentionally remain in
// the durable execution copy; ordinary script reads mask them. Protect the
// exact body while the rest of the script is reconciled, then restore the
// source bytes unchanged.
func preparePersistentBrunoNativeScript(script *dsl.Script) (*dsl.Script, error) {
	if script == nil {
		return nil, errors.New("native Bruno script is required")
	}
	candidate := cloneScriptDefinition(script)
	if candidate == nil {
		return nil, errors.New("clone native Bruno script")
	}
	preserveImportedCredentials := isCredentialPreservingImport(candidate)
	protectedBodies := make(map[int]string)
	for index := range candidate.Actions {
		action := &candidate.Actions[index]
		if !isBrunoNativeXMLAction(*action) {
			continue
		}
		if !preserveImportedCredentials {
			if err := validateBrunoNativeXMLCredentialBindings(action.Body); err != nil {
				return nil, fmt.Errorf("action %q XML body: %w", action.Name, err)
			}
		}
		protectedBodies[index] = action.Body
		action.Body = protectedBrunoNativeXMLBody
	}

	projected, err := preparePersistentImportedLibraryScript(candidate, nil, "bruno")
	if err != nil {
		return nil, err
	}
	for index, body := range protectedBodies {
		if index < 0 || index >= len(projected.Actions) {
			return nil, errors.New("native Bruno XML body projection changed action ordering")
		}
		projected.Actions[index].Body = body
	}
	restoreBrunoNativeCompactProvenance(projected, script)
	return projected, nil
}

// restoreBrunoNativeCompactProvenance restores only bounded, server-generated
// native-import identifiers and auth-mode enums after the generic metadata
// scrubber has run. The raw Bruno source remains exclusively in the encrypted
// shared artifact; no authored values or JavaScript are copied back here.
func restoreBrunoNativeCompactProvenance(projected, source *dsl.Script) {
	if projected == nil || source == nil ||
		projected.Metadata == nil || projected.Metadata.Import == nil ||
		source.Metadata == nil || source.Metadata.Import == nil {
		return
	}
	if source.GeneratedBy != brunoNativeImporterIdentity ||
		!strings.EqualFold(strings.TrimSpace(source.Metadata.Import.Source), "bruno") {
		return
	}
	nativeAction := false
	for _, action := range source.Actions {
		if strings.EqualFold(strings.TrimSpace(anyStringValue(action.Metadata["source_profile"])), "bruno_native") {
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

	if mode, ok := sourcePreserved["effective_auth"].(string); ok && isBrunoNativeAuthMode(mode) {
		projectedPreserved["effective_auth"] = mode
	}
	for _, key := range []string{"diagnostic_ids", "finding_ids"} {
		value, exists := sourcePreserved[key]
		if !exists {
			continue
		}
		if ids, ok := brunoNativeFindingIDs(value); ok {
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
	if mode, ok := sourceFields["auth_mode"].(string); ok && isBrunoNativeAuthMode(mode) {
		projectedFields["auth_mode"] = mode
	}
}

func isBrunoNativeAuthMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "inherit", "none", "noauth", "no_auth", "basic", "bearer",
		"digest", "oauth2", "oauth_2", "apikey", "api_key", "awsv4", "ntlm",
		"wsse", "unsupported":
		return true
	default:
		return false
	}
}

func brunoNativeFindingIDs(value any) ([]string, bool) {
	var values []string
	switch typed := value.(type) {
	case []string:
		values = typed
	case []any:
		values = make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, text)
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

func isBrunoNativeXMLAction(action dsl.Action) bool {
	if !strings.EqualFold(strings.TrimSpace(anyStringValue(action.Metadata["source_profile"])), "bruno_native") {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(action.Body), "<")
}

func anyStringValue(value any) string {
	text, _ := value.(string)
	return text
}

type brunoNativeXMLCredentialFrame struct {
	name      string
	sensitive bool
	text      strings.Builder
}

func validateBrunoNativeXMLCredentialBindings(body string) error {
	decoder := xml.NewDecoder(strings.NewReader(body))
	frames := make([]*brunoNativeXMLCredentialFrame, 0, 16)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid XML: %w", err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			for _, attribute := range typed.Attr {
				if !brunoconvert.IsNativeXMLCredentialName(attribute.Name.Local) {
					continue
				}
				if !isGovernedBrunoNativeXMLCredential(attribute.Value) {
					return fmt.Errorf("credential attribute %q must use a governed {{secret.*}} reference", attribute.Name.Local)
				}
			}
			frame := &brunoNativeXMLCredentialFrame{
				name:      typed.Name.Local,
				sensitive: brunoconvert.IsNativeXMLCredentialName(typed.Name.Local),
			}
			frames = append(frames, frame)
		case xml.CharData:
			for _, frame := range frames {
				if frame.sensitive {
					_, _ = frame.text.Write([]byte(typed))
				}
			}
		case xml.EndElement:
			if len(frames) == 0 {
				return errors.New("invalid XML element nesting")
			}
			frame := frames[len(frames)-1]
			frames = frames[:len(frames)-1]
			if frame.sensitive && !isGovernedBrunoNativeXMLCredential(frame.text.String()) {
				return fmt.Errorf("credential element %q must use a governed {{secret.*}} reference", frame.name)
			}
		}
	}
	if len(frames) != 0 {
		return errors.New("invalid XML element nesting")
	}
	return nil
}

func isGovernedBrunoNativeXMLCredential(value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	if !scriptSecretTemplatePattern.MatchString(value) {
		return false
	}
	return strings.TrimSpace(scriptSecretTemplatePattern.ReplaceAllString(value, "")) == ""
}
