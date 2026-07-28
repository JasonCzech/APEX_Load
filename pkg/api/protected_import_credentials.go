package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

const optionalCredentialMigrationUnavailableWarning = "Optional Project Secret migration is unavailable; the imported destination retains its credential values and remains runnable."

const protectedImportCredentialMetadataKey = "protected_credential_requirements_v1"

type protectedImportCredentialLocation struct {
	Path       string `json:"path"`
	Occurrence int    `json:"occurrence,omitempty"`
	Encoding   string `json:"encoding,omitempty"`
}

// protectedImportCredentialDescriptor is the disclosure-safe portion of a
// shared credential requirement. It is allowed in the editable inspection
// script so the existing SecretBindingReview apply route can validate stable
// requirement IDs without receiving a source value.
type protectedImportCredentialDescriptor struct {
	ID            string                              `json:"id"`
	Label         string                              `json:"label"`
	Context       string                              `json:"context"`
	SuggestedName string                              `json:"suggested_name"`
	Replacement   string                              `json:"replacement"`
	Locations     []protectedImportCredentialLocation `json:"locations"`
}

func attachProtectedImportCredentialReview(
	script *dsl.Script,
	requirements []credentialpolicy.Requirement,
) ([]SecretBindingRequirement, error) {
	if script == nil || len(requirements) == 0 {
		return nil, nil
	}
	raw, err := scriptAsJSONValue(script)
	if err != nil {
		return nil, err
	}
	descriptors := make([]protectedImportCredentialDescriptor, 0, len(requirements))
	public := make([]SecretBindingRequirement, 0, len(requirements))
	seenIDs := make(map[string]struct{}, len(requirements))
	seenReplacements := make(map[string]string, len(requirements))
	for _, requirement := range requirements {
		if requirement.ExistingReference || strings.TrimSpace(requirement.Value) == "" {
			continue
		}
		id := strings.TrimSpace(requirement.ID)
		replacement := strings.TrimSpace(requirement.Replacement)
		name, validReference := credential.ParseReference(replacement)
		if id == "" || !validReference || name != requirement.SuggestedName {
			continue
		}
		if _, duplicate := seenIDs[id]; duplicate {
			return nil, errors.New("protected import credential requirement ids must be unique")
		}
		if priorID, duplicate := seenReplacements[replacement]; duplicate && priorID != id {
			return nil, errors.New("protected import credential references must be independently bindable")
		}
		locations := findProtectedImportCredentialLocations(raw, replacement)
		if len(locations) == 0 {
			return nil, errors.New("protected import credential target no longer matches the projected script")
		}
		seenIDs[id] = struct{}{}
		seenReplacements[replacement] = id
		descriptor := protectedImportCredentialDescriptor{
			ID:            id,
			Label:         strings.TrimSpace(requirement.Label),
			Context:       strings.TrimSpace(requirement.Context),
			SuggestedName: requirement.SuggestedName,
			Replacement:   replacement,
			Locations:     locations,
		}
		descriptors = append(descriptors, descriptor)
		public = append(public, descriptor.publicRequirement(script, false))
	}
	if len(descriptors) == 0 {
		return nil, nil
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].ID < descriptors[j].ID })
	sort.Slice(public, func(i, j int) bool { return public[i].ID < public[j].ID })
	preserved := ensureScriptImportPreserved(script, "import")
	preserved[protectedImportCredentialMetadataKey] = descriptors
	return public, nil
}

func (descriptor protectedImportCredentialDescriptor) publicRequirement(
	script *dsl.Script,
	sourceValueAvailable bool,
) SecretBindingRequirement {
	path := ""
	occurrence := 0
	if len(descriptor.Locations) > 0 {
		path = descriptor.Locations[0].Path
		occurrence = descriptor.Locations[0].Occurrence
	}
	requirement := SecretBindingRequirement{
		ID:                   descriptor.ID,
		Path:                 path,
		Occurrence:           occurrence,
		Label:                descriptor.Label,
		Context:              descriptor.Context,
		SuggestedName:        descriptor.SuggestedName,
		Replacement:          descriptor.Replacement,
		SourceValueAvailable: sourceValueAvailable,
	}
	if script != nil {
		requirement.ScriptID = script.ID
		requirement.ScriptName = script.Name
	}
	return requirement
}

