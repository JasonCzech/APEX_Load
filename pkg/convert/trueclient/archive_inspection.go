package trueclient

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type ArchiveInspection struct {
	ScriptType              string                 `json:"script_type"`
	ScriptTypeScope         *ScriptTypeScope       `json:"script_type_scope,omitempty"`
	ProjectFiles            []ArchiveFileSummary   `json:"project_files,omitempty"`
	DefaultXML              *ArchiveFileSummary    `json:"default_xml,omitempty"`
	ObjectRepositories      []ArchiveFileSummary   `json:"object_repositories,omitempty"`
	ObjectDescriptors       []ObjectDescriptor     `json:"object_descriptors,omitempty"`
	ParameterFiles          []ArchiveFileSummary   `json:"parameter_files,omitempty"`
	RuntimeFiles            []ArchiveFileSummary   `json:"runtime_files,omitempty"`
	StepInventory           StepInventory          `json:"step_inventory"`
	Steps                   []StepInspection       `json:"steps,omitempty"`
	UnsupportedFragments    []SourceFragment       `json:"unsupported_fragments,omitempty"`
	PreservedMetadataInputs []string               `json:"preserved_metadata_inputs,omitempty"`
	Warnings                []string               `json:"warnings,omitempty"`
	CapabilityStatuses      map[string]ScopeStatus `json:"capability_statuses,omitempty"`
}

type ArchiveFileSummary struct {
	Path     string `json:"path"`
	BaseName string `json:"base_name"`
	Kind     string `json:"kind"`
	Bytes    int    `json:"bytes"`
}

type StepInventory struct {
	Total       int            `json:"total"`
	Supported   map[string]int `json:"supported,omitempty"`
	ReviewOnly  map[string]int `json:"review_only,omitempty"`
	Unsupported map[string]int `json:"unsupported,omitempty"`
}

type StepInspection struct {
	StepID             string              `json:"step_id,omitempty"`
	Index              string              `json:"index,omitempty"`
	Action             string              `json:"action"`
	ActionName         string              `json:"action_name,omitempty"`
	ObjectDescriptorID string              `json:"object_descriptor_id,omitempty"`
	ObjectDescriptor   *ObjectDescriptor   `json:"object_descriptor,omitempty"`
	FrameContext       string              `json:"frame_context,omitempty"`
	WindowContext      string              `json:"window_context,omitempty"`
	Ordinal            string              `json:"ordinal,omitempty"`
	Regex              string              `json:"regex,omitempty"`
	ParameterRefs      []string            `json:"parameter_refs,omitempty"`
	SnapshotRefs       []string            `json:"snapshot_refs,omitempty"`
	Status             ScopeStatus         `json:"status"`
	ScopeName          string              `json:"scope_name,omitempty"`
	SelectorCandidates []SelectorCandidate `json:"selector_candidates,omitempty"`
	ReplayDiagnostics  []ReplayDiagnostic  `json:"replay_diagnostics,omitempty"`
	SourceSnippet      string              `json:"source_snippet,omitempty"`
}

type ObjectDescriptor struct {
	ID                 string              `json:"id,omitempty"`
	Name               string              `json:"name,omitempty"`
	SourcePath         string              `json:"source_path,omitempty"`
	Selector           string              `json:"selector,omitempty"`
	XPath              string              `json:"xpath,omitempty"`
	Text               string              `json:"text,omitempty"`
	Role               string              `json:"role,omitempty"`
	Ordinal            string              `json:"ordinal,omitempty"`
	Regex              string              `json:"regex,omitempty"`
	FrameContext       string              `json:"frame_context,omitempty"`
	WindowContext      string              `json:"window_context,omitempty"`
	Attributes         map[string]string   `json:"attributes,omitempty"`
	SelectorCandidates []SelectorCandidate `json:"selector_candidates,omitempty"`
}

type SelectorCandidate struct {
	Kind       string `json:"kind"`
	Value      string `json:"value"`
	Confidence int    `json:"confidence"`
	Reason     string `json:"reason"`
}

type ReplayDiagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Action   string `json:"action"`
}

