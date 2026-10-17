package importreview

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	pathpkg "path"
	"sort"
	"strconv"
	"strings"
)

const ProtectedImportCredentialMetadataKey = "protected_credential_requirements_v1"

type ProtectedImportCredentialLocation struct {
	Path       string `json:"path"`
	Occurrence int    `json:"occurrence,omitempty"`
	Encoding   string `json:"encoding,omitempty"`
}

type ProtectedImportCredentialDescriptor struct {
	ID                                 string                              `json:"id"`
	Label                              string                              `json:"label"`
	Context                            string                              `json:"context"`
	SuggestedName                      string                              `json:"suggested_name"`
	Replacement                        string                              `json:"replacement"`
	SourceVariable                     string                              `json:"source_variable,omitempty"`
	UnresolvedSourceVariable           bool                                `json:"unresolved_source_variable,omitempty"`
	AffectedPaths                      []string                            `json:"affected_paths,omitempty"`
	PreservationUnavailableReason      string                              `json:"preservation_unavailable_reason,omitempty"`
	PreservationUnavailableDescription string                              `json:"preservation_unavailable_description,omitempty"`
	Locations                          []ProtectedImportCredentialLocation `json:"locations"`
}

func AttachImportCredentialReview(
	scripts []*dsl.Script,
	requirements []credentialpolicy.Requirement,
) ([]SecretBindingRequirement, error) {
	return AttachImportCredentialReviewWithWork(scripts, requirements, nil)
}

type ImportCredentialReviewWork struct {
	ScriptSerializations int
	LocationScans        int
}

func AttachImportCredentialReviewWithWork(
	scripts []*dsl.Script,
	requirements []credentialpolicy.Requirement,
	work *ImportCredentialReviewWork,
) ([]SecretBindingRequirement, error) {
	if len(scripts) == 0 || len(requirements) == 0 {
		return nil, nil
	}
	rawScripts := make([]any, len(scripts))
	sourcePaths := make([][]string, len(scripts))
	requestIdentities := make([][]string, len(scripts))
	locationCache := make([]map[string][]ProtectedImportCredentialLocation, len(scripts))
	for index, script := range scripts {
		raw, err := ScriptAsJSONValue(script)
		if err != nil {
			return nil, err
		}
		rawScripts[index] = raw
		sourcePaths[index] = ImportCredentialScriptSourcePaths(script)
		requestIdentities[index] = ImportCredentialScriptRequestIdentities(script)
		locationCache[index] = make(map[string][]ProtectedImportCredentialLocation)
		if work != nil {
			work.ScriptSerializations++
		}
	}
	locations := func(scriptIndex int, replacement string) []ProtectedImportCredentialLocation {
		if cached, ok := locationCache[scriptIndex][replacement]; ok {
			return cached
		}
		found := FindProtectedImportCredentialLocations(rawScripts[scriptIndex], replacement)
		locationCache[scriptIndex][replacement] = found
		if work != nil {
			work.LocationScans++
		}
		return found
	}
	AllScripts := make([]int, len(scripts))
	for index := range scripts {
		AllScripts[index] = index
	}
	byScript := make([][]credentialpolicy.Requirement, len(scripts))
	for _, requirement := range requirements {
		if requirement.ExistingReference {
			continue
		}
		replacement := strings.TrimSpace(requirement.Replacement)
		if replacement == "" {
			continue
		}
		candidates := NarrowImportCredentialScriptMatches(
			requirement, AllScripts, sourcePaths, requestIdentities,
		)
		matches := make([]int, 0, len(candidates))
		for _, scriptIndex := range candidates {
			if len(locations(scriptIndex, replacement)) > 0 {
				matches = append(matches, scriptIndex)
			}
		}
		if len(matches) == 0 {
			return nil, errors.New("import credential target no longer matches the projected scripts")
		}
		if len(matches) != 1 {
			return nil, errors.New("import credential target matches multiple generated scripts without unique source provenance")
		}
		byScript[matches[0]] = append(byScript[matches[0]], requirement)
	}
	public := make([]SecretBindingRequirement, 0, len(requirements))
	for scriptIndex, scoped := range byScript {
		if len(scoped) == 0 {
			continue
		}
		locationsByReplacement := make(map[string][]ProtectedImportCredentialLocation, len(scoped))
		for _, requirement := range scoped {
			locationsByReplacement[strings.TrimSpace(requirement.Replacement)] =
				locations(scriptIndex, strings.TrimSpace(requirement.Replacement))
		}
		review, err := AttachProtectedImportCredentialReviewWithLocations(
			scripts[scriptIndex], scoped, locationsByReplacement,
		)
		if err != nil {
			return nil, err
		}
		for index := range review {
			review[index].ScriptIndex = scriptIndex
		}
		public = append(public, review...)
	}
	sort.SliceStable(public, func(i, j int) bool {
		if public[i].ScriptIndex != public[j].ScriptIndex {
			return public[i].ScriptIndex < public[j].ScriptIndex
		}
		if public[i].ID != public[j].ID {
			return public[i].ID < public[j].ID
		}
		return public[i].Path < public[j].Path
	})
	return public, nil
}