func mergeProtectedImportCredentialReview(
	response map[string]any,
	requirements []SecretBindingRequirement,
) {
	if response == nil || len(requirements) == 0 {
		return
	}
	existing, _ := response["secret_binding_requirements"].([]SecretBindingRequirement)
	merged := append(append([]SecretBindingRequirement(nil), existing...), requirements...)
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].ID != merged[j].ID {
			return merged[i].ID < merged[j].ID
		}
		if merged[i].Path != merged[j].Path {
			return merged[i].Path < merged[j].Path
		}
		return merged[i].Occurrence < merged[j].Occurrence
	})
	response["secret_binding_requirements"] = merged
}

func (s *Server) attachProtectedImportCredentialMigration(
	r *http.Request,
	response map[string]any,
	projectID string,
	protected []credentialpolicy.Requirement,
	public []SecretBindingRequirement,
) []SecretBindingRequirement {
	if s == nil || response == nil || len(public) == 0 ||
		s.protectedSourceSessions == nil || !s.protectedSourceSessions.Available() {
		return public
	}
	if sessionID, _ := response["source_secret_session_id"].(string); strings.TrimSpace(sessionID) != "" {
		return public
	}
	allowed := make(map[string]struct{}, len(public))
	for _, requirement := range public {
		allowed[requirement.ID] = struct{}{}
	}
	values := make(map[string]string, len(public))
	for _, requirement := range protected {
		if _, ok := allowed[requirement.ID]; ok && requirement.Value != "" {
			values[requirement.ID] = requirement.Value
		}
	}
	if len(values) == 0 {
		return public
	}
	sessionID, err := s.registerSourceSecretSession(r, projectID, values)
	if err != nil {
		warnings, _ := response["warnings"].([]string)
		response["warnings"] = append(warnings, optionalCredentialMigrationUnavailableWarning)
		return public
	}
	if strings.TrimSpace(sessionID) == "" {
		return public
	}
	for index := range public {
		_, public[index].SourceValueAvailable = values[public[index].ID]
	}
	response["source_secret_session_id"] = sessionID
	response["source_secret_session_expires_at"] = time.Now().UTC().Add(sourceSecretSessionTTL)
	response["source_secret_migration_allowed"] = true
	return public
}

func loadProtectedImportCredentialDescriptors(script *dsl.Script) ([]protectedImportCredentialDescriptor, error) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return nil, nil
	}
	raw, exists := script.Metadata.Import.Preserved[protectedImportCredentialMetadataKey]
	if !exists || raw == nil {
		return nil, nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.New("protected import credential metadata is invalid")
	}
	var descriptors []protectedImportCredentialDescriptor
	if err := json.Unmarshal(payload, &descriptors); err != nil {
		return nil, errors.New("protected import credential metadata is invalid")
	}
	seen := make(map[string]struct{}, len(descriptors))
	for _, descriptor := range descriptors {
		name, ok := credential.ParseReference(descriptor.Replacement)
		if descriptor.ID == "" || descriptor.SuggestedName == "" || !ok ||
			name != descriptor.SuggestedName || len(descriptor.Locations) == 0 {
			return nil, errors.New("protected import credential metadata is invalid")
		}
		if _, duplicate := seen[descriptor.ID]; duplicate {
			return nil, errors.New("protected import credential metadata contains duplicate ids")
		}
		seen[descriptor.ID] = struct{}{}
		for _, location := range descriptor.Locations {
			if !strings.HasPrefix(location.Path, "/") || location.Occurrence < 0 ||
				(location.Encoding != "" && location.Encoding != "base64") {
				return nil, errors.New("protected import credential metadata is invalid")
			}
		}
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].ID < descriptors[j].ID })
	return descriptors, nil
}

