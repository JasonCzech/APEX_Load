package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	defaultSecretUsagePageSize = 50
	maxSecretUsagePageSize     = 200
)

var exactScriptSecretReferencePattern = regexp.MustCompile(`\{\{\s*secret\.\s*(` + engine.LogicalSecretNameInputPattern + `)\s*\}\}`)

type scriptSecretReferenceLocation struct {
	Name string
	Path string
}

type scriptSecretBindingDetail struct {
	Name            string   `json:"name"`
	Configured      bool     `json:"configured"`
	Provider        string   `json:"provider,omitempty"`
	Writable        bool     `json:"writable,omitempty"`
	OccurrenceCount int      `json:"occurrence_count"`
	Locations       []string `json:"locations"`
}

type projectSecretConsumer struct {
	ResourceType   string `json:"resource_type,omitempty"`
	ResourceID     string `json:"resource_id,omitempty"`
	ScriptID       string `json:"script_id"`
	ScriptName     string `json:"script_name"`
	ReferenceCount int    `json:"reference_count"`
}

type projectSecretUsage struct {
	ProjectID      string                  `json:"project_id"`
	Name           string                  `json:"name"`
	UsageRevision  string                  `json:"usage_revision"`
	ConsumerCount  int                     `json:"consumer_count"`
	ReferenceCount int                     `json:"reference_count"`
	Consumers      []projectSecretConsumer `json:"consumers"`
}

type projectSecretExpectedUsage struct {
	UsageRevision     string `json:"usage_revision"`
	ExclusiveScriptID string `json:"exclusive_script_id,omitempty"`
}

type rebindScriptSecretReferenceRequest struct {
	Script  dsl.Script `json:"script"`
	OldName string     `json:"old_name"`
	NewName string     `json:"new_name"`
}

func (s *Server) acquireSecretReferenceMutation(projectID string) (func(), error) {
	if s == nil || s.secretReferenceMutations == nil {
		return func() {}, nil
	}
	return s.secretReferenceMutations.acquireDurable(projectID)
}

func (s *Server) projectSecretUsage(projectID, name string) (projectSecretUsage, error) {
	projectID = normalizeProjectID(projectID)
	name, _ = normalizeProjectSecretName(name)
	usage := projectSecretUsage{ProjectID: projectID, Name: name, Consumers: []projectSecretConsumer{}}
	if s == nil || s.runner == nil {
		usage.UsageRevision = projectSecretUsageRevision(projectID, name, nil)
		return usage, nil
	}
	scripts := make([]*dsl.Script, 0)
	if s.runner.scriptStore != nil {
		listed, err := s.runner.scriptStore.ListScripts(projectID, nil)
		if err != nil {
			return usage, fmt.Errorf("list authoritative script references: %w", err)
		}
		pending, err := s.runner.pendingImportPublicationSnapshot()
		if err != nil {
			return usage, fmt.Errorf("read import publication state: %w", err)
		}
		for _, script := range listed {
			if script == nil || isCIScriptSnapshot(script) || importPublicationContains(pending, "script", script.ID) {
				continue
			}
			scripts = append(scripts, script)
		}
	} else {
		for _, stored := range s.runner.ListScriptsFiltered(projectID, nil) {
			if stored != nil && stored.Script != nil {
				scripts = append(scripts, stored.Script)
			}
		}
	}
	return s.appendAppDynamicsSecretUsage(projectSecretUsageFromScripts(projectID, name, scripts))
}

func projectSecretUsageFromScripts(projectID, name string, scripts []*dsl.Script) projectSecretUsage {
	projectID = normalizeProjectID(projectID)
	name, _ = normalizeProjectSecretName(name)
	usage := projectSecretUsage{ProjectID: projectID, Name: name, Consumers: []projectSecretConsumer{}}
	for _, script := range scripts {
		if normalizeProjectID(script.ProjectID) != projectID {
			continue
		}
		count := 0
		for _, location := range inspectScriptSecretReferenceLocations(script) {
			if location.Name == name {
				count++
			}
		}
		if count == 0 {
			continue
		}
		usage.Consumers = append(usage.Consumers, projectSecretConsumer{
			ScriptID: script.ID, ScriptName: disclosureSafeScriptName(script.Name, script), ReferenceCount: count,
		})
		usage.ReferenceCount += count
	}
	sort.Slice(usage.Consumers, func(i, j int) bool {
		if usage.Consumers[i].ScriptName == usage.Consumers[j].ScriptName {
			return usage.Consumers[i].ScriptID < usage.Consumers[j].ScriptID
		}
		return usage.Consumers[i].ScriptName < usage.Consumers[j].ScriptName
	})
	usage.ConsumerCount = len(usage.Consumers)
	usage.UsageRevision = projectSecretUsageRevision(projectID, name, usage.Consumers)
	return usage
}