func NarrowImportCredentialScriptMatches(
	requirement credentialpolicy.Requirement,
	matches []int,
	sourcePaths [][]string,
	requestIdentities [][]string,
) []int {
	if len(matches) <= 1 {
		return matches
	}
	sourcePath := NormalizeImportCredentialSourcePath(requirement.Source.Path)
	if sourcePath != "" {
		sourceMatches := make([]int, 0, len(matches))
		hadScriptSource := false
		for _, index := range matches {
			paths := sourcePaths[index]
			if len(paths) > 0 {
				hadScriptSource = true
			}
			for _, candidate := range paths {
				if candidate == sourcePath {
					sourceMatches = append(sourceMatches, index)
					break
				}
			}
		}
		if hadScriptSource {
			if len(sourceMatches) <= 1 {
				return sourceMatches
			}
			// Multi-lane converters commonly retain the same source file on every
			// generated script. Keep refining by trusted request/script identity
			// instead of treating source provenance as uniquely identifying.
			matches = sourceMatches
		}
	}
	requestKeys := make([]string, 0, len(requirement.Targets))
	for _, target := range requirement.Targets {
		if key := strings.ToLower(strings.TrimSpace(target.RequestKey)); key != "" {
			requestKeys = append(requestKeys, key)
		}
	}
	if len(requestKeys) == 0 {
		return matches
	}
	targetMatches := make([]int, 0, len(matches))
	for _, index := range matches {
		if ImportCredentialRequestKeyMatchesIdentities(requestKeys, requestIdentities[index]) {
			targetMatches = append(targetMatches, index)
		}
	}
	if len(targetMatches) > 0 {
		return targetMatches
	}
	return matches
}

func ImportCredentialScriptSourcePaths(script *dsl.Script) []string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return nil
	}
	paths := make([]string, 0, 4)
	appendPath := func(value string) {
		value = NormalizeImportCredentialSourcePath(value)
		if value != "" {
			paths = append(paths, value)
		}
	}
	if entry, _ := script.Metadata.Import.Preserved["source_zip_entry"].(string); entry != "" {
		appendPath(entry)
	}
	if rawReview, ok := script.Metadata.Import.Preserved["source_review"]; ok {
		payload, err := json.Marshal(rawReview)
		if err == nil {
			var review struct {
				SourceFiles []struct {
					Path string `json:"path"`
				} `json:"source_files"`
				Mappings []struct {
					SourceFile string `json:"source_file"`
				} `json:"mappings"`
			}
			if json.Unmarshal(payload, &review) == nil {
				for _, file := range review.SourceFiles {
					appendPath(file.Path)
				}
				for _, mapping := range review.Mappings {
					appendPath(mapping.SourceFile)
				}
			}
		}
	}
	return UniqueSortedStrings(paths)
}

func NormalizeImportCredentialSourcePath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	if value == "" || value == "." {
		return ""
	}
	return strings.TrimPrefix(pathpkg.Clean(value), "./")
}

func ImportCredentialScriptRequestIdentities(script *dsl.Script) []string {
	if script == nil {
		return nil
	}
	identities := []string{strings.ToLower(strings.TrimSpace(script.Name))}
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if ref.Action != nil {
			name := strings.ToLower(strings.TrimSpace(ref.Action.Name))
			if name != "" {
				identities = append(identities, name)
			}
		}
		return nil
	})
	return UniqueSortedStrings(identities)
}

