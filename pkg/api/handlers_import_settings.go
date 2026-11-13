package api

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
)

//go:embed import_settings_fields.json
var importSettingsFieldsJSON []byte

type importSettingField struct {
	Category string   `json:"category"`
	Group    string   `json:"group,omitempty"`
	Subgroup string   `json:"subgroup,omitempty"`
	Path     string   `json:"path"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Unit     string   `json:"unit,omitempty"`
	Options  []string `json:"options,omitempty"`
	Secret   bool     `json:"secret,omitempty"`
}

var importSettingFields = func() []importSettingField {
	var fields []importSettingField
	if err := json.Unmarshal(importSettingsFieldsJSON, &fields); err != nil {
		panic(err)
	}
	return fields
}()

// importSettingFieldsByPath indexes the import contract fields. Their options
// may deliberately narrow the catalog (TLS is 1.2/1.3 only, matching the
// select_tls repair), so a catalog list only fills in a field the contract
// leaves open, whichever category the edit arrives under.
var importSettingFieldsByPath = func() map[string]importSettingField {
	out := make(map[string]importSettingField, len(importSettingFields))
	for _, f := range importSettingFields {
		out[f.Path] = f
	}
	return out
}()

type importSettingsFieldsResponse struct {
	Groups []dsl.ConfigGroupMeta `json:"groups"`
	Fields []importSettingField  `json:"fields"`
}

// handleImportSettingsFields returns the widened import setting field definitions
// combining legacy categories with the canonical dsl.ConfigCatalog() import scope.
func (s *Server) handleImportSettingsFields(w http.ResponseWriter, r *http.Request) {
	protocol := strings.TrimSpace(r.URL.Query().Get("protocol"))
	writeJSON(w, http.StatusOK, importSettingsFieldsResponse{
		Groups: dsl.ConfigGroups(),
		Fields: widenedImportSettingFields(protocol),
	})
}

func widenedImportSettingFields(protocol string) []importSettingField {
	importCatalog := dsl.ConfigCatalogForFilter(protocol, dsl.ConfigScopeImport)
	seen := make(map[string]int, len(importSettingFields))
	out := make([]importSettingField, 0, len(importSettingFields)+len(importCatalog))
	for _, f := range importSettingFields {
		if meta, ok := dsl.ConfigFieldForPath(f.Path); ok {
			if protocol != "" {
				matched := false
				for _, m := range importCatalog {
					if m.Path == f.Path {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			f.Group = meta.Group
			f.Subgroup = meta.Subgroup
			f.Unit = meta.Unit
			f.Secret = meta.Secret
			if len(f.Options) == 0 {
				f.Options = append([]string(nil), meta.Options...)
			}
		} else if protocol != "" {
			continue
		}
		seen[f.Path] = len(out)
		out = append(out, f)
	}
	for _, meta := range importCatalog {
		if _, exists := seen[meta.Path]; exists {
			continue
		}
		out = append(out, importSettingFieldFromCatalogMeta(meta, defaultImportCategoryForCatalogMeta(meta)))
	}
	return out
}

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
	FidelityReport            map[string]any             `json:"fidelity_report,omitempty"`
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
	var claim *importDraftAttestationClaim
	if importDraftAttestationToken(req.Script) != "" || isJMeterBatchChildDraft(req.Script) {
		var err error
		claim, err = s.claimImportDraftMutation(r, req.Script)
		if err != nil {
			if writeImportDraftBusy(w, err) {
				return
			}
			writeError(w, 409, "Import draft is stale or expired; re-import to continue")
			return
		}
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
	findings, err := importreview.RefreshDependencies(draft)
	if err != nil {
		writeError(w, 422, "Cannot evaluate draft dependencies")
		return
	}
	issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(draft)
	requirements := discoverSecretBindingRequirements(draft, 0)
	readiness, err := importreview.EvaluateScript(draft, importreview.EvaluationInput{ValidationIssues: issues, UnresolvedBindings: len(requirements)})
	var report map[string]any
	if importSettingsSelectsTLS(req.Changes) && err == nil {
		resolver := s.runner.secretResolverForProject(draft.ProjectID)
		findings, requirements, readiness, report, err = evaluateRepairedScript(draft, resolver)
		issues = dsl.ValidateScriptIssuesWithPreservedImportCredentials(draft)
		if err == nil {
			attachImportReplayPreflight(draft, profile, repairReplayPreflight(draft, target, resolver))
		}
	}
	if err != nil {
		writeError(w, 422, "Cannot evaluate draft readiness")
		return
	}
	var expiry *time.Time
	if claim != nil {
		expires, rotateErr := s.completeImportDraftMutation(r, claim, draft)
		if rotateErr == nil {
			claim = nil
			if !expires.IsZero() {
				expiry = &expires
			}
		} else {
			stripImportDraftAttestation(draft)
		}
	}
	s.recordAuditEvent(r, "script.import.settings", "success", "script", draft.Name, draft.ProjectID, map[string]any{"category": req.Category, "changes": len(req.Changes)})
	writeJSON(w, 200, ImportSettingsResponse{FidelityReport: report, Script: repairDisclosure(draft), ImportDraftExpiresAt: expiry, ValidationIssues: issues, Findings: findings, Readiness: readiness, SecretBindingRequirements: requirements})
}

func applyImportSettings(source *dsl.Script, category string, changes []ImportSettingsChange) (*dsl.Script, map[string]string) {
	errors := map[string]string{}
	raw, _ := json.Marshal(source)
	var wire map[string]any
	_ = decodeImportSettingJSON(raw, &wire)
	var original map[string]any
	_ = decodeImportSettingJSON(raw, &original)
	safeRaw, _ := json.Marshal(repairDisclosure(source))
	var safe map[string]any
	_ = decodeImportSettingJSON(safeRaw, &safe)
	maskedSourceRaw, _ := json.Marshal(maskScriptSecrets(&dsl.Script{Config: source.Config}))
	var maskedSource map[string]any
	_ = decodeImportSettingJSON(maskedSourceRaw, &maskedSource)
	seen := map[string]bool{}
	for _, change := range changes {
		key := change.Path
		path := strings.Split(change.Path, ".")
		var field *importSettingField
		var catalogMeta *dsl.ConfigFieldMeta
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
				if f.Path == change.Path && (category == "" || f.Category == category) {
					copy := f
					field = &copy
					break
				}
			}
			if meta, ok := dsl.ConfigFieldForPath(change.Path); ok && slices.Contains(meta.Scopes, "import") {
				metaCopy := meta
				catalogMeta = &metaCopy
				if field == nil && importCatalogFieldMatchesCategory(meta, category) {
					f := importSettingFieldFromCatalogMeta(meta, defaultImportCategoryForCatalogMeta(meta))
					if contract := importSettingFieldsByPath[meta.Path]; len(contract.Options) > 0 {
						f.Options = append([]string(nil), contract.Options...)
					}
					field = &f
				} else if field != nil && len(field.Options) == 0 {
					field.Options = append([]string(nil), meta.Options...)
				}
				if field != nil && isSignedConfigCatalogField(meta.Path) {
					field.Type = "signed"
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
		if catalogMeta != nil && catalogMeta.Type == dsl.ConfigFieldTypeInt && value != nil {
			number, ok := value.(json.Number)
			if !ok {
				errors[key] = "Enter a valid integer value"
				continue
			}
			iv, err := number.Int64()
			if err != nil {
				errors[key] = "Enter a valid integer value"
				continue
			}
			if catalogMeta.Path == "config.http.max_redirects" && iv < -1 {
				errors[key] = "Enter a valid integer value"
				continue
			}
		}
		// Compare the masking projections only at the leaves this change adds
		// or changes. An unchanged imported leaf is script-sourced, never a
		// secret, so re-sending it (alone or beside new entries) always passes,
		// even when a name-based projection masks it.
		projections := []map[string]any{safe}
		if change.Parameter == "" {
			projections = append(projections, maskedSource)
		}
		if importSettingOverwritesProtectedValue(value, importSettingValue(wire, path), path, wire, projections...) {
			errors[key] = "Protected values must be managed in Credentials"
			continue
		}
		// A secret-typed field must hold a {{secret.*}} reference only when the
		// user types a new value; the imported value is script-sourced and
		// re-sending it unchanged always passes.
		if catalogMeta != nil && catalogMeta.Secret && value != nil && !reflect.DeepEqual(value, importSettingValue(wire, path)) {
			if s, ok := value.(string); ok && strings.TrimSpace(s) != "" && !isPortableCredentialReference(s) {
				errors[key] = "Use Credentials for sensitive values"
				continue
			}
		}
		// The disclosure projections do not recognise every credential shape
		// (URL userinfo, token query parameters, JAAS/DSN assignments), on
		// contract and catalog-widened paths alike. Inspect the new value
		// itself; an unchanged imported leaf is script-sourced and never a
		// secret, so only new or changed string leaves are inspected.
		if change.Parameter == "" && importSettingValueCarriesCredential(value, importSettingValue(wire, path)) {
			errors[key] = "Use Credentials for sensitive values"
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
				if change.Parameter == "" && (strings.HasPrefix(issue, change.Path+" ") || strings.HasPrefix(issue, change.Path+":")) {
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
	maskedAfterRaw, _ := json.Marshal(maskScriptSecrets(&dsl.Script{Config: draft.Config}))
	var maskedAfter map[string]any
	_ = decodeImportSettingJSON(maskedAfterRaw, &maskedAfter)
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
		projections := []map[string]any{afterSafe}
		if change.Parameter == "" {
			projections = append(projections, maskedAfter)
		}
		if importSettingChangedLeafDiffers(importSettingValue(after, path), importSettingValue(original, path), path, after, projections...) {
			errors[key] = "Use Credentials for sensitive values"
		}
	}
	if len(errors) > 0 {
		return nil, errors
	}
	// TLS selection is an intentional script-wide migration. Resolve only TLS
	// gates, through the same operation as the protected guided-repair flow.
	if importSettingsSelectsTLS(changes) {
		findings, err := importreview.FindingsFromScript(&draft)
		if err != nil {
			return nil, map[string]string{"category": "Cannot inspect TLS requirements"}
		}
		for _, finding := range findings {
			if finding.Code != "legacy_tls_replacement" {
				continue
			}
			minimum, maximum := draft.Config.HTTP.TLS.MinVersion, draft.Config.HTTP.TLS.MaxVersion
			if minimum == "" {
				minimum = "1.2"
			}
			if maximum == "" {
				maximum = "1.3"
			}
			_, err := applyImportRepair(&draft, ImportRepairRequest{Finding: finding.Code, ActionPath: finding.ActionPath, Operation: ImportRepairOperation{Type: "select_tls", TLSMin: minimum, TLSMax: maximum}})
			if err != nil {
				return nil, map[string]string{"category": err.Error()}
			}
			break
		}
	}
	return &draft, errors
}

// A reset to defaults is not an explicit replacement for a gated source policy.
func importSettingsSelectsTLS(changes []ImportSettingsChange) bool {
	for _, change := range changes {
		if change.Parameter == "" && string(bytes.TrimSpace(change.Value)) != "null" && (change.Path == "config.http.tls.min_version" || change.Path == "config.http.tls.max_version") {
			return true
		}
	}
	return false
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
	case "int":
		number, ok := v.(json.Number)
		if !ok {
			return false
		}
		_, err := number.Int64()
		return err == nil
	case "signed", "float":
		number, ok := v.(json.Number)
		n, err := number.Float64()
		return ok && err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	case "text", "string":
		s, ok := v.(string)
		return ok && len(s) <= 65536
	case "select":
		s, ok := v.(string)
		if !ok {
			return false
		}
		// An empty selection is the unset value, like null: re-sending an
		// imported field that was never set must pass. The contract does not
		// list "" because the editor already offers its own Default entry.
		if s == "" {
			return true
		}
		if len(f.Options) == 0 {
			return len(s) <= 65536
		}
		for _, option := range f.Options {
			if s == option {
				return true
			}
		}
		return false
	case "lines", "string_list":
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
	case "key_value_map":
		m, ok := v.(map[string]any)
		if !ok || len(m) > 1000 {
			return false
		}
		for k, val := range m {
			s, ok := val.(string)
			if len(k) > 4096 || !ok || len(s) > 65536 {
				return false
			}
		}
		return true
	case "object_list":
		values, ok := v.([]any)
		if !ok || len(values) > 10000 {
			return false
		}
		for _, val := range values {
			if _, ok := val.(map[string]any); !ok {
				return false
			}
		}
		return true
	case "object":
		_, ok := v.(map[string]any)
		return ok
	}
	return false
}

// importSettingChangedLeaves lists, relative to the setting, the leaves of
// next that are new or changed against prior. A value is unchanged when it
// equals an imported value at the same place: map entries match the same key,
// and a list element matches any imported element of that list (so reordering,
// inserting or removing imported elements adds nothing). A map element of a
// list is not compared with the prior element at its index, which an insert,
// remove or reorder would shift; each of its leaves instead matches the same
// key of every imported element, as importSettingValueCarriesCredential does.
// So editing one field of a moved element (toggling enabled, renaming) leaves
// its other, imported fields unchanged. Removed entries are not listed.
func importSettingChangedLeaves(next, prior any) [][]string {
	var out [][]string
	var walk func(next any, pool *importSettingPriorPool, at []string)
	walk = func(next any, pool *importSettingPriorPool, at []string) {
		if pool.contains(next) {
			return
		}
		switch typed := next.(type) {
		case map[string]any:
			for key, item := range typed {
				walk(item, pool.field(key), append(slices.Clip(at), key))
			}
			return
		case []any:
			elements := pool.elements()
			for i, item := range typed {
				walk(item, elements, append(slices.Clip(at), fmt.Sprint(i)))
			}
			return
		}
		out = append(out, slices.Clone(at))
	}
	walk(next, &importSettingPriorPool{values: []any{prior}}, nil)
	return out
}

// importSettingChangedLeafDiffers reports whether, at any leaf next adds or
// changes against prior, the tree differs from one of its masking
// projections. path locates the setting in tree and the projections.
func importSettingChangedLeafDiffers(next, prior any, path []string, tree map[string]any, projections ...map[string]any) bool {
	for _, leaf := range importSettingChangedLeaves(next, prior) {
		full := append(slices.Clip(path), leaf...)
		want := importSettingValue(tree, full)
		for _, projection := range projections {
			if !reflect.DeepEqual(want, importSettingValue(projection, full)) {
				return true
			}
		}
	}
	return false
}

// importSettingOverwritesProtectedValue reports whether next, saved over
// prior at path in tree, overwrites a value that one of the masking
// projections hides. Map entries are compared by key against the prior tree.
//
// A list element has no stable identity: a value at index i need not replace
// the prior element at index i, because inserting, removing or reordering
// elements shifts every index after it. So a new or changed list element is
// never compared with whatever prior element shares its index (an unchanged
// imported element moved by an insert would then read as overwritten). Only
// the masking placeholder is refused there, since an element carrying it would
// write the mask over the credential it hid. The value the element itself
// adds is checked after the change, against the projections of the new tree.
func importSettingOverwritesProtectedValue(next, prior any, path []string, tree map[string]any, projections ...map[string]any) bool {
	var walk func(next, prior any, at []string) bool
	walk = func(next, prior any, at []string) bool {
		if reflect.DeepEqual(next, prior) {
			return false
		}
		switch typed := next.(type) {
		case map[string]any:
			priorMap, _ := prior.(map[string]any)
			for key, item := range typed {
				if walk(item, priorMap[key], append(slices.Clip(at), key)) {
					return true
				}
			}
			return false
		case []any:
			if priorList, ok := prior.([]any); ok || prior == nil {
				pool := &importSettingPriorPool{values: priorList}
				for _, item := range typed {
					if !pool.contains(item) && importSettingCarriesMaskPlaceholder(item) {
						return true
					}
				}
				return false
			}
		}
		full := append(slices.Clip(path), at...)
		want := importSettingValue(tree, full)
		for _, projection := range projections {
			if !reflect.DeepEqual(want, importSettingValue(projection, full)) {
				return true
			}
		}
		return false
	}
	return walk(next, prior, nil)
}

// importSettingCarriesMaskPlaceholder reports whether any string leaf of v
// carries the masking placeholder a protected value is displayed as.
func importSettingCarriesMaskPlaceholder(v any) bool {
	switch typed := v.(type) {
	case string:
		return strings.Contains(typed, credential.Redacted)
	case []any:
		for _, item := range typed {
			if importSettingCarriesMaskPlaceholder(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if importSettingCarriesMaskPlaceholder(item) {
				return true
			}
		}
	}
	return false
}

func isPortableCredentialReference(s string) bool {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{{") || !strings.HasSuffix(trimmed, "}}") {
		return false
	}
	probe, err := scriptdisclosure.Project(&dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{Password: s}}}})
	return err == nil && probe != nil && probe.Config.HTTP.Auth.Password == s
}

// importSettingCredentialAssignment matches name=value pairs inside free text:
// JAAS options (password="..."), connection strings (SharedAccessKey=...;) and
// DSN keywords (password=...).
var importSettingCredentialAssignment = regexp.MustCompile(`([A-Za-z0-9_.\-]+)\s*=\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|([^\s;,&"']+))`)

// importSettingURLScheme locates a URL scheme separator anywhere in a token,
// so a quoted or assigned URL (url="https://...") is still found.
var importSettingURLScheme = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.\-]*://`)

// importSettingValueCarriesCredential reports whether a typed import setting
// value embeds credential material in a string leaf that is new relative to
// the current (imported) value: URL userinfo, a credential-named or Azure SAS
// signature (sig) query parameter or assignment, or a credential-named inline
// assignment. Map leaves compare by key; list elements compare against every
// imported element, and a map element's leaf against the same key of every
// imported map element, so reordering, inserting or removing elements does
// not re-inspect an unchanged imported value. Unchanged leaves are
// script-sourced and never secrets. Only exact portable references accepted
// by isPortableCredentialReference ({{secret.NAME}}) are exempt.
//
// Each value is matched against the imported values through a hashed index
// built once per level, and each distinct string is scanned at most once, so
// the work stays linear in the request and imported sizes.
func importSettingValueCarriesCredential(value, current any) bool {
	return importSettingCredentialCheck{scanned: map[string]bool{}}.carries(value, &importSettingPriorPool{values: []any{current}})
}

type importSettingCredentialCheck struct {
	scanned map[string]bool
}

func (c importSettingCredentialCheck) carries(value any, pool *importSettingPriorPool) bool {
	if value == nil || pool.contains(value) {
		return false
	}
	switch typed := value.(type) {
	case string:
		found, ok := c.scanned[typed]
		if !ok {
			found = importSettingTextCarriesCredential(typed)
			c.scanned[typed] = found
		}
		return found
	case []any:
		elements := pool.elements()
		for _, item := range typed {
			if c.carries(item, elements) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if c.carries(item, pool.field(key)) {
				return true
			}
		}
	}
	return false
}

// importSettingPriorPool holds the imported values a new value at one place
// may equal. Its index and child pools are built lazily, once.
type importSettingPriorPool struct {
	values   []any
	index    map[string]struct{}
	children *importSettingPriorPool
	fields   map[string]*importSettingPriorPool
}

func importSettingCanonical(v any) (string, bool) {
	raw, err := json.Marshal(v)
	return string(raw), err == nil
}

func (p *importSettingPriorPool) contains(v any) bool {
	if len(p.values) == 0 {
		return false
	}
	if p.index == nil {
		p.index = make(map[string]struct{}, len(p.values))
		for _, prior := range p.values {
			if key, ok := importSettingCanonical(prior); ok {
				p.index[key] = struct{}{}
			}
		}
	}
	key, ok := importSettingCanonical(v)
	if !ok {
		return false
	}
	_, found := p.index[key]
	return found
}

// elements pools the elements of every imported list in p.
func (p *importSettingPriorPool) elements() *importSettingPriorPool {
	if p.children == nil {
		p.children = &importSettingPriorPool{}
		for _, prior := range p.values {
			if list, ok := prior.([]any); ok {
				p.children.values = append(p.children.values, list...)
			}
		}
	}
	return p.children
}

// field pools the value under key of every imported map in p.
func (p *importSettingPriorPool) field(key string) *importSettingPriorPool {
	if child, ok := p.fields[key]; ok {
		return child
	}
	if p.fields == nil {
		p.fields = map[string]*importSettingPriorPool{}
	}
	child := &importSettingPriorPool{}
	for _, prior := range p.values {
		if m, ok := prior.(map[string]any); ok {
			if v, present := m[key]; present {
				child.values = append(child.values, v)
			}
		}
	}
	p.fields[key] = child
	return child
}

// importSettingTextCarriesCredential inspects free text without relying on a
// URL parser, so malformed URLs and scheme-less authorities
// (user:pass@host, DSNs) are still checked.
func importSettingTextCarriesCredential(text string) bool {
	for _, token := range strings.Fields(text) {
		if importSettingTokenCarriesUserinfo(importSettingTrimToken(token)) {
			return true
		}
	}
	for _, match := range importSettingCredentialAssignment.FindAllStringSubmatch(text, -1) {
		if importSettingAssignmentIsCredential(match[1], match[2]+match[3]+match[4]) {
			return true
		}
	}
	return importSettingNestedPairsCarryCredential(text)
}

func importSettingAssignmentIsCredential(name, value string) bool {
	return strings.EqualFold(name, "sig") && importSettingCredentialMaterial(name, value) || importSettingCredentialPair(name, value)
}

// importSettingNestedPairsCarryCredential finds the name=value pairs an
// unquoted assignment value would otherwise swallow (a=b?sig=...,
// next=https://h/c?sig=..., url=...?api_key=...). The text is split into
// segments at whitespace, quotes and the query, fragment and parameter
// delimiters, and every '=' in a segment starts a pair whose name is the run
// of name characters before it and whose value is the rest of the segment.
func importSettingNestedPairsCarryCredential(text string) bool {
	segments := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '?', '#', '&', ';', ',', '"', '\'', ' ', '\t', '\n', '\r', '\v', '\f':
			return true
		}
		return false
	})
	for _, segment := range segments {
		for i := 0; i < len(segment); i++ {
			if segment[i] != '=' {
				continue
			}
			nameStart := i
			for nameStart > 0 && importSettingNameByte(segment[nameStart-1]) {
				nameStart--
			}
			name := segment[nameStart:i]
			if name == "" || !strings.EqualFold(name, "sig") && !credential.SensitiveName(name) {
				continue
			}
			if importSettingAssignmentIsCredential(name, importSettingTrimToken(segment[i+1:])) {
				return true
			}
		}
	}
	return false
}

func importSettingNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '.' || b == '-'
}

// importSettingTokenCarriesUserinfo reports literal userinfo in a token. After
// a scheme (https://...) a literal password, or a lone user part, counts;
// without one only a user:password@ form counts, so a bare email address or image digest does
// not.
func importSettingTokenCarriesUserinfo(token string) bool {
	schemes := importSettingURLScheme.FindAllStringIndex(token, -1)
	if len(schemes) == 0 {
		if strings.HasPrefix(strings.ToLower(token), "mailto:") {
			return false
		}
		_, password, hasPassword, ok := importSettingAuthorityUserinfo(token)
		return ok && hasPassword && importSettingUserinfoPartIsMaterial(password)
	}
	for _, loc := range schemes {
		user, password, hasPassword, ok := importSettingAuthorityUserinfo(token[loc[1]:])
		if !ok {
			continue
		}
		// A user name beside a password is not secret; a lone userinfo part
		// (https://TOKEN@host) is the credential itself.
		if hasPassword && importSettingUserinfoPartIsMaterial(password) || !hasPassword && importSettingUserinfoPartIsMaterial(user) {
			return true
		}
	}
	return false
}

// importSettingAuthorityUserinfo splits the userinfo of the authority that
// starts rest. The authority ends at the first '/', '?' or '#'; userinfo is
// everything before its last '@'.
func importSettingAuthorityUserinfo(rest string) (user, password string, hasPassword, ok bool) {
	authority := rest
	if i := strings.IndexAny(authority, "/?#"); i >= 0 {
		authority = authority[:i]
	}
	at := strings.LastIndex(authority, "@")
	if at <= 0 {
		return "", "", false, false
	}
	userinfo := authority[:at]
	user, password, hasPassword = strings.Cut(userinfo, ":")
	return user, password, hasPassword, true
}