type SourceFragment struct {
	StepID        string      `json:"step_id,omitempty"`
	Index         string      `json:"index,omitempty"`
	Action        string      `json:"action"`
	Status        ScopeStatus `json:"status"`
	SourceSnippet string      `json:"source_snippet,omitempty"`
	Reason        string      `json:"reason"`
}

func InspectArchive(files map[string]string, scriptType string) (*ArchiveInspection, error) {
	if files == nil {
		files = map[string]string{}
	}

	inspection := &ArchiveInspection{
		ScriptType:              strings.TrimSpace(scriptType),
		CapabilityStatuses:      web2CapabilityStatusMap(),
		PreservedMetadataInputs: make([]string, 0),
	}
	if scope, ok := LookupScriptTypeScope(scriptType); ok {
		inspection.ScriptTypeScope = &scope
	}

	for name, content := range files {
		summary := archiveFileSummary(name, content)
		switch classifyArchiveFile(name) {
		case "project":
			summary.Kind = "project"
			inspection.ProjectFiles = append(inspection.ProjectFiles, summary)
		case "object_repository":
			summary.Kind = "object_repository"
			inspection.ObjectRepositories = append(inspection.ObjectRepositories, summary)
			inspection.ObjectDescriptors = append(inspection.ObjectDescriptors, parseObjectRepositoryDescriptors(name, content)...)
		case "parameter":
			summary.Kind = "parameter"
			inspection.ParameterFiles = append(inspection.ParameterFiles, summary)
		case "runtime":
			summary.Kind = "runtime"
			inspection.RuntimeFiles = append(inspection.RuntimeFiles, summary)
		}
	}
	sortArchiveFiles(inspection.ProjectFiles)
	sortArchiveFiles(inspection.ObjectRepositories)
	sortObjectDescriptors(inspection.ObjectDescriptors)
	sortArchiveFiles(inspection.ParameterFiles)
	sortArchiveFiles(inspection.RuntimeFiles)

	if xmlName, xmlContent, ok := findDefaultXML(files); ok {
		summary := archiveFileSummary(xmlName, xmlContent)
		summary.Kind = "default_xml"
		inspection.DefaultXML = &summary
		inventory, steps, fragments, err := inspectDefaultXML(xmlName, xmlContent, inspection.ObjectDescriptors)
		if err != nil {
			return nil, err
		}
		inspection.StepInventory = inventory
		inspection.Steps = steps
		inspection.UnsupportedFragments = fragments
	} else {
		inspection.Warnings = append(inspection.Warnings, "No TruClient default.xml was found in the archive.")
	}

	inspection.PreservedMetadataInputs = preservedMetadataInputs(inspection)
	return inspection, nil
}

func inspectDefaultXML(name string, content string, descriptors []ObjectDescriptor) (StepInventory, []StepInspection, []SourceFragment, error) {
	decoded := normalizeXMLDeclaration(decodeXMLContent(content))
	var doc truScriptDocument
	if err := xml.Unmarshal([]byte(decoded), &doc); err != nil {
		return StepInventory{}, nil, nil, fmt.Errorf("parse TruClient %s for archive inspection: %w", filepath.Base(name), err)
	}

	inventory := StepInventory{
		Supported:   map[string]int{},
		ReviewOnly:  map[string]int{},
		Unsupported: map[string]int{},
	}
	steps := make([]StepInspection, 0)
	fragments := make([]SourceFragment, 0)
	for _, step := range executionSteps(doc.Steps) {
		action := strings.TrimSpace(step.Action)
		if action == "" {
			continue
		}
		inventory.Total++
		scope, ok := LookupStepScope(action)
		if !ok {
			inventory.Unsupported[action]++
			stepInspection := inspectStep(step, ScopeOutOfScope, "", descriptors)
			steps = append(steps, stepInspection)
			fragments = append(fragments, sourceFragment(stepInspection, "Unsupported TruClient step action is not mapped to a APEX Load browser action."))
			continue
		}
		switch scope.Status {
		case ScopeSupported:
			inventory.Supported[scope.CanonicalName]++
		case ScopeReviewOnly:
			inventory.ReviewOnly[scope.CanonicalName]++
			stepInspection := inspectStep(step, scope.Status, scope.CanonicalName, descriptors)
			steps = append(steps, stepInspection)
			fragments = append(fragments, sourceFragment(stepInspection, "Review-only TruClient step requires replay or guided repair before approval."))
			continue
		default:
			inventory.Unsupported[scope.CanonicalName]++
			stepInspection := inspectStep(step, scope.Status, scope.CanonicalName, descriptors)
			steps = append(steps, stepInspection)
			fragments = append(fragments, sourceFragment(stepInspection, "TruClient step scope is not executable in the current browser draft importer."))
			continue
		}
		steps = append(steps, inspectStep(step, scope.Status, scope.CanonicalName, descriptors))
	}
	return inventory, steps, fragments, nil
}