func ImportCredentialRequestKeyMatchesIdentities(keys, identities []string) bool {
	for _, key := range keys {
		for _, identity := range identities {
			if identity != "" && (key == identity || strings.HasPrefix(key, identity+":")) {
				return true
			}
		}
	}
	return false
}

func AttachProtectedImportCredentialReviewWithLocations(
	script *dsl.Script,
	requirements []credentialpolicy.Requirement,
	locationsByReplacement map[string][]ProtectedImportCredentialLocation,
) ([]SecretBindingRequirement, error) {
	if script == nil || len(requirements) == 0 {
		return nil, nil
	}
	descriptors := make([]ProtectedImportCredentialDescriptor, 0, len(requirements))
	public := make([]SecretBindingRequirement, 0, len(requirements))
	seenIDs := make(map[string]struct{}, len(requirements))
	seenReplacements := make(map[string]string, len(requirements))
	for _, requirement := range requirements {
		if requirement.ExistingReference {
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
		locations := append([]ProtectedImportCredentialLocation(nil), locationsByReplacement[replacement]...)
		if len(locations) == 0 {
			return nil, errors.New("protected import credential target no longer matches the projected script")
		}
		seenIDs[id] = struct{}{}
		seenReplacements[replacement] = id
		descriptor := ProtectedImportCredentialDescriptor{
			ID:            id,
			Label:         strings.TrimSpace(requirement.Label),
			Context:       strings.TrimSpace(requirement.Context),
			SuggestedName: requirement.SuggestedName,
			Replacement:   replacement,
			Locations:     locations,
		}
		descriptor.EnrichSourceContext(requirement)
		descriptors = append(descriptors, descriptor)
		public = append(public, descriptor.PublicRequirement(script, false))
	}
	if len(descriptors) == 0 {
		return nil, nil
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].ID < descriptors[j].ID })
	sort.Slice(public, func(i, j int) bool { return public[i].ID < public[j].ID })
	preserved := EnsureScriptImportPreserved(script, "import")
	preserved[ProtectedImportCredentialMetadataKey] = descriptors
	return public, nil
}

func (descriptor ProtectedImportCredentialDescriptor) PublicRequirement(
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
		ID:                                 descriptor.ID,
		Path:                               path,
		Occurrence:                         occurrence,
		Label:                              descriptor.Label,
		Context:                            descriptor.Context,
		SuggestedName:                      descriptor.SuggestedName,
		Replacement:                        descriptor.Replacement,
		SourceVariable:                     descriptor.SourceVariable,
		UnresolvedSourceVariable:           descriptor.UnresolvedSourceVariable,
		AffectedPaths:                      append([]string(nil), descriptor.AffectedPaths...),
		PreservationUnavailableReason:      descriptor.PreservationUnavailableReason,
		PreservationUnavailableDescription: descriptor.PreservationUnavailableDescription,
		SourceValueAvailable:               sourceValueAvailable,
	}
	if script != nil {
		requirement.ScriptID = script.ID
		requirement.ScriptName = script.Name
	}
	return requirement
}

func (descriptor *ProtectedImportCredentialDescriptor) EnrichSourceContext(
	requirement credentialpolicy.Requirement,
) {
	if descriptor == nil {
		return
	}
	variables := append([]string(nil), requirement.SourceVariableNames...)
	sort.Strings(variables)
	if len(variables) > 0 {
		descriptor.SourceVariable = variables[0]
	}
	paths := make([]string, 0, len(requirement.Targets)+1)
	if sourcePath := strings.TrimSpace(requirement.Source.Path); sourcePath != "" {
		paths = append(paths, sourcePath)
	}
	for _, target := range requirement.Targets {
		if requestKey := strings.TrimSpace(target.RequestKey); requestKey != "" {
			paths = append(paths, requestKey)
		}
	}
	for _, location := range descriptor.Locations {
		if path := strings.TrimSpace(location.Path); path != "" {
			paths = append(paths, path)
		}
	}
	descriptor.AffectedPaths = UniqueSortedStrings(paths)
	status := requirement.SourceValueStatus
	if status == "" {
		status = credentialpolicy.ClassifySourceCredentialValue(requirement.Value)
	}
	if status.Available() {
		return
	}
	if len(variables) > 0 || strings.TrimSpace(requirement.DeferredSourceExpression) != "" {
		descriptor.UnresolvedSourceVariable = true
		descriptor.PreservationUnavailableReason = "unresolved_source_variable"
		descriptor.PreservationUnavailableDescription = "The source variable could not be resolved to an imported credential value. Configure the destination secret before replay."
		return
	}
	public := SecretBindingRequirement{}
	SetSecretBindingPreservationUnavailable(&public, status)
	descriptor.PreservationUnavailableReason = public.PreservationUnavailableReason
	descriptor.PreservationUnavailableDescription = public.PreservationUnavailableDescription
}

