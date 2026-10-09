package sample

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// structureMultipartBodies turns recorded multipart/form-data bodies into
// body_multipart, so replay builds each with a fresh boundary and its text
// fields stay editable; requestimport.HoistMultipartFiles then makes the
// uploaded files data sources. A body that an imported credential locator
// points into keeps its recorded form, so the credential workflow (and
// run-output redaction) still finds it.
func structureMultipartBodies(script *dsl.Script) {
	if script == nil {
		return
	}
	for index := range script.Actions {
		action := &script.Actions[index]
		if action.BodyMultipart != nil || action.Body == "" || action.Params[httpprotocol.HTMLStepParam] != nil || bodyHasCredentialTarget(action.Metadata) {
			continue
		}
		var payload []byte
		switch strings.ToLower(strings.TrimSpace(action.BodyEncoding)) {
		case "":
			payload = []byte(action.Body)
		case "base64":
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(action.Body))
			if err != nil {
				continue
			}
			payload = decoded
		default:
			continue
		}
		parts, ok := requestimport.ParseMultipartBody(actionContentType(*action), payload)
		if !ok {
			continue
		}
		structured, ok := requestimport.DSLMultipartBody(parts)
		if !ok {
			continue
		}
		action.BodyMultipart = structured
		action.Body, action.BodyEncoding = "", ""
		// Replay sets the boundary; the recorded one would only mislead.
		for name := range action.Headers {
			if strings.EqualFold(name, "Content-Type") {
				action.Headers[name] = "multipart/form-data"
			}
		}
		for index := range action.HeaderList {
			if strings.EqualFold(action.HeaderList[index].Name, "Content-Type") {
				action.HeaderList[index].Value = "multipart/form-data"
			}
		}
	}
	requestimport.HoistMultipartFiles(script)
}

func actionContentType(action dsl.Action) string {
	for _, header := range action.HeaderList {
		if strings.EqualFold(header.Name, "Content-Type") {
			return header.Value
		}
	}
	for name, value := range action.Headers {
		if strings.EqualFold(name, "Content-Type") {
			return value
		}
	}
	return ""
}

// bodyHasCredentialTarget reports whether an imported credential locator
// points into the request body.
func bodyHasCredentialTarget(metadata map[string]any) bool {
	raw, exists := metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]
	if !exists || raw == nil {
		return false
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return true
	}
	var targets []struct {
		Field string `json:"field"`
	}
	if err := json.Unmarshal(payload, &targets); err != nil {
		return true
	}
	for _, target := range targets {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(target.Field)), "body") {
			return true
		}
	}
	return false
}