func archiveFileSummary(path string, content string) ArchiveFileSummary {
	return ArchiveFileSummary{
		Path:     path,
		BaseName: filepath.Base(path),
		Bytes:    len([]byte(content)),
	}
}

func classifyArchiveFile(path string) string {
	base := strings.ToLower(filepath.Base(path))
	dir := strings.ToLower(filepath.Dir(path))
	ext := strings.ToLower(filepath.Ext(base))
	compact := strings.ReplaceAll(base, "_", "")
	compact = strings.ReplaceAll(compact, "-", "")
	compactDir := strings.ReplaceAll(dir, "_", "")
	compactDir = strings.ReplaceAll(compactDir, "-", "")

	switch {
	case ext == ".usr":
		return "project"
	case strings.EqualFold(base, "default.cfg") || strings.EqualFold(base, "default.usp") || strings.Contains(compact, "runtime"):
		return "runtime"
	case ext == ".dat" || ext == ".csv" || strings.Contains(compactDir, "parameter") || strings.Contains(compact, "parameter"):
		return "parameter"
	case strings.Contains(compact, "objectrepository") || strings.Contains(compactDir, "objectrepository") || strings.Contains(compactDir, "objects"):
		return "object_repository"
	default:
		return ""
	}
}

func sortArchiveFiles(files []ArchiveFileSummary) {
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
}

func sortObjectDescriptors(descriptors []ObjectDescriptor) {
	sort.Slice(descriptors, func(i, j int) bool {
		if descriptors[i].SourcePath == descriptors[j].SourcePath {
			if descriptors[i].ID == descriptors[j].ID {
				return descriptors[i].Name < descriptors[j].Name
			}
			return descriptors[i].ID < descriptors[j].ID
		}
		return descriptors[i].SourcePath < descriptors[j].SourcePath
	})
}

func preservedMetadataInputs(inspection *ArchiveInspection) []string {
	inputs := make([]string, 0)
	if inspection == nil {
		return inputs
	}
	appendKind := func(kind string, files []ArchiveFileSummary) {
		for _, file := range files {
			inputs = append(inputs, kind+": "+file.Path)
		}
	}
	if inspection.DefaultXML != nil {
		inputs = append(inputs, "default_xml: "+inspection.DefaultXML.Path)
	}
	appendKind("project", inspection.ProjectFiles)
	appendKind("object_repository", inspection.ObjectRepositories)
	appendKind("parameter", inspection.ParameterFiles)
	appendKind("runtime", inspection.RuntimeFiles)
	sort.Strings(inputs)
	return inputs
}

func web2CapabilityStatusMap() map[string]ScopeStatus {
	statuses := make(map[string]ScopeStatus)
	for _, scope := range Web2ProfileCapabilityScopes() {
		statuses[scope.CanonicalName] = scope.Status
	}
	return statuses
}