func MergeProtectedImportCredentialReview(
	response map[string]any,
	requirements []SecretBindingRequirement,
) {
	if response == nil || len(requirements) == 0 {
		return
	}
	existing, _ := response["secret_binding_requirements"].([]SecretBindingRequirement)
	authoritativeIDs := make(map[string]struct{}, len(requirements))
	authoritativePaths := make(map[string]struct{}, len(requirements)*2)
	for _, requirement := range requirements {
		authoritativeIDs[requirement.ID] = struct{}{}
		paths := append([]string{requirement.Path}, requirement.AffectedPaths...)
		for _, path := range paths {
			path = strings.TrimSpace(path)
			if path != "" {
				authoritativePaths[fmt.Sprintf("%d\x00%s", requirement.ScriptIndex, path)] = struct{}{}
			}
		}
	}
	merged := make([]SecretBindingRequirement, 0, len(existing)+len(requirements))
	for _, requirement := range existing {
		if _, duplicate := authoritativeIDs[requirement.ID]; duplicate {
			continue
		}
		if _, superseded := authoritativePaths[fmt.Sprintf("%d\x00%s", requirement.ScriptIndex, requirement.Path)]; superseded {
			// Converter-authored context is authoritative for this exact
			// credential slot. Drop the generic [REDACTED] discovery card rather
			// than showing two IDs for one selection.
			continue
		}
		merged = append(merged, requirement)
	}
	merged = append(merged, requirements...)
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

func LoadProtectedImportCredentialDescriptors(script *dsl.Script) ([]ProtectedImportCredentialDescriptor, error) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return nil, nil
	}
	raw, exists := script.Metadata.Import.Preserved[ProtectedImportCredentialMetadataKey]
	if !exists || raw == nil {
		return nil, nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.New("protected import credential metadata is invalid")
	}
	var descriptors []ProtectedImportCredentialDescriptor
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

func ApplyProtectedImportCredentialBindings(
	script *dsl.Script,
	bindings []SecretBindingSelection,
) (*dsl.Script, []SecretBindingSelection, []SecretBindingRequirement, error) {
	descriptors, err := LoadProtectedImportCredentialDescriptors(script)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(descriptors) == 0 {
		return script, bindings, nil, nil
	}
	known := make(map[string]ProtectedImportCredentialDescriptor, len(descriptors))
	for _, descriptor := range descriptors {
		known[descriptor.ID] = descriptor
	}
	selected := make(map[string]string)
	legacy := make([]SecretBindingSelection, 0, len(bindings))
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
		if err != nil || !LogicalSecretNamePattern.MatchString(name) {
			return nil, nil, nil, fmt.Errorf("secret_name for %q must contain 1-249 letters, digits, underscores, dots, or hyphens", id)
		}
		if descriptor.ID == "" {
			return nil, nil, nil, errors.New("protected import credential binding is stale")
		}
		selected[id] = name
	}
	if len(selected) == 0 {
		remaining := PublicProtectedImportCredentialDescriptors(script, descriptors, false)
		return script, legacy, remaining, nil
	}
	raw, err := ScriptAsJSONValue(script)
	if err != nil {
		return nil, nil, nil, err
	}
	replacements := make(map[string][]ProtectedReferenceReplacement)
	for id, name := range selected {
		descriptor := known[id]
		replacement, err := credential.Reference(name)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, location := range descriptor.Locations {
			replacements[location.Path] = append(
				replacements[location.Path],
				ProtectedReferenceReplacement{
					From:       descriptor.Replacement,
					To:         replacement,
					Occurrence: location.Occurrence,
					Encoding:   location.Encoding,
				},
			)
		}
	}
	resolved, applied, err := ApplyProtectedReferenceReplacements(raw, "", replacements)
	if err != nil {
		return nil, nil, nil, err
	}
	if applied != ProtectedReferenceReplacementCount(replacements) {
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
	remainingDescriptors := make([]ProtectedImportCredentialDescriptor, 0, len(descriptors)-len(selected))
	for _, descriptor := range descriptors {
		if _, applied := selected[descriptor.ID]; !applied {
			remainingDescriptors = append(remainingDescriptors, descriptor)
		}
	}
	SetProtectedImportCredentialDescriptors(&result, remainingDescriptors)
	remaining := PublicProtectedImportCredentialDescriptors(&result, remainingDescriptors, false)
	return &result, legacy, remaining, nil
}

