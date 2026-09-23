package api

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

//go:embed import_settings_fields.json
var importSettingsFieldsJSON []byte

type importSettingField struct {
	Category string   `json:"category"`
	Path     string   `json:"path"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"`
}

var importSettingFields = func() []importSettingField {
	var fields []importSettingField
	if err := json.Unmarshal(importSettingsFieldsJSON, &fields); err != nil {
		panic(err)
	}
	return fields
}()

type ImportSettingsChange struct {
	Path      string          `json:"path"`
	Parameter string          `json:"parameter,omitempty"`
	Value     json.RawMessage `json:"value"`
}
type ImportSettingsRequest struct {
	Script   *dsl.Script            `json:"script"`
	Category string                 `json:"category"`
	Changes  []ImportSettingsChange `json:"changes"`
}
type ImportSettingsResponse struct {
	Script                    *dsl.Script                `json:"script"`
	ImportDraftExpiresAt      *time.Time                 `json:"import_draft_expires_at,omitempty"`
	ValidationIssues          []string                   `json:"validation_issues"`
	Findings                  []importreview.Finding     `json:"findings"`
	Readiness                 importreview.Readiness     `json:"readiness"`
	SecretBindingRequirements []SecretBindingRequirement `json:"secret_binding_requirements"`
}

// handleImportSettings applies one category atomically to the exact import draft.
// Source metadata, credentials and executable actions are never client replacements.
func (s *Server) handleImportSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	var req ImportSettingsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || req.Script == nil || len(req.Changes) == 0 || len(req.Changes) > 256 {
		writeError(w, 400, "Supply a draft and between 1 and 256 setting changes")
		return
	}
	claim, err := s.claimImportDraftMutation(r, req.Script)
	if err != nil {
		writeError(w, 409, "Import draft is stale or expired; re-import to continue")
		return
	}
	defer func() {
		if claim != nil {
			s.releaseImportDraftAttestation(claim)
		}
	}()
	target := req.Script
	profile := genericImportDraftProfile(target, "")
	if claim != nil {
		target = claim.Script
		profile = claim.Profile
	}
	if target == nil || !s.requireDomainFeatureForProject(w, target.ProjectID, featureIDForImportProfile(profile)) {
		return
	}
	draft, fieldErrors := applyImportSettings(target, req.Category, req.Changes)
	if len(fieldErrors) > 0 {
		writeJSON(w, 422, map[string]any{"error": "Check the highlighted settings", "field_errors": fieldErrors})
		return
	}
	findings, err := importreview.RefreshDependencies(draft)
	if err != nil {
		writeError(w, 422, "Cannot evaluate draft dependencies")
		return
	}
	// Rebuild runtime coverage from the current draft; preserved source files remain untouched.
	if draft.Metadata != nil && draft.Metadata.Import != nil && draft.Metadata.Import.Preserved["runtime_settings_parity"] != nil {
		parity := buildValidationRuntimeSettingsParity(draft)
		if len(parity) > 0 {
			if draft.Metadata.Import.Preserved == nil {
				draft.Metadata.Import.Preserved = map[string]any{}
			}
			draft.Metadata.Import.Preserved["runtime_settings_parity"] = parity
		}
	}
	issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(draft)
	requirements := discoverSecretBindingRequirements(draft, 0)
	readiness, err := importreview.EvaluateScript(draft, importreview.EvaluationInput{ValidationIssues: issues, UnresolvedBindings: len(requirements)})
	if err != nil {
		writeError(w, 422, "Cannot evaluate draft readiness")
		return
	}
	var expiry *time.Time
	if claim != nil {
		expires, err := s.completeImportDraftMutation(r, claim, draft)
		if err != nil {
			writeError(w, 503, "Could not secure the updated draft; retry")
			return
		}
		claim = nil
		if !expires.IsZero() {
			expiry = &expires
		}
	}
	s.recordAuditEvent(r, "script.import.settings", "success", "script", draft.Name, draft.ProjectID, map[string]any{"category": req.Category, "changes": len(req.Changes)})
	writeJSON(w, 200, ImportSettingsResponse{Script: repairDisclosure(draft), ImportDraftExpiresAt: expiry, ValidationIssues: issues, Findings: findings, Readiness: readiness, SecretBindingRequirements: requirements})
}

