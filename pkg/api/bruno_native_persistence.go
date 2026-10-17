package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

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
const brunoNativeImporterIdentity = importreview.BrunoNativeImporterIdentity

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
	if preserveImportedCredentials {
		return projected, nil
	}
	restoreBrunoNativeCompactProvenance(projected, script)
	return projected, nil
}

// restoreBrunoNativeCompactProvenance restores only bounded, server-generated
// native-import identifiers and auth-mode enums after the generic metadata
// scrubber has run. The raw Bruno source remains exclusively in the encrypted
// shared artifact; no authored values or JavaScript are copied back here.
var restoreBrunoNativeCompactProvenance = importreview.RestoreBrunoNativeCompactProvenance

var isBrunoNativeAuthMode = importreview.IsBrunoNativeAuthMode

var brunoNativeFindingIDs = importreview.BrunoNativeFindingIDs

func isBrunoNativeXMLAction(action dsl.Action) bool {
	if !strings.EqualFold(strings.TrimSpace(anyStringValue(action.Metadata["source_profile"])), "bruno_native") {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(action.Body), "<")
}

var anyStringValue = importreview.AnyStringValue

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