func projectSecretUsageRevision(projectID, name string, consumers []projectSecretConsumer) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", normalizeProjectID(projectID), name)
	for _, consumer := range consumers {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%d\x00", consumer.ScriptID, consumer.ScriptName, consumer.ReferenceCount)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *Server) handleProjectSecretUsage(w http.ResponseWriter, r *http.Request) {
	projectID := normalizeProjectID(r.PathValue("id"))
	if !s.ensureSecretProjectExists(w, projectID) {
		return
	}
	name, err := normalizeProjectSecretName(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, offset, err := parseSecretUsagePagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	usage, err := s.projectSecretUsage(projectID, name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "project secret usage is unavailable")
		return
	}
	start := offset
	if start > len(usage.Consumers) {
		start = len(usage.Consumers)
	}
	end := start + limit
	if end > len(usage.Consumers) {
		end = len(usage.Consumers)
	}
	page := append([]projectSecretConsumer(nil), usage.Consumers[start:end]...)
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id": projectID, "name": name, "usage_revision": usage.UsageRevision,
		"consumer_count": usage.ConsumerCount, "reference_count": usage.ReferenceCount,
		"consumers": page, "limit": limit, "offset": offset, "has_more": end < len(usage.Consumers),
	})
}

func parseSecretUsagePagination(r *http.Request) (int, int, error) {
	limit := defaultSecretUsagePageSize
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > maxSecretUsagePageSize {
			return 0, 0, fmt.Errorf("limit must be between 1 and %d", maxSecretUsagePageSize)
		}
		limit = parsed
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, fmt.Errorf("offset must be a non-negative integer")
		}
		offset = parsed
	}
	return limit, offset, nil
}

func scriptSecretBindingDetails(script *dsl.Script, statuses []projectSecretStatus) []scriptSecretBindingDetail {
	byName := make(map[string]*scriptSecretBindingDetail, len(statuses))
	for _, status := range statuses {
		byName[status.Name] = &scriptSecretBindingDetail{
			Name: status.Name, Configured: status.Configured, Provider: status.Provider, Writable: status.Writable, Locations: []string{},
		}
	}
	locationsByName := make(map[string]map[string]struct{})
	for _, location := range inspectScriptSecretReferenceLocations(script) {
		detail := byName[location.Name]
		if detail == nil {
			continue
		}
		detail.OccurrenceCount++
		if locationsByName[location.Name] == nil {
			locationsByName[location.Name] = make(map[string]struct{})
		}
		locationsByName[location.Name][location.Path] = struct{}{}
	}
	result := make([]scriptSecretBindingDetail, 0, len(byName))
	for _, status := range statuses {
		detail := byName[status.Name]
		for path := range locationsByName[status.Name] {
			detail.Locations = append(detail.Locations, path)
		}
		sort.Strings(detail.Locations)
		result = append(result, *detail)
	}
	return result
}

func inspectScriptSecretReferenceLocations(script *dsl.Script) []scriptSecretReferenceLocation {
	if script == nil {
		return nil
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil
	}
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil
	}
	locations := make([]scriptSecretReferenceLocation, 0)
	walkScriptSecretJSON(root, "", true, false, func(path, rawName string) string {
		name, err := normalizeProjectSecretName(rawName)
		if err == nil {
			locations = append(locations, scriptSecretReferenceLocation{Name: name, Path: path})
		}
		return rawName
	})
	return locations
}