// importSettingUserinfoPartIsMaterial reports whether a userinfo part is
// literal material: non-empty and not an exact portable reference, raw or
// percent-decoded.
func importSettingUserinfoPartIsMaterial(part string) bool {
	if part == "" {
		return false
	}
	return !importSettingIsExactReference(part)
}

// importSettingIsExactReference accepts only the exact portable credential
// reference grammar, checking the raw text and, when it decodes, its
// percent-decoded form (a browser may have encoded the braces).
func importSettingIsExactReference(value string) bool {
	if isPortableCredentialReference(value) {
		return true
	}
	if decoded, err := url.PathUnescape(value); err == nil && decoded != value {
		return isPortableCredentialReference(decoded)
	}
	return false
}

// importSettingTrimToken strips surrounding quotes and punctuation from a
// free-text token, and only unbalanced braces, so a trailing {{secret.X}}
// reference stays whole.
func importSettingTrimToken(token string) string {
	token = strings.Trim(token, `"'(),;[]`)
	open, closing := strings.Count(token, "{"), strings.Count(token, "}")
	for closing > open && strings.HasSuffix(token, "}") {
		token = token[:len(token)-1]
		closing--
	}
	for open > closing && strings.HasPrefix(token, "{") {
		token = token[1:]
		open--
	}
	return strings.Trim(token, `"'(),;[]`)
}