func inspectStep(step truXMLStep, status ScopeStatus, scopeName string, descriptors []ObjectDescriptor) StepInspection {
	values, _ := parseArgumentValues(step.Arguments)
	descriptorID := objectDescriptorIDFromValues(values)
	objectDescriptor := matchObjectDescriptor(descriptors, descriptorID, step)
	candidates := selectorCandidatesFromValues(values)
	if objectDescriptor != nil {
		candidates = append(candidates, objectDescriptor.SelectorCandidates...)
	}
	inspection := StepInspection{
		StepID:             strings.TrimSpace(step.StepID),
		Index:              strings.TrimSpace(step.Index),
		Action:             strings.TrimSpace(step.Action),
		ActionName:         strings.TrimSpace(step.ActionName),
		ObjectDescriptorID: descriptorID,
		ObjectDescriptor:   objectDescriptor,
		FrameContext:       firstNonEmptyString(trimArgumentValue(values, "Frame", "Frame ID", "FrameContext", "Iframe", "Iframe ID"), descriptorString(objectDescriptor, "frame")),
		WindowContext:      firstNonEmptyString(trimArgumentValue(values, "Window", "Window ID", "WindowContext", "Tab", "Browser Window"), descriptorString(objectDescriptor, "window")),
		Ordinal:            firstNonEmptyString(trimArgumentValue(values, "Ordinal", "Index", "Object Index"), descriptorString(objectDescriptor, "ordinal")),
		Regex:              firstNonEmptyString(trimArgumentValue(values, "Regex", "RegExp", "Regular Expression"), descriptorString(objectDescriptor, "regex")),
		ParameterRefs:      parameterReferences(values),
		SnapshotRefs:       snapshotReferences(values),
		Status:             status,
		ScopeName:          scopeName,
		SelectorCandidates: uniqueSelectorCandidates(candidates),
		SourceSnippet:      compactSourceSnippet(step.Arguments),
	}
	inspection.ReplayDiagnostics = replayDiagnosticsForStep(inspection)
	return inspection
}

func sourceFragment(step StepInspection, reason string) SourceFragment {
	return SourceFragment{
		StepID:        step.StepID,
		Index:         step.Index,
		Action:        step.Action,
		Status:        step.Status,
		SourceSnippet: step.SourceSnippet,
		Reason:        reason,
	}
}

func compactSourceSnippet(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(value) <= 500 {
		return value
	}
	return value[:500] + "..."
}

func objectDescriptorID(rawArguments string) string {
	values, err := parseArgumentValues(rawArguments)
	if err != nil {
		return ""
	}
	return objectDescriptorIDFromValues(values)
}

func objectDescriptorIDFromValues(values map[string]truArgument) string {
	for _, key := range []string{"Object ID", "ObjectID", "object_id", "Descriptor ID", "DescriptorID", "Test Object ID", "Step Object ID"} {
		if value := argumentValue(values, key); value != "" {
			return trimQuotedValue(value)
		}
	}
	return ""
}

func selectorCandidatesFromValues(values map[string]truArgument) []SelectorCandidate {
	candidates := make([]SelectorCandidate, 0)
	add := func(kind string, confidence int, reason string, keys ...string) {
		for _, key := range keys {
			value := trimQuotedValue(argumentValue(values, key))
			if value == "" {
				continue
			}
			candidates = append(candidates, SelectorCandidate{
				Kind:       kind,
				Value:      value,
				Confidence: confidence,
				Reason:     reason,
			})
			return
		}
	}
	add("css", 85, "Captured CSS selector from TruClient step arguments.", "Selector", "selector", "CSS", "Css", "css")
	add("css", 70, "Captured target/element locator from TruClient step arguments.", "Target", "target", "Element", "element")
	add("xpath", 60, "Captured XPath locator; replay should validate stability.", "XPath", "Xpath", "xpath")
	add("text", 55, "Captured visible text locator; replay should validate uniqueness.", "Text", "text", "Visible Text", "visible_text")
	add("role", 65, "Captured role locator; replay should validate accessible role and name.", "Role", "role", "ARIA Role", "aria_role")
	return candidates
}

func argumentValue(values map[string]truArgument, key string) string {
	for existingKey, existingValue := range values {
		if strings.EqualFold(existingKey, key) && strings.TrimSpace(existingValue.Value) != "" {
			return existingValue.Value
		}
	}
	return ""
}

type objectRepoNode struct {
	XMLName  xml.Name         `xml:""`
	Attrs    []xml.Attr       `xml:",any,attr"`
	Text     string           `xml:",chardata"`
	Children []objectRepoNode `xml:",any"`
}

var parameterReferencePattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_.-]*)\}`)

func parseObjectRepositoryDescriptors(path string, content string) []ObjectDescriptor {
	decoded := normalizeXMLDeclaration(decodeXMLContent(content))
	var root objectRepoNode
	if err := xml.Unmarshal([]byte(decoded), &root); err != nil {
		return nil
	}
	descriptors := make([]ObjectDescriptor, 0)
	var walk func(objectRepoNode)
	walk = func(node objectRepoNode) {
		props := objectRepositoryProperties(node)
		if descriptor, ok := objectDescriptorFromProperties(path, props); ok {
			descriptors = append(descriptors, descriptor)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return uniqueObjectDescriptors(descriptors)
}

func objectRepositoryProperties(node objectRepoNode) map[string]string {
	props := make(map[string]string)
	for _, attr := range node.Attrs {
		if value := strings.TrimSpace(attr.Value); value != "" {
			props[normalizeObjectRepositoryKey(attr.Name.Local)] = value
		}
	}
	for _, child := range node.Children {
		key := firstNonEmptyString(objectRepoAttr(child, "name"), objectRepoAttr(child, "key"), objectRepoAttr(child, "type"), child.XMLName.Local)
		value := firstNonEmptyString(objectRepoAttr(child, "value"), objectRepoAttr(child, "selector"), objectRepoAttr(child, "path"), strings.TrimSpace(child.Text))
		if key != "" && value != "" {
			props[normalizeObjectRepositoryKey(key)] = value
		}
		for _, grandchild := range child.Children {
			nestedKey := firstNonEmptyString(objectRepoAttr(grandchild, "name"), objectRepoAttr(grandchild, "key"), objectRepoAttr(grandchild, "type"), grandchild.XMLName.Local)
			nestedValue := firstNonEmptyString(objectRepoAttr(grandchild, "value"), strings.TrimSpace(grandchild.Text))
			if nestedKey != "" && nestedValue != "" {
				props[normalizeObjectRepositoryKey(nestedKey)] = nestedValue
			}
		}
	}
	return props
}

func objectRepoAttr(node objectRepoNode, key string) string {
	for _, attr := range node.Attrs {
		if strings.EqualFold(attr.Name.Local, key) {
			return strings.TrimSpace(attr.Value)
		}
	}
	return ""
}

func objectDescriptorFromProperties(path string, props map[string]string) (ObjectDescriptor, bool) {
	explicitID := firstProperty(props, "objectid", "descriptorid", "testobjectid", "stepobjectid", "logicalid", "id")
	descriptor := ObjectDescriptor{
		ID:            explicitID,
		Name:          firstProperty(props, "logicalname", "name", "displayname", "label"),
		SourcePath:    path,
		Selector:      firstProperty(props, "selector", "css", "cssselector", "csspath"),
		XPath:         firstProperty(props, "xpath", "xpathexpression"),
		Text:          firstProperty(props, "text", "visibletext", "innertext", "textcontent"),
		Role:          firstProperty(props, "role", "ariarole"),
		Ordinal:       firstProperty(props, "ordinal", "objectindex", "index"),
		Regex:         firstProperty(props, "regex", "regexp", "regularexpression"),
		FrameContext:  firstProperty(props, "frame", "frameid", "framecontext", "iframe", "iframeid"),
		WindowContext: firstProperty(props, "window", "windowid", "windowcontext", "tab", "browserwindow"),
		Attributes:    objectDescriptorAttributes(props),
	}
	hasLocatorEvidence := descriptor.Selector != "" || descriptor.XPath != "" || descriptor.Text != "" || descriptor.Role != "" || len(descriptor.Attributes) > 0
	hasContextEvidence := descriptor.Ordinal != "" || descriptor.Regex != "" || descriptor.FrameContext != "" || descriptor.WindowContext != ""
	if descriptor.ID == "" && (hasLocatorEvidence || hasContextEvidence) {
		descriptor.ID = descriptor.Name
	}
	if explicitID == "" && descriptor.ID == "" && !hasLocatorEvidence && !hasContextEvidence {
		return ObjectDescriptor{}, false
	}
	descriptor.SelectorCandidates = selectorCandidatesFromDescriptor(descriptor)
	return descriptor, true
}

func objectDescriptorAttributes(props map[string]string) map[string]string {
	attributes := make(map[string]string)
	for rawKey, value := range props {
		if value == "" {
			continue
		}
		key := rawKey
		switch key {
		case "datatestid":
			attributes["data-testid"] = value
		case "arialabel":
			attributes["aria-label"] = value
		case "htmlid":
			attributes["id"] = value
		case "htmlname":
			attributes["name"] = value
		case "classname":
			attributes["class"] = value
		case "href", "id", "class", "type", "title":
			attributes[key] = value
		default:
			if strings.HasPrefix(key, "attr") && len(key) > 4 {
				attributes[key[4:]] = value
			}
		}
	}
	if len(attributes) == 0 {
		return nil
	}
	return attributes
}

func selectorCandidatesFromDescriptor(descriptor ObjectDescriptor) []SelectorCandidate {
	candidates := make([]SelectorCandidate, 0)
	if descriptor.Attributes != nil {
		if value := descriptor.Attributes["data-testid"]; value != "" {
			candidates = append(candidates, SelectorCandidate{Kind: "css", Value: cssAttributeSelector("data-testid", value), Confidence: 95, Reason: "Object repository data-testid attribute."})
		}
		if value := descriptor.Attributes["id"]; value != "" {
			candidates = append(candidates, SelectorCandidate{Kind: "css", Value: "#" + value, Confidence: 90, Reason: "Object repository HTML id attribute."})
		}
		if value := descriptor.Attributes["aria-label"]; value != "" {
			candidates = append(candidates, SelectorCandidate{Kind: "css", Value: cssAttributeSelector("aria-label", value), Confidence: 82, Reason: "Object repository ARIA label attribute."})
		}
		if value := descriptor.Attributes["name"]; value != "" {
			candidates = append(candidates, SelectorCandidate{Kind: "css", Value: cssAttributeSelector("name", value), Confidence: 76, Reason: "Object repository name attribute."})
		}
	}
	if descriptor.Selector != "" {
		candidates = append(candidates, SelectorCandidate{Kind: "css", Value: descriptor.Selector, Confidence: 88, Reason: "Object repository CSS selector."})
	}
	if descriptor.Role != "" {
		candidates = append(candidates, SelectorCandidate{Kind: "role", Value: descriptor.Role, Confidence: 72, Reason: "Object repository role locator."})
	}
	if descriptor.Text != "" {
		candidates = append(candidates, SelectorCandidate{Kind: "text", Value: descriptor.Text, Confidence: 62, Reason: "Object repository visible text locator."})
	}
	if descriptor.XPath != "" {
		candidates = append(candidates, SelectorCandidate{Kind: "xpath", Value: descriptor.XPath, Confidence: 58, Reason: "Object repository XPath locator; validate stability in replay."})
	}
	return uniqueSelectorCandidates(candidates)
}

func cssAttributeSelector(name string, value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return fmt.Sprintf(`[%s="%s"]`, name, escaped)
}

func matchObjectDescriptor(descriptors []ObjectDescriptor, descriptorID string, step truXMLStep) *ObjectDescriptor {
	normalizedID := normalizeObjectRepositoryKey(descriptorID)
	actionName := normalizeObjectRepositoryKey(firstNonEmptyString(step.ActionName, step.Action))
	for _, descriptor := range descriptors {
		if normalizedID != "" && (normalizeObjectRepositoryKey(descriptor.ID) == normalizedID || normalizeObjectRepositoryKey(descriptor.Name) == normalizedID) {
			copy := descriptor
			return &copy
		}
		if normalizedID == "" && actionName != "" && normalizeObjectRepositoryKey(descriptor.Name) == actionName {
			copy := descriptor
			return &copy
		}
	}
	return nil
}

func uniqueObjectDescriptors(descriptors []ObjectDescriptor) []ObjectDescriptor {
	seen := make(map[string]struct{})
	out := make([]ObjectDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		key := strings.Join([]string{descriptor.SourcePath, descriptor.ID, descriptor.Name, descriptor.Selector, descriptor.XPath, descriptor.Text}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, descriptor)
	}
	return out
}

func uniqueSelectorCandidates(candidates []SelectorCandidate) []SelectorCandidate {
	seen := make(map[string]struct{})
	out := make([]SelectorCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Value = strings.TrimSpace(candidate.Value)
		if candidate.Value == "" {
			continue
		}
		key := strings.ToLower(candidate.Kind) + "\x00" + candidate.Value
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence == out[j].Confidence {
			return out[i].Value < out[j].Value
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out
}

func firstProperty(props map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(props[normalizeObjectRepositoryKey(key)]); value != "" {
			return trimQuotedValue(value)
		}
	}
	return ""
}

func normalizeObjectRepositoryKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "", ".", "", ":", "")
	return replacer.Replace(value)
}

func trimArgumentValue(values map[string]truArgument, keys ...string) string {
	for _, key := range keys {
		if value := argumentValue(values, key); value != "" {
			return trimQuotedValue(value)
		}
	}
	return ""
}

func descriptorString(descriptor *ObjectDescriptor, field string) string {
	if descriptor == nil {
		return ""
	}
	switch field {
	case "frame":
		return descriptor.FrameContext
	case "window":
		return descriptor.WindowContext
	case "ordinal":
		return descriptor.Ordinal
	case "regex":
		return descriptor.Regex
	default:
		return ""
	}
}

func parameterReferences(values map[string]truArgument) []string {
	refs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, value := range values {
		for _, match := range parameterReferencePattern.FindAllStringSubmatch(value.Value, -1) {
			if len(match) < 2 {
				continue
			}
			ref := strings.TrimSpace(match[1])
			if ref == "" {
				continue
			}
			if _, ok := seen[ref]; ok {
				continue
			}
			seen[ref] = struct{}{}
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	return refs
}

func snapshotReferences(values map[string]truArgument) []string {
	refs := make([]string, 0)
	seen := make(map[string]struct{})
	for key, value := range values {
		normalized := normalizeObjectRepositoryKey(key)
		if !strings.Contains(normalized, "snapshot") && !strings.Contains(normalized, "screenshot") {
			continue
		}
		ref := trimQuotedValue(value.Value)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func replayDiagnosticsForStep(step StepInspection) []ReplayDiagnostic {
	diagnostics := make([]ReplayDiagnostic, 0)
	hasSelector := len(step.SelectorCandidates) > 0
	selectorConfidence := 0
	if hasSelector {
		selectorConfidence = step.SelectorCandidates[0].Confidence
	}
	if !hasSelector && step.Status == ScopeSupported {
		diagnostics = append(diagnostics, ReplayDiagnostic{
			Code:     "object_not_found_risk",
			Severity: "warning",
			Summary:  "No selector candidate was preserved for this browser action.",
			Action:   "Choose a selector from replay DOM evidence before approving the imported step.",
		})
	}
	if step.FrameContext != "" {
		diagnostics = append(diagnostics, ReplayDiagnostic{
			Code:     "frame_mismatch_risk",
			Severity: "info",
			Summary:  "The imported step carries iframe/frame context.",
			Action:   "Replay the step and confirm the selector is scoped to the expected frame.",
		})
	}
	if step.WindowContext != "" {
		diagnostics = append(diagnostics, ReplayDiagnostic{
			Code:     "window_context_risk",
			Severity: "info",
			Summary:  "The imported step carries window or tab context.",
			Action:   "Replay the flow and confirm the active page matches the original VuGen window context.",
		})
	}
	if step.Ordinal != "" {
		if ordinal, err := strconv.Atoi(step.Ordinal); err == nil && ordinal > 1 {
			diagnostics = append(diagnostics, ReplayDiagnostic{
				Code:     "ambiguous_match_risk",
				Severity: "warning",
				Summary:  "The imported descriptor depends on an ordinal match.",
				Action:   "Prefer a unique data-testid, role, or id selector before production promotion.",
			})
		}
	}
	if step.Regex != "" {
		diagnostics = append(diagnostics, ReplayDiagnostic{
			Code:     "regex_selector_risk",
			Severity: "info",
			Summary:  "The imported descriptor includes a regular expression matcher.",
			Action:   "Replay against representative dynamic content and replace with a stable selector if possible.",
		})
	}
	if hasSelector && selectorConfidence < 70 {
		diagnostics = append(diagnostics, ReplayDiagnostic{
			Code:     "timing_timeout_risk",
			Severity: "warning",
			Summary:  "The highest-confidence selector candidate is below the recommended threshold.",
			Action:   "Add a visible/network-idle wait and select a more stable locator from replay DOM evidence.",
		})
	}
	return diagnostics
}