func applyProtectedImportCredentialBindings(
	script *dsl.Script,
	bindings []secretBindingSelection,
) (*dsl.Script, []secretBindingSelection, []SecretBindingRequirement, error) {
	descriptors, err := loadProtectedImportCredentialDescriptors(script)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(descriptors) == 0 {
		return script, bindings, nil, nil
	}
	known := make(map[string]protectedImportCredentialDescriptor, len(descriptors))
	for _, descriptor := range descriptors {
		known[descriptor.ID] = descriptor
	}
	selected := make(map[string]string)
	legacy := make([]secretBindingSelection, 0, len(bindings))
	for _, binding := range bindings {
		id := strings.TrimSpace(binding.RequirementID)
		descriptor, protected := known[id]
		if !protected {
			legacy = append(legacy, binding)
			continue
		}
		if _, duplicate := selected[id]; duplicate {
			return nil, nil, nil, fmt.Errorf("binding requirement_id %q is duplicated", id)
		}
		name, err := engine.NormalizeLogicalSecretName(binding.SecretName)
		if err != nil || !logicalSecretNamePattern.MatchString(name) {
			return nil, nil, nil, fmt.Errorf("secret_name for %q must contain 1-249 letters, digits, underscores, dots, or hyphens", id)
		}
		if descriptor.ID == "" {
			return nil, nil, nil, errors.New("protected import credential binding is stale")
		}
		selected[id] = name
	}
	if len(selected) == 0 {
		remaining := publicProtectedImportCredentialDescriptors(script, descriptors, false)
		return script, legacy, remaining, nil
	}
	raw, err := scriptAsJSONValue(script)
	if err != nil {
		return nil, nil, nil, err
	}
	replacements := make(map[string][]protectedReferenceReplacement)
	for id, name := range selected {
		descriptor := known[id]
		replacement, err := credential.Reference(name)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, location := range descriptor.Locations {
			replacements[location.Path] = append(
				replacements[location.Path],
				protectedReferenceReplacement{
					From:       descriptor.Replacement,
					To:         replacement,
					Occurrence: location.Occurrence,
					Encoding:   location.Encoding,
				},
			)
		}
	}
	resolved, applied, err := applyProtectedReferenceReplacements(raw, "", replacements)
	if err != nil {
		return nil, nil, nil, err
	}
	if applied != protectedReferenceReplacementCount(replacements) {
		return nil, nil, nil, errors.New("protected import credential bindings are stale")
	}
	payload, err := json.Marshal(resolved)
	if err != nil {
		return nil, nil, nil, err
	}
	var result dsl.Script
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, nil, nil, err
	}
	remainingDescriptors := make([]protectedImportCredentialDescriptor, 0, len(descriptors)-len(selected))
	for _, descriptor := range descriptors {
		if _, applied := selected[descriptor.ID]; !applied {
			remainingDescriptors = append(remainingDescriptors, descriptor)
		}
	}
	setProtectedImportCredentialDescriptors(&result, remainingDescriptors)
	remaining := publicProtectedImportCredentialDescriptors(&result, remainingDescriptors, false)
	return &result, legacy, remaining, nil
}

func stripProtectedImportCredentialMetadata(script *dsl.Script) {
	setProtectedImportCredentialDescriptors(script, nil)
}

func setProtectedImportCredentialDescriptors(script *dsl.Script, descriptors []protectedImportCredentialDescriptor) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return
	}
	if len(descriptors) == 0 {
		delete(script.Metadata.Import.Preserved, protectedImportCredentialMetadataKey)
		return
	}
	script.Metadata.Import.Preserved[protectedImportCredentialMetadataKey] = descriptors
}

func publicProtectedImportCredentialDescriptors(
	script *dsl.Script,
	descriptors []protectedImportCredentialDescriptor,
	sourceValueAvailable bool,
) []SecretBindingRequirement {
	result := make([]SecretBindingRequirement, 0, len(descriptors))
	for _, descriptor := range descriptors {
		result = append(result, descriptor.publicRequirement(script, sourceValueAvailable))
	}
	return result
}

type protectedReferenceReplacement struct {
	From       string
	To         string
	Occurrence int
	Encoding   string
}