// importSettingCredentialMaterial reports whether a value under a name already
// known to carry a credential is literal material rather than empty or an
// exact portable reference.
func importSettingCredentialMaterial(name, value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || importSettingIsExactReference(trimmed) {
		return false
	}
	return credential.MatchableNamedValue(name, trimmed)
}

func importSettingCredentialPair(name, value string) bool {
	return credential.SensitiveName(name) && !credential.NonCredentialName(name) && importSettingCredentialMaterial(name, value)
}

func importSettingFieldFromCatalogMeta(meta dsl.ConfigFieldMeta, category string) importSettingField {
	fieldType := meta.Type
	switch meta.Type {
	case "string":
		fieldType = "text"
	case "int", "float":
		if isSignedConfigCatalogField(meta.Path) {
			fieldType = "signed"
		} else {
			fieldType = "number"
		}
	case "string_list":
		fieldType = "lines"
	}
	return importSettingField{
		Category: category,
		Group:    meta.Group,
		Subgroup: meta.Subgroup,
		Path:     meta.Path,
		Label:    meta.Label,
		Type:     fieldType,
		Unit:     meta.Unit,
		Options:  append([]string(nil), meta.Options...),
		Secret:   meta.Secret,
	}
}

func isSignedConfigCatalogField(path string) bool {
	switch path {
	case "config.pacing.seed",
		"config.http.max_redirects",
		"config.browser.geolocation.latitude",
		"config.browser.geolocation.longitude":
		return true
	default:
		return false
	}
}