func transformScriptSecretReferences(script *dsl.Script, oldName, newName string) (*dsl.Script, []string, int, error) {
	if script == nil {
		return nil, nil, 0, fmt.Errorf("script is required")
	}
	oldName, err := normalizeProjectSecretName(oldName)
	if err != nil {
		return nil, nil, 0, err
	}
	newName, err = normalizeProjectSecretName(newName)
	if err != nil {
		return nil, nil, 0, err
	}
	if oldName == newName {
		return nil, nil, 0, fmt.Errorf("new_name must differ from old_name")
	}
	if strings.HasPrefix(oldName, runtimeOverrideSecretPrefix) || strings.HasPrefix(newName, runtimeOverrideSecretPrefix) {
		return nil, nil, 0, fmt.Errorf("secret rebind uses a reserved internal secret namespace")
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("encode script draft")
	}
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, nil, 0, fmt.Errorf("decode script draft")
	}
	changedLocations := make(map[string]struct{})
	changedCount := 0
	walkScriptSecretJSON(root, "", true, false, func(path, rawName string) string {
		normalized, normalizeErr := normalizeProjectSecretName(rawName)
		if normalizeErr != nil || normalized != oldName {
			return rawName
		}
		changedLocations[path] = struct{}{}
		changedCount++
		return newName
	})
	if changedCount == 0 {
		return nil, nil, 0, fmt.Errorf("script does not reference secret %q", oldName)
	}
	transformedPayload, err := json.Marshal(root)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("encode transformed script draft")
	}
	var transformed dsl.Script
	if err := json.Unmarshal(transformedPayload, &transformed); err != nil {
		return nil, nil, 0, fmt.Errorf("decode transformed script draft")
	}
	locations := make([]string, 0, len(changedLocations))
	for path := range changedLocations {
		locations = append(locations, path)
	}
	sort.Strings(locations)
	return &transformed, locations, changedCount, nil
}

// walkScriptSecretJSON traverses executable script fields. The callback sees
// logical names and sanitized JSON-pointer locations only; it never receives a
// resolved value. Returning a different name rewrites that exact reference.
func walkScriptSecretJSON(value any, path string, root, dedicatedField bool, visit func(path, name string) string) {
	switch typed := value.(type) {
	case map[string]any:
		if isNonExecutableScriptSecretReviewMap(typed) {
			return
		}
		encodedBody := false
		if body, ok := typed["body"].(string); ok {
			encoding, _ := typed["body_encoding"].(string)
			if decoded, encoded, err := dsl.DecodeBodyEncoding(body, encoding, 0); encoded && err == nil {
				encodedBody = true
				bodyPath := path + "/body"
				transformed := replaceSecretReferencesInString(string(decoded), bodyPath, visit)
				if transformed != string(decoded) {
					typed["body"] = base64.StdEncoding.EncodeToString([]byte(transformed))
				}
			}
		}
		for key, child := range typed {
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
			if root && isNonExecutableRootSecretField(normalizedKey) {
				continue
			}
			if encodedBody && normalizedKey == "body" {
				continue
			}
			if path == "/config/java_vuser" && normalizedKey == "additional_vm_parameters" {
				continue
			}
			_, childDedicated := map[string]struct{}{
				"client_secret_ref": {}, "dsn_secret": {}, "sasl_password_secret": {}, "connection_string_secret": {}, "ca_cert_secret": {},
			}[normalizedKey]
			childPath := path + "/" + escapeJSONPointerToken(key)
			if text, ok := child.(string); ok {
				updated := replaceSecretReferencesInString(text, childPath, visit)
				if childDedicated && updated == text && !strings.Contains(text, "{{") {
					if _, err := normalizeProjectSecretName(text); err == nil {
						updated = visit(childPath, text)
					}
				}
				typed[key] = updated
				continue
			}
			walkScriptSecretJSON(child, childPath, false, childDedicated, visit)
		}
	case []any:
		for index, child := range typed {
			childPath := path + "/" + strconv.Itoa(index)
			if text, ok := child.(string); ok {
				typed[index] = replaceSecretReferencesInString(text, childPath, visit)
				continue
			}
			walkScriptSecretJSON(child, childPath, false, dedicatedField, visit)
		}
	case string:
		_ = replaceSecretReferencesInString(typed, path, visit)
	}
}

func replaceSecretReferencesInString(value, path string, visit func(path, name string) string) string {
	return exactScriptSecretReferencePattern.ReplaceAllStringFunc(value, func(reference string) string {
		match := exactScriptSecretReferencePattern.FindStringSubmatch(reference)
		if len(match) != 2 {
			return reference
		}
		updated := visit(path, match[1])
		if updated == match[1] {
			return reference
		}
		return "{{secret." + updated + "}}"
	})
}