func applyProtectedReferenceReplacements(
	value any,
	path string,
	replacements map[string][]protectedReferenceReplacement,
) (any, int, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		applied := 0
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childPath := appendJSONPointer(path, key)
			if childPath == "/metadata/import/preserved/"+protectedImportCredentialMetadataKey {
				out[key] = typed[key]
				continue
			}
			child, count, err := applyProtectedReferenceReplacements(typed[key], childPath, replacements)
			if err != nil {
				return nil, 0, err
			}
			out[key] = child
			applied += count
		}
		return out, applied, nil
	case []any:
		out := make([]any, len(typed))
		applied := 0
		for index, child := range typed {
			projected, count, err := applyProtectedReferenceReplacements(
				child,
				appendJSONPointer(path, strconv.Itoa(index)),
				replacements,
			)
			if err != nil {
				return nil, 0, err
			}
			out[index] = projected
			applied += count
		}
		return out, applied, nil
	case string:
		instructions := append([]protectedReferenceReplacement(nil), replacements[path]...)
		if len(instructions) == 0 {
			return typed, 0, nil
		}
		result := typed
		bodyEncoding := ""
		for _, instruction := range instructions {
			if instruction.Encoding == "" {
				continue
			}
			if bodyEncoding != "" && bodyEncoding != instruction.Encoding {
				return nil, 0, errors.New("protected import credential metadata is invalid")
			}
			bodyEncoding = instruction.Encoding
		}
		if bodyEncoding == "base64" {
			decoded, _, err := dsl.DecodeBodyEncoding(
				result,
				bodyEncoding,
				credentialpolicy.DefaultLimits().MaxBytes,
			)
			if err != nil {
				return nil, 0, errors.New("protected import credential binding is stale")
			}
			result = string(decoded)
		}
		applied := 0
		sort.Slice(instructions, func(i, j int) bool {
			if instructions[i].From != instructions[j].From {
				return instructions[i].From > instructions[j].From
			}
			return instructions[i].Occurrence > instructions[j].Occurrence
		})
		for _, instruction := range instructions {
			next, ok := replaceProtectedReferenceOccurrence(
				result,
				instruction.From,
				instruction.Occurrence,
				instruction.To,
			)
			if !ok {
				return nil, 0, errors.New("protected import credential binding is stale")
			}
			result = next
			applied++
		}
		if bodyEncoding == "base64" {
			result = base64.StdEncoding.EncodeToString([]byte(result))
		}
		return result, applied, nil
	default:
		return value, 0, nil
	}
}

func replaceProtectedReferenceOccurrence(value, needle string, occurrence int, replacement string) (string, bool) {
	if needle == "" || occurrence < 0 {
		return value, false
	}
	offset := 0
	for current := 0; ; current++ {
		index := strings.Index(value[offset:], needle)
		if index < 0 {
			return value, false
		}
		index += offset
		if current == occurrence {
			return value[:index] + replacement + value[index+len(needle):], true
		}
		offset = index + len(needle)
	}
}

func protectedReferenceReplacementCount(replacements map[string][]protectedReferenceReplacement) int {
	count := 0
	for _, occurrences := range replacements {
		count += len(occurrences)
	}
	return count
}

func findProtectedImportCredentialLocations(root any, replacement string) []protectedImportCredentialLocation {
	result := make([]protectedImportCredentialLocation, 0)
	script, ok := root.(map[string]any)
	if !ok {
		return result
	}
	for _, key := range []string{"config", "init", "actions", "teardown"} {
		value, exists := script[key]
		if !exists {
			continue
		}
		collectProtectedImportCredentialLocations(
			value,
			appendJSONPointer("", key),
			replacement,
			&result,
		)
	}
	return result
}

func collectProtectedImportCredentialLocations(
	value any,
	path string,
	replacement string,
	out *[]protectedImportCredentialLocation,
) {
	switch typed := value.(type) {
	case map[string]any:
		body, hasBody := typed["body"].(string)
		bodyEncoding, _ := typed["body_encoding"].(string)
		base64Body := false
		if decoded, encoded, err := dsl.DecodeBodyEncoding(
			body,
			bodyEncoding,
			credentialpolicy.DefaultLimits().MaxBytes,
		); hasBody && encoded && err == nil {
			collectProtectedImportCredentialStringLocations(
				string(decoded),
				appendJSONPointer(path, "body"),
				replacement,
				"base64",
				out,
			)
			base64Body = true
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if base64Body && key == "body" {
				continue
			}
			collectProtectedImportCredentialLocations(
				typed[key],
				appendJSONPointer(path, key),
				replacement,
				out,
			)
		}
	case []any:
		for index, child := range typed {
			collectProtectedImportCredentialLocations(
				child,
				appendJSONPointer(path, strconv.Itoa(index)),
				replacement,
				out,
			)
		}
	case string:
		collectProtectedImportCredentialStringLocations(typed, path, replacement, "", out)
	}
}

func collectProtectedImportCredentialStringLocations(
	value,
	path,
	replacement,
	encoding string,
	out *[]protectedImportCredentialLocation,
) {
	for occurrence, offset := 0, 0; ; occurrence++ {
		index := strings.Index(value[offset:], replacement)
		if index < 0 {
			return
		}
		*out = append(*out, protectedImportCredentialLocation{
			Path:       path,
			Occurrence: occurrence,
			Encoding:   encoding,
		})
		offset += index + len(replacement)
	}
}