func defaultImportCategoryForCatalogMeta(meta dsl.ConfigFieldMeta) string {
	switch {
	case strings.HasPrefix(meta.Path, "config.soap."):
		return "SOAP"
	case strings.HasPrefix(meta.Path, "config.web_runtime."):
		return "HTTP"
	case strings.HasPrefix(meta.Path, "config.http.proxy."):
		return "Proxy"
	case strings.HasPrefix(meta.Path, "config.http.") || meta.Path == "config.base_url" || meta.Path == "config.headers" || meta.Path == "config.cookies":
		return "HTTP"
	case strings.HasPrefix(meta.Path, "config.run_logic."):
		return "Run logic"
	case strings.HasPrefix(meta.Path, "config.pacing."):
		return "Pacing"
	case strings.HasPrefix(meta.Path, "config.think_time."):
		return "Think time"
	case strings.HasPrefix(meta.Path, "config.log."):
		return "Logging"
	case strings.HasPrefix(meta.Path, "config.error_handling.") || strings.HasPrefix(meta.Path, "config.transaction_granularity."):
		return "Error handling"
	case strings.HasPrefix(meta.Path, "config.results.") || strings.HasPrefix(meta.Path, "config.replay_diagnostics."):
		return "Diagnostics"
	case strings.HasPrefix(meta.Path, "config.browser_emulation."):
		return "Browser emulation"
	case strings.HasPrefix(meta.Path, "config.browser."):
		return "Browser"
	case strings.HasPrefix(meta.Path, "config.websocket."):
		return "WebSocket"
	case strings.HasPrefix(meta.Path, "config.kafka."):
		return "Kafka"
	case strings.HasPrefix(meta.Path, "config.jms."):
		return "JMS"
	case strings.HasPrefix(meta.Path, "config.eventhubs."):
		return "Event Hubs"
	case strings.HasPrefix(meta.Path, "config.grpc."):
		return "gRPC"
	case strings.HasPrefix(meta.Path, "config.database."):
		return "Database"
	case strings.HasPrefix(meta.Path, "config.network."):
		return "Network"
	case strings.HasPrefix(meta.Path, "config.appium."):
		return "Appium"
	case strings.HasPrefix(meta.Path, "config.java_vuser."):
		return "Java VUser"
	default:
		return meta.Group
	}
}