func applyImportSettings(source *dsl.Script, category string, changes []ImportSettingsChange) (*dsl.Script, map[string]string) {
	errors := map[string]string{}
	raw, _ := json.Marshal(source)
	var wire map[string]any
	_ = decodeImportSettingJSON(raw, &wire)
	safeRaw, _ := json.Marshal(repairDisclosure(source))
	var safe map[string]any
	_ = decodeImportSettingJSON(safeRaw, &safe)
	seen := map[string]bool{}
	for _, change := range changes {
		key := change.Path
		path := strings.Split(change.Path, ".")
		var field *importSettingField
		if change.Parameter != "" {
			key = change.Parameter + "." + change.Path
			for i, p := range source.Parameters {
				if p.Name == change.Parameter && category == "parameters:"+p.Type {
					if f, ok := importParameterSettingFields[change.Path]; ok {
						copy := f
						field = &copy
						path = []string{"parameters", fmt.Sprint(i), change.Path}
					}
					break
				}
			}
		} else {
			for _, f := range importSettingFields {
				if f.Path == change.Path && f.Category == category {
					copy := f
					field = &copy
					break
				}
			}
		}
		if field == nil || seen[key] {
			errors[key] = "This field is not editable in this category"
			continue
		}
		seen[key] = true
		var value any
		if decodeImportSettingJSON(change.Value, &value) != nil {
			errors[key] = "Invalid value"
			continue
		}
		if !validImportSettingValue(*field, value) {
			errors[key] = "Enter a valid " + field.Type + " value"
			continue
		}
		if !reflect.DeepEqual(importSettingValue(wire, path), importSettingValue(safe, path)) {
			errors[key] = "Protected values must be managed in Credentials"
			continue
		}
		setImportSettingValue(wire, path, value)
	}
	if len(errors) > 0 {
		return nil, errors
	}
	updated, _ := json.Marshal(wire)
	var draft dsl.Script
	dec := json.NewDecoder(bytes.NewReader(updated))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&draft); err != nil {
		errors["category"] = "One or more settings have an invalid type"
		return nil, errors
	}
	// Only new validation failures block this category; existing import blockers remain visible.
	oldIssues := map[string]bool{}
	for _, issue := range dsl.ValidateScriptIssuesWithPreservedImportCredentials(source) {
		oldIssues[issue] = true
	}
	for _, issue := range dsl.ValidateScriptIssuesWithPreservedImportCredentials(&draft) {
		if !oldIssues[issue] {
			matched := false
			for _, change := range changes {
				if change.Parameter == "" && strings.HasPrefix(issue, change.Path+" ") {
					errors[change.Path] = issue
					matched = true
				}
			}
			if !matched {
				errors["category"] = issue
			}
		}
	}
	projected, _ := json.Marshal(repairDisclosure(&draft))
	var afterSafe map[string]any
	_ = decodeImportSettingJSON(projected, &afterSafe)
	afterRaw, _ := json.Marshal(&draft)
	var after map[string]any
	_ = decodeImportSettingJSON(afterRaw, &after)
	for _, change := range changes {
		path := strings.Split(change.Path, ".")
		key := change.Path
		if change.Parameter != "" {
			key = change.Parameter + "." + change.Path
			for i, p := range draft.Parameters {
				if p.Name == change.Parameter {
					path = []string{"parameters", fmt.Sprint(i), change.Path}
					break
				}
			}
		}
		if !reflect.DeepEqual(importSettingValue(after, path), importSettingValue(afterSafe, path)) {
			errors[key] = "Use Credentials for sensitive values"
		}
	}
	if len(errors) > 0 {
		return nil, errors
	}
	return &draft, errors
}

// Keep integer tokens exact while editing the untyped tree, including unrelated
// generator seeds outside JavaScript's safe range. The typed script decoder
// retains the DSL's normal number types for assertion payloads.
func decodeImportSettingJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func validImportSettingValue(f importSettingField, v any) bool {
	if v == nil {
		return true
	} // Clear a setting to its documented default.
	switch f.Type {
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		number, ok := v.(json.Number)
		n, err := number.Float64()
		return ok && err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0
	case "signed":
		number, ok := v.(json.Number)
		_, err := number.Float64()
		return ok && err == nil
	case "text":
		s, ok := v.(string)
		return ok && len(s) <= 65536
	case "select":
		s, ok := v.(string)
		if !ok {
			return false
		}
		for _, option := range f.Options {
			if s == option {
				return true
			}
		}
		return false
	case "lines":
		values, ok := v.([]any)
		if !ok || len(values) > 10000 {
			return false
		}
		for _, v := range values {
			s, ok := v.(string)
			if !ok || len(s) > 65536 {
				return false
			}
		}
		return true
	}
	return false
}
func importSettingValue(v any, path []string) any {
	if len(path) == 0 {
		return v
	}
	if m, ok := v.(map[string]any); ok {
		return importSettingValue(m[path[0]], path[1:])
	}
	if a, ok := v.([]any); ok {
		var i int
		if _, err := fmt.Sscan(path[0], &i); err == nil && i >= 0 && i < len(a) {
			return importSettingValue(a[i], path[1:])
		}
	}
	return nil
}
func setImportSettingValue(v any, path []string, value any) {
	if a, ok := v.([]any); ok {
		var i int
		_, _ = fmt.Sscan(path[0], &i)
		setImportSettingValue(a[i], path[1:], value)
		return
	}
	m := v.(map[string]any)
	if len(path) == 1 {
		if value == nil {
			delete(m, path[0])
		} else {
			m[path[0]] = value
		}
		return
	}
	if m[path[0]] == nil {
		m[path[0]] = map[string]any{}
	}
	setImportSettingValue(m[path[0]], path[1:], value)
}

var importParameterSettingFields = map[string]importSettingField{
	"values": {Type: "lines"}, "data_source": {Type: "text"}, "column": {Type: "text"},
	"selection":     {Type: "select", Options: []string{"sequential", "random", "unique"}},
	"update":        {Type: "select", Options: []string{"once", "each_iteration", "each_occurrence"}},
	"on_exhaustion": {Type: "select", Options: []string{"wrap", "empty", "error", "last"}},
	"same_line_as":  {Type: "text"}, "start_row": {Type: "number"}, "block_size": {Type: "number"}, "auto_allocate": {Type: "boolean"}, "value_for_each_vuser": {Type: "boolean"},
	"format": {Type: "text"}, "timezone": {Type: "text"}, "offset_seconds": {Type: "signed"}, "min": {Type: "signed"}, "max": {Type: "signed"}, "start": {Type: "signed"}, "end": {Type: "signed"}, "step": {Type: "signed"}, "length": {Type: "number"}, "charset": {Type: "text"}, "pad_width": {Type: "number"}, "pad_char": {Type: "text"}, "seed": {Type: "signed"},
}