func isNonExecutableRootSecretField(key string) bool {
	switch key {
	case "id", "ref_key", "name", "version", "protocol", "project_id", "folder_id", "generated_by", "generated_from", "metadata", "created_at", "updated_at":
		return true
	default:
		return false
	}
}

func isNonExecutableScriptSecretReviewMap(value map[string]any) bool {
	if _, hasName := value["name"]; !hasName {
		return false
	}
	if _, hasTarget := value["target"]; !hasTarget {
		return false
	}
	actionType, _ := value["type"].(string)
	switch strings.ToLower(strings.TrimSpace(actionType)) {
	case "source_review", "review_required":
		return true
	default:
		return false
	}
}

func escapeJSONPointerToken(token string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(token)
}

type ScriptSecretRebindAuthoringResponse struct {
	Script           ScriptAuthoringDocument `json:"script"`
	OldName          string                  `json:"old_name"`
	NewName          string                  `json:"new_name"`
	ChangedLocations []string                `json:"changed_locations"`
	ChangedCount     int                     `json:"changed_count"`
}
type ScriptSecretRebindLegacyResponse struct {
	Script           *dsl.Script `json:"script"`
	OldName          string      `json:"old_name"`
	NewName          string      `json:"new_name"`
	ChangedLocations []string    `json:"changed_locations"`
	ChangedCount     int         `json:"changed_count"`
}

func (s *Server) handleRebindScriptSecretReference(w http.ResponseWriter, r *http.Request) {
	var req rebindScriptSecretReferenceRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	stored, supported, admitted := s.loadScriptAuthoringRequest(w, r, strings.TrimSpace(req.Script.ID))
	if !admitted {
		return
	}
	// This endpoint only transforms the caller's draft. It neither saves nor
	// mints a validator for the transformed document. New authoring clients use
	// their exact observed content validator; enriched timestamps stay a legacy
	// compatibility gate only when no authoring validator was supplied.
	revision, present, err := parseScriptAuthoringIfMatch(r.Header)
	if err != nil {
		writeError(w, http.StatusBadRequest, "If-Match must contain one strong script authoring v1 tag")
		return
	}
	if present {
		if !supported {
			writeError(w, http.StatusServiceUnavailable, "durable script content revisions unavailable")
			return
		}
		if revision != stored.ContentRevision {
			writeError(w, http.StatusPreconditionFailed, "script authored content changed; reload before rebinding secrets")
			return
		}
		if req.Script.ID != stored.ID {
			writeError(w, http.StatusBadRequest, "draft ID must match the observed authoring document")
			return
		}
	}
	if req.Script.ProjectID != stored.ProjectID {
		writeError(w, http.StatusBadRequest, "script project does not match the saved script")
		return
	}
	if !present && (req.Script.UpdatedAt.IsZero() || !req.Script.UpdatedAt.Equal(stored.Script.UpdatedAt)) {
		writeError(w, http.StatusConflict, "script changed since this draft was loaded; refresh before rebinding secrets")
		return
	}
	oldName, err := normalizeProjectSecretName(req.OldName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	newName, err := normalizeProjectSecretName(req.NewName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	transformed, locations, count, err := transformScriptSecretReferences(&req.Script, oldName, newName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if present {
		writeJSON(w, http.StatusOK, ScriptSecretRebindAuthoringResponse{Script: scriptAuthoringDocumentFromScript(transformed), OldName: oldName, NewName: newName, ChangedLocations: locations, ChangedCount: count})
		return
	}
	writeJSON(w, http.StatusOK, ScriptSecretRebindLegacyResponse{Script: transformed, OldName: oldName, NewName: newName, ChangedLocations: locations, ChangedCount: count})
}

func validateExpectedProjectSecretUsage(usage projectSecretUsage, expected *projectSecretExpectedUsage) error {
	if expected == nil {
		return nil
	}
	if strings.TrimSpace(expected.UsageRevision) == "" || expected.UsageRevision != usage.UsageRevision {
		return fmt.Errorf("secret consumers changed; refresh usage before creating the value")
	}
	if scriptID := strings.TrimSpace(expected.ExclusiveScriptID); scriptID != "" {
		if usage.ConsumerCount != 1 || usage.Consumers[0].ScriptID != scriptID || usage.Consumers[0].ResourceType != "" && usage.Consumers[0].ResourceType != "script" {
			return fmt.Errorf("secret is no longer dedicated to the expected script")
		}
	}
	return nil
}