func SetProtectedImportCredentialDescriptors(script *dsl.Script, descriptors []ProtectedImportCredentialDescriptor) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return
	}
	if len(descriptors) == 0 {
		delete(script.Metadata.Import.Preserved, ProtectedImportCredentialMetadataKey)
		return
	}
	script.Metadata.Import.Preserved[ProtectedImportCredentialMetadataKey] = descriptors
}

func PublicProtectedImportCredentialDescriptors(
	script *dsl.Script,
	descriptors []ProtectedImportCredentialDescriptor,
	sourceValueAvailable bool,
) []SecretBindingRequirement {
	result := make([]SecretBindingRequirement, 0, len(descriptors))
	for _, descriptor := range descriptors {
		result = append(result, descriptor.PublicRequirement(script, sourceValueAvailable))
	}
	return result
}

type ProtectedReferenceReplacement struct {
	From       string
	To         string
	Occurrence int
	Encoding   string
}

func ApplyProtectedReferenceReplacements(
	value any,
	path string,
	replacements map[string][]ProtectedReferenceReplacement,
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
			childPath := AppendJSONPointer(path, key)
			if childPath == "/metadata/import/preserved/"+ProtectedImportCredentialMetadataKey {
				out[key] = typed[key]
				continue
			}
			child, count, err := ApplyProtectedReferenceReplacements(typed[key], childPath, replacements)
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
			projected, count, err := ApplyProtectedReferenceReplacements(
				child,
				AppendJSONPointer(path, strconv.Itoa(index)),
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
		instructions := append([]ProtectedReferenceReplacement(nil), replacements[path]...)
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
			next, ok := ReplaceProtectedReferenceOccurrence(
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

func ReplaceProtectedReferenceOccurrence(value, needle string, occurrence int, replacement string) (string, bool) {
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

func ProtectedReferenceReplacementCount(replacements map[string][]ProtectedReferenceReplacement) int {
	count := 0
	for _, occurrences := range replacements {
		count += len(occurrences)
	}
	return count
}

func FindProtectedImportCredentialLocations(root any, replacement string) []ProtectedImportCredentialLocation {
	result := make([]ProtectedImportCredentialLocation, 0)
	script, ok := root.(map[string]any)
	if !ok {
		return result
	}
	for _, key := range []string{"config", "init", "actions", "teardown"} {
		value, exists := script[key]
		if !exists {
			continue
		}
		CollectProtectedImportCredentialLocations(
			value,
			AppendJSONPointer("", key),
			replacement,
			&result,
		)
	}
	return result
}

func CollectProtectedImportCredentialLocations(
	value any,
	path string,
	replacement string,
	out *[]ProtectedImportCredentialLocation,
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
			CollectProtectedImportCredentialStringLocations(
				string(decoded),
				AppendJSONPointer(path, "body"),
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
			CollectProtectedImportCredentialLocations(
				typed[key],
				AppendJSONPointer(path, key),
				replacement,
				out,
			)
		}
	case []any:
		for index, child := range typed {
			CollectProtectedImportCredentialLocations(
				child,
				AppendJSONPointer(path, strconv.Itoa(index)),
				replacement,
				out,
			)
		}
	case string:
		CollectProtectedImportCredentialStringLocations(typed, path, replacement, "", out)
	}
}

func CollectProtectedImportCredentialStringLocations(
	value,
	path,
	replacement,
	encoding string,
	out *[]ProtectedImportCredentialLocation,
) {
	for occurrence, offset := 0, 0; ; occurrence++ {
		index := strings.Index(value[offset:], replacement)
		if index < 0 {
			return
		}
		*out = append(*out, ProtectedImportCredentialLocation{
			Path:       path,
			Occurrence: occurrence,
			Encoding:   encoding,
		})
		offset += index + len(replacement)
	}
}