func importCatalogFieldMatchesCategory(meta dsl.ConfigFieldMeta, category string) bool {
	if category == "" || category == "all" || category == "config" {
		return true
	}
	if category == meta.Group || category == meta.Subgroup || category == meta.Group+":"+meta.Subgroup {
		return true
	}
	defaultCat := defaultImportCategoryForCatalogMeta(meta)
	if strings.EqualFold(category, defaultCat) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "http", "internet_protocol:preferences":
		return strings.HasPrefix(meta.Path, "config.http.") || strings.HasPrefix(meta.Path, "config.web_runtime.") || meta.Path == "config.base_url" || meta.Path == "config.headers" || meta.Path == "config.cookies" || strings.HasPrefix(meta.Path, "config.bandwidth.") || strings.HasPrefix(meta.Path, "config.streaming.")
	case "soap":
		return strings.HasPrefix(meta.Path, "config.soap.") || meta.Subgroup == "soap"
	case "web_runtime", "web runtime", "internet_protocol:download_filters", "internet_protocol:content_check":
		return strings.HasPrefix(meta.Path, "config.web_runtime.")
	case "tls", "http_tls":
		return strings.HasPrefix(meta.Path, "config.http.tls.")
	case "proxy", "http_proxy", "internet_protocol:proxy":
		return strings.HasPrefix(meta.Path, "config.http.proxy.") || meta.Path == "config.http.proxy_mode"
	case "auth", "http_auth":
		return strings.HasPrefix(meta.Path, "config.http.auth.")
	case "quic", "http_quic":
		return strings.HasPrefix(meta.Path, "config.http.quic.")
	case "base_url", "target", "target_identity":
		return meta.Path == "config.base_url" || meta.Group == dsl.ConfigGroupTargetIdentity
	case "run_logic", "run logic":
		return strings.HasPrefix(meta.Path, "config.run_logic.") || meta.Subgroup == "iterations"
	case "pacing":
		return strings.HasPrefix(meta.Path, "config.pacing.") || meta.Subgroup == "pacing"
	case "think_time", "think time":
		return strings.HasPrefix(meta.Path, "config.think_time.") || meta.Group == dsl.ConfigGroupThinkTime
	case "log", "logging":
		return strings.HasPrefix(meta.Path, "config.log.") || meta.Subgroup == "logging"
	case "error_handling", "error handling", "miscellaneous":
		return strings.HasPrefix(meta.Path, "config.error_handling.") || strings.HasPrefix(meta.Path, "config.transaction_granularity.")
	case "diagnostics":
		return strings.HasPrefix(meta.Path, "config.results.") || strings.HasPrefix(meta.Path, "config.replay_diagnostics.")
	case "browser_emulation", "browser emulation":
		return strings.HasPrefix(meta.Path, "config.browser_emulation.") || strings.HasPrefix(meta.Path, "config.browser.")
	case "browser":
		return strings.HasPrefix(meta.Path, "config.browser.") || strings.HasPrefix(meta.Path, "config.browser_emulation.")
	case "network:speed_simulation", "bandwidth":
		return strings.HasPrefix(meta.Path, "config.bandwidth.") || meta.Subgroup == "bandwidth"
	case "streaming":
		return strings.HasPrefix(meta.Path, "config.streaming.") || meta.Subgroup == "streaming_media"
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
		if value == nil {
			return
		}
		m[path[0]] = map[string]any{}
	}
	setImportSettingValue(m[path[0]], path[1:], value)
	if sub, ok := m[path[0]].(map[string]any); ok && len(sub) == 0 {
		delete(m, path[0])
	}
}

var importParameterSettingFields = map[string]importSettingField{
	"values": {Type: "lines"}, "data_source": {Type: "text"}, "column": {Type: "text"},
	"selection":     {Type: "select", Options: []string{"sequential", "random", "unique"}},
	"update":        {Type: "select", Options: []string{"once", "each_iteration", "each_occurrence"}},
	"on_exhaustion": {Type: "select", Options: []string{"wrap", "empty", "error", "last"}},
	"same_line_as":  {Type: "text"}, "start_row": {Type: "number"}, "block_size": {Type: "number"}, "auto_allocate": {Type: "boolean"}, "value_for_each_vuser": {Type: "boolean"},
	"format": {Type: "text"}, "timezone": {Type: "text"}, "offset_seconds": {Type: "signed"}, "min": {Type: "signed"}, "max": {Type: "signed"}, "start": {Type: "signed"}, "end": {Type: "signed"}, "step": {Type: "signed"}, "length": {Type: "number"}, "charset": {Type: "text"}, "pad_width": {Type: "number"}, "pad_char": {Type: "text"}, "seed": {Type: "signed"},
}
