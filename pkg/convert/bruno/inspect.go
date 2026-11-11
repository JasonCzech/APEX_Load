package bruno

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
)

var (
	templateVariablePattern = regexp.MustCompile(`\{\{\s*([^{}\s()]+)(?:\([^{}]*\))?\s*\}\}`)
	brunoTemplatePattern    = regexp.MustCompile(`\{\{([^{}\r\n]+)\}\}`)
	bruMetaBlockPattern     = regexp.MustCompile(`(?ms)\bmeta\s*\{(.*?)\}`)
	bruFieldPattern         = regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9_-]*)\s*:\s*(.*?)\s*$`)
	bruSecretLinePattern    = regexp.MustCompile(`(?mi)^(\s*)(username|password|passwd|pwd|pass|token|access[-_]?token|refresh[-_]?token|api[-_]?key|client[-_]?secret|private[-_]?key|secret[-_]?key|access[-_]?key|passphrase|secret)\s*:\s*(.*?)\s*$`)
	bruHeadersBlockPattern  = regexp.MustCompile(`(?ms)^[ \t]*headers(?:\s*:\s*[A-Za-z][A-Za-z0-9_-]*)?\s*\{[ \t]*\r?\n.*?^[ \t]*\}[ \t]*(?:\r?\n|$)`)
	bruHeaderLinePattern    = regexp.MustCompile(`^(\s*)(~?)([^:\r\n]+?)(\s*:\s*)(.*?)(\r?)$`)
)

func Inspect(input Input) (*Result, error) {
	outputMode := strings.ToLower(strings.TrimSpace(input.Options.OutputMode))
	if outputMode == "" {
		outputMode = OutputModeNativeRequests
	}
	switch outputMode {
	case OutputModeNativeRequests:
	case OutputModeCanonical:
		return nil, errors.New(dsl.BrunoRunnerRetiredMessage)
	default:
		return nil, fmt.Errorf("unsupported Bruno output_mode %q", input.Options.OutputMode)
	}
	if outputMode == OutputModeNativeRequests {
		if err := validateNativeInspectInput(input); err != nil {
			return nil, err
		}
	}
	rawFiles, original, err := loadSourceFiles(input)
	if err != nil {
		return nil, err
	}
	// Operating-system files go before the root is chosen, so a __MACOSX copy
	// cannot move it. The collection's own ignore lists apply relative to that
	// root and are read before .gitignore itself is dropped as bookkeeping.
	uploadedFiles := rawFiles
	rawFiles, skippedFiles := filterIgnoredSourceFiles(rawFiles, "", sourceIgnoreRules{})
	root := detectRoot(rawFiles, input.RootPath)
	var ignoredFiles []SkippedFile
	rawFiles, ignoredFiles = filterIgnoredSourceFiles(rawFiles, root, brunoIgnoreRules(uploadedFiles, root))
	skippedFiles = append(skippedFiles, ignoredFiles...)
	sort.Slice(skippedFiles, func(i, j int) bool { return skippedFiles[i].Path < skippedFiles[j].Path })
	skippedFileCount := len(skippedFiles)
	if len(skippedFiles) > maxReportedSkippedFiles {
		skippedFiles = skippedFiles[:maxReportedSkippedFiles]
	}
	if outputMode == OutputModeNativeRequests {
		if err := validateNativeYAMLFiles(rawFiles); err != nil {
			return nil, err
		}
		if err := rejectReservedNativeCredentialMarkers(rawFiles); err != nil {
			return nil, err
		}
	}
	sanitizedFiles, requirements, findings, err := sanitizeFiles(
		rawFiles,
		outputMode == OutputModeNativeRequests,
	)
	if err != nil {
		return nil, err
	}
	rawArchive, err := deterministicTAR(rawFiles)
	if err != nil {
		return nil, fmt.Errorf("build raw Bruno bundle: %w", err)
	}
	sanitizedArchive, err := deterministicTAR(sanitizedFiles)
	if err != nil {
		return nil, fmt.Errorf("build sanitized Bruno bundle: %w", err)
	}
	manifest, err := buildManifest(sanitizedFiles, rawArchive, sanitizedArchive)
	if err != nil {
		return nil, err
	}
	collection, environments, capabilities, missing, sourceFormat, err := inspectCollection(sanitizedFiles, root)
	if err != nil {
		return nil, err
	}
	applyBrunoCollectionDisplayName(&collection, input.Name)
	// Source capabilities remain useful for explaining conversion limits, but
	// native scripts have no JavaScript runner or sandbox requirement.
	capabilities.RequiredSandbox = ""
	var nativeCandidates []NativeScriptCandidate
	nativeRequestPasses := 0
	if outputMode == OutputModeNativeRequests {
		nativeCandidates, requirements, missing, findings, err = nativeRequestScripts(
			input,
			sanitizedFiles,
			root,
			collection,
			manifest,
			requirements,
			findings,
		)
		nativeRequestPasses++
		if err != nil {
			return nil, err
		}
	}
	requirements = finalizeNativeSecretRequirements(requirements)
	capabilities.ConvertedHooks, capabilities.UnconvertedHooks = nativeHookCounts(nativeCandidates)
	// A converted hook runs as a native check, so JavaScript is a conversion
	// limit only when a hook still needs translation or the bundle carries
	// script modules.
	capabilities.HasJavaScript = capabilities.UnconvertedHooks > 0 || capabilities.HasLocalModules ||
		javaScriptWithoutNativeScript(collection.Items, nativeCandidates)
	for index := range nativeCandidates {
		nativeCandidates[index].SecretRequirements = finalizeNativeSecretRequirements(
			nativeCandidates[index].SecretRequirements,
		)
		convertutil.SanitizeScriptDSLTemplates(nativeCandidates[index].Script, nil)
	}
	executable, apps := countItems(collection.Items)
	result := &Result{
		SchemaVersion:      ImporterVersion,
		OutputMode:         outputMode,
		SourceFormat:       sourceFormat,
		RootPath:           root,
		Manifest:           manifest,
		Collection:         collection,
		Environments:       environments,
		Capabilities:       capabilities,
		SecretRequirements: requirements,
		SecurityFindings:   findings,
		Fidelity: FidelityReport{
			Status:          "native_conversion",
			PreservedFiles:  len(rawFiles),
			ExecutableItems: executable,
			PreservedApps:   apps,
			DroppedItems:    0,
			Compatibility:   "native_requests",
			IntentionalExtensions: []string{
				"OpenCollection YAML HTTP requests import as independent native APEX scripts",
			},
		},
		MissingVariables:    missing,
		SkippedFiles:        skippedFiles,
		SkippedFileCount:    skippedFileCount,
		KeptHeaders:         nativeKeptHeaders(nativeCandidates),
		Scripts:             nativeCandidates,
		RawArchive:          rawArchive,
		SanitizedArchive:    sanitizedArchive,
		OriginalUpload:      original,
		InspectedAt:         time.Now().UTC(),
		nativeRequestPasses: nativeRequestPasses,
	}
	return result, nil
}

// nativeHookCounts totals, across candidates, the JavaScript hooks converted to
// native checks and those left for review. A hook inherited from a folder or
// the collection counts once for each request it runs for.
func nativeHookCounts(candidates []NativeScriptCandidate) (int, int) {
	converted, unconverted := 0, 0
	for _, candidate := range candidates {
		if candidate.Script == nil || candidate.Script.Metadata == nil || candidate.Script.Metadata.Import == nil {
			continue
		}
		preserved := candidate.Script.Metadata.Import.Preserved
		converted += preservedCount(preserved["converted_hook_count"])
		unconverted += preservedCount(preserved["review_hook_count"])
	}
	return converted, unconverted
}

// javaScriptWithoutNativeScript reports whether a request item that runs
// JavaScript produced no native script, such as a request type the native
// conversion does not cover. Its hooks were neither converted nor counted.
func javaScriptWithoutNativeScript(items []CollectionItem, candidates []NativeScriptCandidate) bool {
	converted := make(map[string]struct{}, 2*len(candidates))
	for _, candidate := range candidates {
		converted["uid:"+candidate.SourceUID] = struct{}{}
		converted["path:"+candidate.SourcePath] = struct{}{}
	}
	var uncovered func([]CollectionItem) bool
	uncovered = func(items []CollectionItem) bool {
		for _, item := range items {
			if item.Type != "folder" && item.Type != "app" && (item.HasScripts || item.HasTests) {
				_, byUID := converted["uid:"+item.UID]
				_, byPath := converted["path:"+item.Path]
				if !byUID && !byPath {
					return true
				}
			}
			if uncovered(item.Children) {
				return true
			}
		}
		return false
	}
	return uncovered(items)
}

func preservedCount(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case json.Number:
		count, _ := typed.Int64()
		return int(count)
	default:
		return 0
	}
}

func finalizeNativeSecretRequirements(requirements []SecretRequirement) []SecretRequirement {
	out := make([]SecretRequirement, len(requirements))
	for index, requirement := range requirements {
		out[index] = finalizeNativeSecretRequirement(requirement)
	}
	return out
}

func applyBrunoCollectionDisplayName(collection *Collection, requested string) {
	if collection == nil {
		return
	}
	if name := strings.TrimSpace(requested); name != "" {
		// An operator-supplied display name is not the collection's identity.
		collection.Name = name
		collection.NameDeclared = false
	}
	if collection.Name == "" {
		collection.Name = "Imported Bruno Collection"
	}
}

func appendUniqueSecretRequirements(existing, additional []SecretRequirement) []SecretRequirement {
	out := append([]SecretRequirement(nil), existing...)
	positions := make(map[string]int, len(existing)+len(additional))
	for index, requirement := range existing {
		positions[requirement.ID] = index
	}
	for _, requirement := range additional {
		if index, duplicate := positions[requirement.ID]; duplicate {
			out[index] = mergeNativeSecretRequirements(out[index], requirement)
			continue
		}
		positions[requirement.ID] = len(out)
		out = append(out, requirement)
	}
	return out
}

func validateNativeInspectInput(input Input) error {
	if strings.TrimSpace(input.Name) != "" {
		return errors.New("Bruno native_requests does not accept a singular script name override")
	}
	if strings.TrimSpace(input.Sandbox) != "" {
		return fmt.Errorf("%s Native imports do not accept a JavaScript sandbox selection", dsl.BrunoRunnerRetiredMessage)
	}
	options := input.Options
	if strings.TrimSpace(options.DefaultEnvironment) != "" ||
		strings.TrimSpace(options.DefaultGlobalEnvironment) != "" ||
		strings.TrimSpace(options.Selection) != "" ||
		len(options.Paths) > 0 ||
		len(options.IncludeTags) > 0 ||
		len(options.ExcludeTags) > 0 ||
		options.Recursive ||
		strings.TrimSpace(options.Execution) != "" ||
		options.Bail ||
		options.TestsOnly ||
		options.DelayMS != 0 ||
		options.StreamTimeoutMS != 0 ||
		options.PersistSession {
		return fmt.Errorf("%s Native imports do not accept canonical run-selection or tests-only options", dsl.BrunoRunnerRetiredMessage)
	}
	return nil
}

func inspectCollection(files []sourceFile, root string) (Collection, []Environment, Capabilities, []string, string, error) {
	collection := Collection{}
	environments := make([]Environment, 0)
	capabilities := Capabilities{}
	requestTypes := map[string]struct{}{}
	variables := map[string]struct{}{}
	definedVariables := map[string]struct{}{}
	sourceFormats := map[string]struct{}{}
	items := make([]CollectionItem, 0)
	folders := make(map[string]CollectionItem)
	for _, file := range files {
		relative := strings.TrimPrefix(file.Path, strings.TrimSuffix(root, "/")+"/")
		role := classifyFileRole(file.Path, file.Data)
		lower := strings.ToLower(file.Path)
		switch path.Ext(lower) {
		case ".bru":
			sourceFormats["bru"] = struct{}{}
		case ".yml", ".yaml":
			sourceFormats["yml"] = struct{}{}
		}
		if role == "dependency" {
			capabilities.HasNPMDependencies = true
		}
		if role == "script" {
			capabilities.HasJavaScript = true
			capabilities.HasLocalModules = true
		}
		if role == "certificate" {
			capabilities.HasCertificates = true
			if containsPrivateKeyMaterial(file.Path, file.Data) {
				capabilities.HasPrivateKeyFiles = true
			}
		}
		if role == "asset" {
			capabilities.HasFileBodies = true
		}
		for _, match := range templateVariablePattern.FindAllSubmatch(file.Data, -1) {
			if len(match) < 2 {
				continue
			}
			name := strings.TrimSpace(string(match[1]))
			if isBuiltinVariable(name) {
				continue
			}
			variables[name] = struct{}{}
		}
		if role == "environment" {
			name := strings.TrimSuffix(path.Base(file.Path), path.Ext(file.Path))
			environments = append(environments, Environment{Name: name, Path: relative, Type: strings.TrimPrefix(path.Ext(file.Path), "."), Values: environmentVariableValues(file.Data, path.Ext(lower))})
			for _, name := range environmentVariableNames(file.Data, path.Ext(lower)) {
				definedVariables[name] = struct{}{}
			}
			continue
		}
		if role == "collection" {
			if name, uid := collectionIdentity(file.Data, path.Ext(lower)); collection.Name == "" {
				collection.Name, collection.UID = name, uid
				collection.NameDeclared = strings.TrimSpace(name) != ""
			}
			continue
		}
		if role == "folder" {
			folderPath := path.Dir(relative)
			if folderPath == "." {
				folderPath = ""
			}
			folderPath = strings.Trim(folderPath, "/")
			if folderPath != "" {
				folder := parseFolderItem(file, folderPath)
				folders[folderPath] = folder
				if folder.HasScripts || folder.HasTests {
					capabilities.HasJavaScript = true
				}
			}
			continue
		}
		if role != "request" {
			continue
		}
		item, err := parseCollectionItem(file, relative)
		if err != nil {
			return Collection{}, nil, Capabilities{}, nil, "", fmt.Errorf("parse Bruno item %q: %w", file.Path, err)
		}
		if item.Type == "app" {
			capabilities.HasBrunoApps = true
		} else if item.Executable {
			requestTypes[item.Type] = struct{}{}
		}
		if item.HasScripts || item.HasTests {
			capabilities.HasJavaScript = true
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return Collection{}, nil, Capabilities{}, nil, "", fmt.Errorf("no Bruno request or app items were found")
	}
	collection.Items = buildItemTree(items, folders)
	if collection.Name == "" {
		if root == "" && len(items) == 1 {
			collection.Name = items[0].Name
		} else {
			collection.Name = collectionNameFromRoot(root, files)
		}
	}
	for itemType := range requestTypes {
		capabilities.RequestTypes = append(capabilities.RequestTypes, itemType)
	}
	sort.Strings(capabilities.RequestTypes)
	sort.Slice(environments, func(i, j int) bool { return environments[i].Path < environments[j].Path })
	missing := make([]string, 0)
	for variable := range variables {
		if _, ok := definedVariables[variable]; !ok {
			missing = append(missing, variable)
		}
	}
	sort.Strings(missing)
	format := "mixed"
	if len(sourceFormats) == 1 {
		for candidate := range sourceFormats {
			format = candidate
		}
	}
	return collection, environments, capabilities, missing, format, nil
}

func containsPrivateKeyMaterial(filename string, data []byte) bool {
	ext := strings.ToLower(path.Ext(filename))
	if ext == ".key" || ext == ".p12" || ext == ".pfx" {
		return true
	}
	upper := bytes.ToUpper(data)
	return bytes.Contains(upper, []byte("-----BEGIN PRIVATE KEY-----")) ||
		bytes.Contains(upper, []byte("-----BEGIN RSA PRIVATE KEY-----")) ||
		bytes.Contains(upper, []byte("-----BEGIN EC PRIVATE KEY-----")) ||
		bytes.Contains(upper, []byte("-----BEGIN ENCRYPTED PRIVATE KEY-----"))
}

func parseCollectionItem(file sourceFile, relative string) (CollectionItem, error) {
	ext := strings.ToLower(path.Ext(file.Path))
	if ext == ".bru" {
		fields := parseBRUMetaFields(file.Data)
		itemType := normalizeItemType(fields["type"])
		name := fields["name"]
		if name == "" {
			name = strings.TrimSuffix(path.Base(file.Path), ext)
		}
		seq, _ := strconv.Atoi(fields["seq"])
		return CollectionItem{
			UID:        convertutil.FirstNonEmpty(fields["uid"], stableItemID(relative, itemType, name, file.Data)),
			Name:       name,
			Path:       relative,
			Type:       itemType,
			Sequence:   seq,
			Tags:       splitTags(fields["tags"]),
			Executable: itemType != "app",
			HasScripts: bytes.Contains(file.Data, []byte("script:")) || bytes.Contains(file.Data, []byte("script {")),
			HasTests:   bytes.Contains(file.Data, []byte("tests {")) || bytes.Contains(file.Data, []byte("assert {")),
		}, nil
	}
	var value map[string]any
	if err := yaml.Unmarshal(file.Data, &value); err != nil {
		return CollectionItem{}, err
	}
	info, _ := value["info"].(map[string]any)
	name := anyString(info["name"])
	if name == "" {
		name = strings.TrimSuffix(path.Base(file.Path), ext)
	}
	itemType := normalizeItemType(anyString(info["type"]))
	if itemType == "http" {
		for _, candidate := range []string{"graphql", "grpc", "websocket", "ws"} {
			if _, ok := value[candidate]; ok {
				itemType = normalizeItemType(candidate)
				break
			}
		}
	}
	seq := anyInt(info["seq"])
	tags := anyStrings(info["tags"])
	hasScripts, hasTests := structuredRuntimeFlags(value)
	return CollectionItem{
		UID:        convertutil.FirstNonEmpty(anyString(info["uid"]), stableItemID(relative, itemType, name, file.Data)),
		Name:       name,
		Path:       relative,
		Type:       itemType,
		Sequence:   seq,
		Tags:       tags,
		Executable: itemType != "app",
		HasScripts: hasScripts,
		HasTests:   hasTests,
	}, nil
}

func parseFolderItem(file sourceFile, folderPath string) CollectionItem {
	ext := strings.ToLower(path.Ext(file.Path))
	name := path.Base(folderPath)
	uid := ""
	sequence := 0
	var tags []string
	hasScripts := false
	hasTests := false
	if ext == ".bru" {
		fields := parseBRUMetaFields(file.Data)
		name = convertutil.FirstNonEmpty(fields["name"], name)
		uid = fields["uid"]
		sequence, _ = strconv.Atoi(fields["seq"])
		tags = splitTags(fields["tags"])
		hasScripts = bytes.Contains(file.Data, []byte("script:")) || bytes.Contains(file.Data, []byte("script {"))
		hasTests = bytes.Contains(file.Data, []byte("tests {")) || bytes.Contains(file.Data, []byte("assert {"))
	} else {
		var value map[string]any
		if yaml.Unmarshal(file.Data, &value) == nil {
			info, _ := value["info"].(map[string]any)
			name = convertutil.FirstNonEmpty(anyString(info["name"]), name)
			uid = anyString(info["uid"])
			sequence = anyInt(info["seq"])
			tags = anyStrings(info["tags"])
			hasScripts, hasTests = structuredRuntimeFlags(value)
		}
	}
	return CollectionItem{
		UID:        convertutil.FirstNonEmpty(uid, stableItemID(folderPath, "folder", name, file.Data)),
		Name:       name,
		Path:       folderPath,
		Type:       "folder",
		Sequence:   sequence,
		Tags:       tags,
		Executable: false,
		HasScripts: hasScripts,
		HasTests:   hasTests,
	}
}

func parseBRUMetaFields(data []byte) map[string]string {
	fields := map[string]string{}
	match := bruMetaBlockPattern.FindSubmatch(data)
	if len(match) <= 1 {
		return fields
	}
	for _, field := range bruFieldPattern.FindAllSubmatch(match[1], -1) {
		if len(field) > 2 {
			fields[strings.ToLower(string(field[1]))] = strings.Trim(strings.TrimSpace(string(field[2])), `"'`)
		}
	}
	return fields
}

func structuredRuntimeFlags(value map[string]any) (bool, bool) {
	runtime, _ := value["runtime"].(map[string]any)
	scripts, _ := runtime["scripts"].([]any)
	hasScripts := len(scripts) > 0
	hasTests := false
	for _, raw := range scripts {
		script, _ := raw.(map[string]any)
		if strings.Contains(strings.ToLower(anyString(script["type"])), "response") ||
			strings.Contains(anyString(script["code"]), "test(") {
			hasTests = true
		}
	}
	if assertions, ok := runtime["assertions"].([]any); ok && len(assertions) > 0 {
		hasTests = true
	}
	return hasScripts, hasTests
}

func sanitizeFiles(
	files []sourceFile,
	preserveNativeCredentialTemplates bool,
) ([]sourceFile, []SecretRequirement, []SecurityFinding, error) {
	out := make([]sourceFile, 0, len(files))
	requirements := make([]SecretRequirement, 0)
	findings := make([]SecurityFinding, 0)
	seenRequirement := map[string]struct{}{}
	existingReferences, err := discoverExistingSecretReferences(files)
	if err != nil {
		return nil, nil, nil, err
	}
	suggestionNames := newSecretSuggestionRegistry()
	for _, reference := range existingReferences {
		suggestionNames.reserve(reference.Name)
	}
	for _, file := range files {
		content := append([]byte(nil), file.Data...)
		ext := strings.ToLower(path.Ext(file.Path))
		role := classifyFileRole(file.Path, file.Data)
		var current []SecretRequirement
		var sanitizeErr error
		switch {
		case (ext == ".yml" || ext == ".yaml" || ext == ".json") && roleSupportsStructuredSanitization(role):
			content, current, sanitizeErr = sanitizeStructuredFile(
				file.Path,
				file.Data,
				ext,
				suggestionNames,
				preserveNativeCredentialTemplates,
			)
		case ext == ".env" || path.Base(file.Path) == ".env":
			content, current = sanitizeDotEnv(
				file.Path,
				file.Data,
				suggestionNames,
				preserveNativeCredentialTemplates,
			)
		case ext == ".bru":
			content, current = sanitizeBRU(
				file.Path,
				file.Data,
				suggestionNames,
				preserveNativeCredentialTemplates,
			)
		}
		if sanitizeErr != nil {
			return nil, nil, nil, fmt.Errorf("sanitize %q: %w", file.Path, sanitizeErr)
		}
		if supportsExistingSecretReferences(file) {
			content, sanitizeErr = canonicalizeExistingSecretReferences(content)
			if sanitizeErr != nil {
				return nil, nil, nil, fmt.Errorf("canonicalize secret reference in %q: %w", file.Path, sanitizeErr)
			}
		}
		var nativeAliasGroups map[string][]string
		if preserveNativeCredentialTemplates &&
			(ext == ".yml" || ext == ".yaml") {
			nativeAliasGroups, sanitizeErr = nativeYAMLAliasGroups(file.Data)
			if sanitizeErr != nil {
				return nil, nil, nil, fmt.Errorf("index YAML aliases in %q: %w", file.Path, sanitizeErr)
			}
		}
		for _, requirement := range current {
			if _, exists := seenRequirement[requirement.ID]; exists {
				continue
			}
			seenRequirement[requirement.ID] = struct{}{}
			requirements = append(requirements, requirement)
		}
		out = append(out, sourceFile{
			Path:              file.Path,
			Data:              content,
			Mode:              file.Mode,
			nativeAliasGroups: nativeAliasGroups,
		})
	}
	sanitizedValueCount := len(requirements)
	for _, reference := range existingReferences {
		requirement := existingSecretRequirement(reference)
		if _, exists := seenRequirement[requirement.ID]; exists {
			continue
		}
		seenRequirement[requirement.ID] = struct{}{}
		requirements = append(requirements, requirement)
	}
	if sanitizedValueCount > 0 {
		findings = append(findings, SecurityFinding{
			Severity: "warning",
			Code:     "source_credentials_sanitized",
			Summary:  fmt.Sprintf("%d credential value(s) were removed from the executable bundle and require governed secret bindings.", sanitizedValueCount),
		})
	}
	if len(existingReferences) > 0 {
		findings = append(findings, SecurityFinding{
			Severity: "info",
			Code:     "existing_secret_references_reused",
			Summary:  fmt.Sprintf("%d existing governed secret reference location(s) were reused.", len(existingReferences)),
		})
	}
	sort.Slice(requirements, func(i, j int) bool {
		if requirements[i].Path == requirements[j].Path {
			if requirements[i].Key == requirements[j].Key {
				return requirements[i].ID < requirements[j].ID
			}
			return requirements[i].Key < requirements[j].Key
		}
		return requirements[i].Path < requirements[j].Path
	})
	return out, requirements, findings, nil
}

type existingSecretReference struct {
	Name string
	Path string
}

func supportsExistingSecretReferences(file sourceFile) bool {
	ext := strings.ToLower(path.Ext(file.Path))
	if ext == ".bru" || ext == ".env" || path.Base(file.Path) == ".env" {
		return true
	}
	return (ext == ".yml" || ext == ".yaml" || ext == ".json") &&
		roleSupportsStructuredSanitization(classifyFileRole(file.Path, file.Data))
}

func discoverExistingSecretReferences(files []sourceFile) ([]existingSecretReference, error) {
	seen := make(map[string]struct{})
	references := make([]existingSecretReference, 0)
	for _, file := range files {
		if !supportsExistingSecretReferences(file) {
			continue
		}
		for _, match := range brunoTemplatePattern.FindAllSubmatch(file.Data, -1) {
			if len(match) < 2 {
				continue
			}
			name, isSecret, err := canonicalBrunoSecretExpression(string(match[1]))
			if err != nil {
				return nil, fmt.Errorf("Bruno source %q contains an invalid secret reference", file.Path)
			}
			if !isSecret {
				continue
			}
			identity := name + "\x00" + file.Path
			if _, exists := seen[identity]; exists {
				continue
			}
			seen[identity] = struct{}{}
			references = append(references, existingSecretReference{Name: name, Path: file.Path})
		}
	}
	sort.Slice(references, func(i, j int) bool {
		if references[i].Name == references[j].Name {
			return references[i].Path < references[j].Path
		}
		return references[i].Name < references[j].Name
	})
	return references, nil
}

func canonicalizeExistingSecretReferences(data []byte) ([]byte, error) {
	var firstErr error
	content := brunoTemplatePattern.ReplaceAllStringFunc(string(data), func(template string) string {
		if firstErr != nil {
			return template
		}
		match := brunoTemplatePattern.FindStringSubmatch(template)
		if len(match) < 2 {
			return template
		}
		name, isSecret, err := canonicalBrunoSecretExpression(match[1])
		if err != nil {
			firstErr = err
			return template
		}
		if !isSecret {
			return template
		}
		return "{{secret." + name + "}}"
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return []byte(content), nil
}

func canonicalBrunoSecretExpression(expression string) (string, bool, error) {
	expression = strings.TrimSpace(expression)
	const prefix = "secret."
	if len(expression) < len(prefix) || !strings.EqualFold(expression[:len(prefix)], prefix) {
		return "", false, nil
	}
	name, err := engine.NormalizeLogicalSecretName(strings.TrimSpace(expression[len(prefix):]))
	if err != nil {
		return "", true, err
	}
	return name, true, nil
}

func existingSecretRequirement(reference existingSecretReference) SecretRequirement {
	idHash := sha256.Sum256([]byte("existing-secret-reference\x00" + reference.Name + "\x00" + reference.Path))
	return SecretRequirement{
		ID:                hex.EncodeToString(idHash[:12]),
		Path:              reference.Path,
		Key:               "secret." + reference.Name,
		SuggestedName:     reference.Name,
		Replacement:       "{{secret." + reference.Name + "}}",
		Context:           "Existing Bruno secret reference",
		ExistingReference: true,
	}
}

func roleSupportsStructuredSanitization(role string) bool {
	switch role {
	case "collection", "folder", "environment", "request":
		return true
	default:
		return false
	}
}

func sanitizeStructuredFile(
	filename string,
	data []byte,
	ext string,
	suggestionNames *secretSuggestionRegistry,
	preserveNativeCredentialTemplates bool,
) ([]byte, []SecretRequirement, error) {
	if ext == ".json" {
		return sanitizeJSONFile(filename, data, suggestionNames, preserveNativeCredentialTemplates)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, nil, err
	}
	const maxAliasDepth = 32
	const maxAliasExpansions = 4096
	aliasExpansions := 0
	var walk func(*yaml.Node, int, map[*yaml.Node]struct{}) error
	walk = func(current *yaml.Node, aliasDepth int, aliasStack map[*yaml.Node]struct{}) error {
		if current == nil {
			return errors.New("Bruno YAML contains an empty node")
		}
		if current.Kind == yaml.AliasNode {
			if current.Alias == nil {
				return errors.New("Bruno YAML contains an unresolved alias")
			}
			if aliasDepth >= maxAliasDepth {
				return fmt.Errorf("Bruno YAML alias expansion exceeds depth %d", maxAliasDepth)
			}
			aliasExpansions++
			if aliasExpansions > maxAliasExpansions {
				return fmt.Errorf("Bruno YAML alias expansion exceeds limit %d", maxAliasExpansions)
			}
			if _, cycle := aliasStack[current.Alias]; cycle {
				return errors.New("Bruno YAML contains a cyclic alias")
			}
			aliasStack[current.Alias] = struct{}{}
			err := walk(current.Alias, aliasDepth+1, aliasStack)
			delete(aliasStack, current.Alias)
			return err
		}
		for _, child := range current.Content {
			if err := walk(child, aliasDepth, aliasStack); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(&node, 0, map[*yaml.Node]struct{}{}); err != nil {
		return nil, nil, err
	}
	return append([]byte(nil), data...), nil, nil
}

func sanitizeJSONFile(
	_ string,
	data []byte,
	_ *secretSuggestionRegistry,
	_ bool,
) ([]byte, []SecretRequirement, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, nil, err
	}
	return append([]byte(nil), data...), nil, nil
}

func structuredScalarString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64), true
	case json.Number:
		return typed.String(), true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return "", false
	}
}

func sanitizeDotEnv(
	_ string,
	data []byte,
	_ *secretSuggestionRegistry,
	_ bool,
) ([]byte, []SecretRequirement) {
	return append([]byte(nil), data...), nil
}

func sanitizeBRU(
	_ string,
	data []byte,
	_ *secretSuggestionRegistry,
	_ bool,
) ([]byte, []SecretRequirement) {
	return append([]byte(nil), data...), nil
}

type secretSuggestionRegistry struct {
	counts map[string]int
	used   map[string]struct{}
	// legacy replays the path-prefixed names an earlier importer version
	// gave these secrets, so a project that configured one can keep it.
	legacy *secretSuggestionRegistry
}

func newSecretSuggestionRegistry() *secretSuggestionRegistry {
	return &secretSuggestionRegistry{
		counts: make(map[string]int),
		used:   make(map[string]struct{}),
		legacy: &secretSuggestionRegistry{counts: make(map[string]int), used: make(map[string]struct{})},
	}
}

func (registry *secretSuggestionRegistry) reserve(name string) {
	if registry == nil || strings.TrimSpace(name) == "" {
		return
	}
	registry.used[name] = struct{}{}
	if registry.counts[name] == 0 {
		registry.counts[name] = 1
	}
	registry.legacy.reserve(name)
}

func (registry *secretSuggestionRegistry) claim(base string) string {
	if registry == nil {
		return base
	}
	count := registry.counts[base]
	if count < 1 {
		count = 1
	}
	for {
		candidate := base
		if count > 1 {
			suffix := "_" + strconv.Itoa(count)
			prefixBytes := credential.MaxLogicalNameBytes - len(suffix)
			if prefixBytes < 1 {
				candidate = "S" + suffix
			} else {
				prefix := strings.TrimRight(base[:min(len(base), prefixBytes)], "_")
				if prefix == "" {
					prefix = "S"
				}
				candidate = prefix + suffix
			}
		}
		if _, exists := registry.used[candidate]; !exists {
			registry.used[candidate] = struct{}{}
			registry.counts[base] = count + 1
			return candidate
		}
		count++
	}
}

func newSecretRequirement(
	suggestionNames *secretSuggestionRegistry,
	filename string,
	identity string,
	keyPath string,
	suggestionKey string,
	value string,
) SecretRequirement {
	// A readable name from the field alone (PASSWORD, AUTHORIZATION), numbered
	// when taken; the path-prefixed name earlier versions gave stays as
	// LegacyName.
	legacyName := secretName(filename, suggestionKey)
	if suggestionNames != nil {
		legacyName = suggestionNames.legacy.claim(legacyName)
	}
	readable := readableNativeSecretBase(strings.TrimPrefix(strings.TrimPrefix(suggestionKey, "header_"), "body_"))
	if readable == "" {
		readable = secretName(filename, suggestionKey)
	}
	suggested := suggestionNames.claim(readable)
	idHash := sha256.Sum256([]byte(filename + "\x00" + identity))
	value = recoverableBrunoCredentialValue(value)
	requirement := SecretRequirement{
		ID:            hex.EncodeToString(idHash[:12]),
		Path:          filename,
		Key:           keyPath,
		SuggestedName: suggested,
		Replacement:   "{{secret." + suggested + "}}",
		Context:       "Bruno source credential",
		Value:         value,
		AffectedPaths: nativeSecretRequirementPaths(filename),
	}
	if legacyName != suggested {
		requirement.LegacyName = legacyName
	}
	requirement.NameBase = readable
	if headerName, isHeader := strings.CutPrefix(suggestionKey, "header_"); isHeader {
		requirement = describeNativeHeaderSecretRequirement(requirement, headerName, value)
	}
	return requirement
}

// recoverableBrunoCredentialValue returns the authored credential value verbatim
// so literal values — including literal "[REDACTED]" placeholders — are preserved
// without blanking during import.
func recoverableBrunoCredentialValue(value string) string {
	return value
}

func containsBrunoCredentialRedactionSentinel(value string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(scrub.Redacted))
}

func decodeBrunoCredentialValueToStable(
	value string,
	decode func(string) (string, error),
) (string, bool) {
	current := value
	const maxDecodeDepth = 32
	for range maxDecodeDepth {
		decoded, err := decode(current)
		if err != nil {
			return current, false
		}
		if decoded == current {
			return current, true
		}
		current = decoded
	}
	return current, false
}

func secretName(filename, key string) string {
	withoutExtension := strings.TrimSuffix(filename, path.Ext(filename))
	segments := strings.Split(strings.Trim(withoutExtension, "/"), "/")
	if len(segments) > 3 {
		segments = segments[len(segments)-3:]
	}
	raw := strings.ToUpper(strings.Join(append(segments, key), "_"))
	var out strings.Builder
	lastUnderscore := false
	for _, r := range raw {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore {
			out.WriteByte('_')
			lastUnderscore = true
		}
	}
	name := strings.Trim(out.String(), "_")
	if name == "" {
		name = "BRUNO_SECRET"
	}
	if len(name) <= credential.MaxLogicalNameBytes {
		return name
	}
	digest := sha256.Sum256([]byte(raw))
	suffix := "_" + strings.ToUpper(hex.EncodeToString(digest[:6]))
	prefix := strings.TrimRight(name[:credential.MaxLogicalNameBytes-len(suffix)], "_")
	return prefix + suffix
}

func isSecretKey(key string, context []string, role string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(key))
	credentialContext := role == "environment" ||
		isStructuredRequestBodyContext(context, role) ||
		isStructuredVariableContext(context, role)
	if isStructuredHeaderContext(context, role) && isStructuredSensitiveHeader(key) {
		return true
	}
	for _, parent := range context {
		normalizedParent := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(parent))
		if strings.Contains(normalizedParent, "auth") ||
			strings.Contains(normalizedParent, "credential") ||
			strings.Contains(normalizedParent, "proxy") ||
			strings.Contains(normalizedParent, "oauth") ||
			strings.Contains(normalizedParent, "certificate") {
			credentialContext = true
			break
		}
	}
	if scrub.IsCredentialField(key) || scrub.SensitiveName(key) {
		return credentialContext
	}
	switch normalized {
	case "password", "passwd", "token", "accesstoken", "refreshtoken", "apikey", "clientsecret", "privatekey", "passphrase", "secret":
		return credentialContext
	case "username":
		return credentialContext
	}
	return false
}

func isStructuredCredentialName(name string, context []string, role string) bool {
	if scrub.IsCredentialField(name) || scrub.SensitiveName(name) {
		return true
	}
	return isStructuredHeaderContext(context, role) && isStructuredSensitiveHeader(name)
}

func isStructuredSensitiveHeader(name string) bool {
	// Cookies and session, anti-forgery and correlation headers are recorded
	// state, not credentials, so they stay as imported.
	if !credential.ImportCredentialHeader(name) {
		return false
	}
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.TrimSpace(name)))
	for _, suffix := range []string{
		"authorization", "apikey", "authtoken", "accesstoken", "securitytoken",
		"clientsecret", "privatekey", "password",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func isGovernedSecretHeaderValue(name, value string, context []string, role string) bool {
	if !isStructuredHeaderContext(context, role) ||
		(!isStructuredSensitiveHeader(name) && !nativeSensitiveHeaderName(name)) {
		return false
	}
	value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
	if isExactGovernedSecretReference(value) {
		return true
	}
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.TrimSpace(name)))
	switch normalized {
	case "authorization", "proxyauthorization":
		fields := strings.Fields(value)
		if len(fields) != 2 {
			return false
		}
		switch strings.ToLower(fields[0]) {
		case "bearer", "basic", "token", "digest":
			return isExactGovernedSecretReference(fields[1])
		default:
			return false
		}
	case "cookie", "setcookie":
		parts := strings.Split(value, ";")
		if len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(pair) != 2 || strings.TrimSpace(pair[0]) == "" || !isExactGovernedSecretReference(pair[1]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// isDeferredNativeCredentialHeaderValue keeps every template-bearing sensitive
// header out of the generic source sanitizer. Native conversion can then
// resolve and scrub its dependencies before deciding whether protocol syntax
// may remain outside the governed secret.
func isDeferredNativeCredentialHeaderValue(name, value string, context []string, role string) bool {
	if !isStructuredHeaderContext(context, role) ||
		(!isStructuredSensitiveHeader(name) && !nativeSensitiveHeaderName(name)) {
		return false
	}
	return brunoTemplatePattern.MatchString(value)
}

func isDeferredNativeCredentialValue(name, value string, context []string, role string) bool {
	if !brunoTemplatePattern.MatchString(value) {
		return false
	}
	if isDeferredNativeCredentialHeaderValue(name, value, context, role) {
		return true
	}
	return isSecretKey(name, context, role)
}

// isNativeCredentialHeaderSyntaxTemplate recognizes the narrow forms whose
// non-template bytes are protocol syntax rather than credential material.
func isNativeCredentialHeaderSyntaxTemplate(name, value string, context []string, role string) bool {
	if !isStructuredHeaderContext(context, role) ||
		(!isStructuredSensitiveHeader(name) && !nativeSensitiveHeaderName(name)) {
		return false
	}
	value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.TrimSpace(name)))
	switch normalized {
	case "authorization", "proxyauthorization":
		fields := strings.Fields(value)
		if len(fields) != 2 {
			return false
		}
		switch strings.ToLower(fields[0]) {
		case "bearer", "basic", "token", "digest":
			return isExactOrdinaryBrunoVariableReference(fields[1])
		default:
			return false
		}
	case "cookie", "setcookie":
		parts := strings.Split(value, ";")
		if len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(pair) != 2 || strings.TrimSpace(pair[0]) == "" ||
				!isExactOrdinaryBrunoVariableReference(pair[1]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isExactOrdinaryBrunoVariableReference(value string) bool {
	value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
	match := brunoTemplatePattern.FindStringSubmatch(value)
	if len(match) < 2 || match[0] != value {
		return false
	}
	expression := strings.TrimSpace(match[1])
	if expression == "" || strings.Contains(expression, "(") ||
		!nativeTemplateNamePattern.MatchString(expression) {
		return false
	}
	lower := strings.ToLower(expression)
	for _, prefix := range []string{"env.", "data.", "var.", "param.", "secret.", "fn.", "fake.", "file."} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
}

func isExactGovernedSecretReference(value string) bool {
	value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
	match := brunoTemplatePattern.FindStringSubmatch(value)
	if len(match) < 2 || match[0] != value {
		return false
	}
	_, isSecret, err := canonicalBrunoSecretExpression(match[1])
	return isSecret && err == nil
}

func structuredSecretSuggestionKey(key string, context []string, role string) string {
	if isStructuredHeaderContext(context, role) {
		return "header_" + key
	}
	if isStructuredRequestBodyContext(context, role) {
		return "body_" + key
	}
	return key
}

func isStructuredHeaderContext(context []string, role string) bool {
	if role != "request" && role != "folder" && role != "collection" {
		return false
	}
	for _, parent := range context {
		switch strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(parent)) {
		case "header", "headers":
			return true
		}
	}
	return false
}

func isStructuredRequestBodyContext(context []string, role string) bool {
	if role != "request" {
		return false
	}
	for _, parent := range context {
		if strings.EqualFold(strings.TrimSpace(parent), "body") {
			return true
		}
	}
	return false
}

func isStructuredVariableContext(context []string, role string) bool {
	if role != "request" && role != "folder" && role != "collection" {
		return false
	}
	for _, parent := range context {
		switch strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(parent)) {
		case "var", "vars", "variable", "variables":
			return true
		}
	}
	return false
}

func shouldSanitizeValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || isExactBrunoTemplateValue(value) {
		return false
	}
	lower := strings.ToLower(value)
	return lower != "null" && lower != "none" && lower != "inherit"
}

// shouldSanitizeCredentialValue differs from the broad source-value heuristic:
// Bruno's words "none", "null", and "inherit" are configuration sentinels only
// where the schema says they are. Inside a password, token, sensitive header,
// URL credential, or body credential field they are literal wire values and
// must be governed like any other credential.
func shouldSanitizeCredentialValue(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !isExactBrunoTemplateValue(value)
}

func nativeCredentialContainerContext(context []string) bool {
	for _, parent := range context {
		if nativeJSONCredentialContainerKey(parent) {
			return true
		}
	}
	return false
}

func structuredNamedCredential(value map[string]any) string {
	for key, raw := range value {
		if !strings.EqualFold(strings.TrimSpace(key), "name") {
			continue
		}
		name, _ := raw.(string)
		return strings.TrimSpace(name)
	}
	return ""
}

func isExactBrunoTemplateValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	location := templateVariablePattern.FindStringIndex(value)
	return len(location) == 2 && location[0] == 0 && location[1] == len(value)
}

func detectRoot(files []sourceFile, requested string) string {
	requested = strings.Trim(strings.ReplaceAll(strings.TrimSpace(requested), "\\", "/"), "/")
	if requested != "" {
		return requested
	}
	candidates := make([]string, 0)
	for _, file := range files {
		switch strings.ToLower(path.Base(file.Path)) {
		case "opencollection.yml", "opencollection.yaml", "collection.bru", "bruno.json":
			dir := path.Dir(file.Path)
			if dir == "." {
				dir = ""
			}
			candidates = append(candidates, dir)
		}
	}
	if len(candidates) > 0 {
		sort.Slice(candidates, func(i, j int) bool {
			leftDepth := strings.Count(candidates[i], "/")
			rightDepth := strings.Count(candidates[j], "/")
			if leftDepth == rightDepth {
				return candidates[i] < candidates[j]
			}
			return leftDepth < rightDepth
		})
		return candidates[0]
	}
	if len(files) == 1 {
		return ""
	}
	return commonDirectory(files)
}

func commonDirectory(files []sourceFile) string {
	if len(files) == 0 {
		return ""
	}
	parts := strings.Split(path.Dir(files[0].Path), "/")
	if len(parts) == 1 && parts[0] == "." {
		return ""
	}
	for _, file := range files[1:] {
		next := strings.Split(path.Dir(file.Path), "/")
		count := min(len(parts), len(next))
		index := 0
		for index < count && parts[index] == next[index] {
			index++
		}
		parts = parts[:index]
	}
	return strings.Trim(strings.Join(parts, "/"), ".")
}

type collectionTreeNode struct {
	item     CollectionItem
	children map[string]*collectionTreeNode
}

func buildItemTree(items []CollectionItem, folders map[string]CollectionItem) []CollectionItem {
	root := &collectionTreeNode{children: make(map[string]*collectionTreeNode)}
	nodes := make(map[string]*collectionTreeNode, len(items)+len(folders))
	var ensureFolder func(string) *collectionTreeNode
	ensureFolder = func(folderPath string) *collectionTreeNode {
		folderPath = strings.Trim(strings.TrimSpace(folderPath), "/")
		if folderPath == "" || folderPath == "." {
			return root
		}
		if existing := nodes[folderPath]; existing != nil {
			return existing
		}
		parentPath := path.Dir(folderPath)
		if parentPath == "." {
			parentPath = ""
		}
		parent := ensureFolder(parentPath)
		name := path.Base(folderPath)
		item, ok := folders[folderPath]
		if !ok {
			item = CollectionItem{
				UID:  stableItemID(folderPath, "folder", name, nil),
				Name: name,
				Path: folderPath,
				Type: "folder",
			}
		}
		node := &collectionTreeNode{item: item, children: make(map[string]*collectionTreeNode)}
		nodes[folderPath] = node
		parent.children[folderPath] = node
		return node
	}
	for folderPath := range folders {
		ensureFolder(folderPath)
	}
	for folderPath, item := range folders {
		nodes[folderPath].item = item
	}
	for _, item := range items {
		parentPath := path.Dir(item.Path)
		if parentPath == "." {
			parentPath = ""
		}
		parent := ensureFolder(parentPath)
		parent.children[item.Path] = &collectionTreeNode{item: item, children: make(map[string]*collectionTreeNode)}
	}
	var flatten func(*collectionTreeNode) []CollectionItem
	flatten = func(parent *collectionTreeNode) []CollectionItem {
		children := make([]CollectionItem, 0, len(parent.children))
		for _, node := range parent.children {
			item := node.item
			item.Children = flatten(node)
			children = append(children, item)
		}
		sort.Slice(children, func(i, j int) bool {
			if children[i].Sequence != children[j].Sequence {
				if children[i].Sequence == 0 {
					return false
				}
				if children[j].Sequence == 0 {
					return true
				}
				return children[i].Sequence < children[j].Sequence
			}
			return children[i].Path < children[j].Path
		})
		return children
	}
	return flatten(root)
}

func countItems(items []CollectionItem) (executable, apps int) {
	for _, item := range items {
		if item.Type == "app" {
			apps++
		} else if item.Executable {
			executable++
		}
		childExecutable, childApps := countItems(item.Children)
		executable += childExecutable
		apps += childApps
	}
	return executable, apps
}

func collectionIdentity(data []byte, ext string) (string, string) {
	if ext == ".bru" {
		fields := map[string]string{}
		if match := bruMetaBlockPattern.FindSubmatch(data); len(match) > 1 {
			for _, field := range bruFieldPattern.FindAllSubmatch(match[1], -1) {
				if len(field) > 2 {
					fields[strings.ToLower(string(field[1]))] = strings.Trim(strings.TrimSpace(string(field[2])), `"'`)
				}
			}
		}
		return fields["name"], fields["uid"]
	}
	var value map[string]any
	if yaml.Unmarshal(data, &value) != nil {
		return "", ""
	}
	info, _ := value["info"].(map[string]any)
	return anyString(info["name"]), anyString(info["uid"])
}

func collectionNameFromRoot(root string, files []sourceFile) string {
	if root != "" {
		return path.Base(root)
	}
	if len(files) == 1 {
		return strings.TrimSuffix(path.Base(files[0].Path), path.Ext(files[0].Path))
	}
	return "Imported Bruno Collection"
}

// brunoRequestContentCache memoizes the structured branch of
// isBrunoRequestContent. classifyFileRole consults it from more than a dozen
// call sites per import (every sanitize pass, both manifest builds, and the
// native validation passes), so an uncached parse turns a large collection into
// tens of redundant yaml.Unmarshal calls per request file. The answer is a pure
// function of the file bytes once the extension has selected the structured
// branch, so a content-hash memo is exact.
var brunoRequestContentCache = struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]bool
	// misses counts the parses actually performed. It exists only for importer
	// performance regression tests and is never read in production.
	misses int
}{entries: make(map[[sha256.Size]byte]bool)}

// maxBrunoRequestContentCacheEntries bounds the memo so a long-lived controller
// cannot accumulate an entry for every file it has ever inspected.
const maxBrunoRequestContentCacheEntries = 8192

func isBrunoRequestContent(data []byte, extension string) bool {
	extension = strings.ToLower(extension)
	if extension == ".bru" {
		return bruMetaBlockPattern.Match(data)
	}
	if extension != ".yml" && extension != ".yaml" && extension != ".json" {
		return false
	}
	// .yml, .yaml and .json share one parse, so the content alone identifies the
	// answer and the extension does not belong in the key.
	key := sha256.Sum256(data)
	brunoRequestContentCache.mu.Lock()
	cached, hit := brunoRequestContentCache.entries[key]
	brunoRequestContentCache.mu.Unlock()
	if hit {
		return cached
	}
	result := parseBrunoRequestContent(data)
	brunoRequestContentCache.mu.Lock()
	if len(brunoRequestContentCache.entries) >= maxBrunoRequestContentCacheEntries {
		brunoRequestContentCache.entries = make(map[[sha256.Size]byte]bool, maxBrunoRequestContentCacheEntries)
	}
	brunoRequestContentCache.entries[key] = result
	brunoRequestContentCache.misses++
	brunoRequestContentCache.mu.Unlock()
	return result
}

func parseBrunoRequestContent(data []byte) bool {
	var value map[string]any
	if yaml.Unmarshal(data, &value) != nil {
		return false
	}
	if _, ok := value["info"]; !ok {
		return false
	}
	for _, key := range []string{"http", "graphql", "grpc", "ws", "websocket", "app"} {
		if _, ok := value[key]; ok {
			return true
		}
	}
	info, _ := value["info"].(map[string]any)
	return normalizeItemType(anyString(info["type"])) != "http" || anyString(info["type"]) != ""
}

func environmentVariableNames(data []byte, extension string) []string {
	if extension == ".env" {
		entries := parseDotEnvEntries(data)
		names := make([]string, 0, len(entries)*2)
		seen := make(map[string]struct{}, len(entries)*2)
		for _, entry := range entries {
			for _, candidate := range []string{entry.Name, "process.env." + entry.Name} {
				if _, exists := seen[candidate]; exists {
					continue
				}
				seen[candidate] = struct{}{}
				names = append(names, candidate)
			}
		}
		return names
	}
	if extension == ".bru" {
		names := map[string]struct{}{}
		for _, block := range parseBruEnvBlocks(data) {
			for _, field := range block.fields {
				names[field.name] = struct{}{}
			}
		}
		for name := range parseBruFlaggedVarNames(data) {
			names[name] = struct{}{}
		}
		out := make([]string, 0, len(names))
		for name := range names {
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}
	var value any
	if yaml.Unmarshal(data, &value) != nil {
		return nil
	}
	names := map[string]struct{}{}
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if name := anyString(typed["name"]); name != "" {
				if _, hasValue := typed["value"]; hasValue {
					names[name] = struct{}{}
				}
			}
			for key, child := range typed {
				if strings.EqualFold(key, "variables") || strings.EqualFold(key, "vars") {
					if varMap, ok := child.(map[string]any); ok && !(anyString(varMap["name"]) != "" && varMap["value"] != nil) {
						for varKey, varRaw := range varMap {
							trimmed := strings.TrimSpace(varKey)
							if trimmed == "" {
								continue
							}
							if nativeScalarValue(varRaw) {
								names[trimmed] = struct{}{}
								continue
							}
							if entryMap, ok := varRaw.(map[string]any); ok && anyString(entryMap["name"]) == "" {
								if _, hasVal := entryMap["value"]; hasVal {
									names[trimmed] = struct{}{}
								}
							}
						}
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func normalizeItemType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "graphql", "graphql-request":
		return "graphql"
	case "grpc", "grpc-request":
		return "grpc"
	case "ws", "websocket", "ws-request", "websocket-request":
		return "websocket"
	case "app", "bruno-app":
		return "app"
	default:
		return "http"
	}
}

func stableItemID(relative, itemType, name string, data []byte) string {
	fingerprint := append([]byte(nil), data...)
	switch strings.ToLower(path.Ext(relative)) {
	case ".bru":
		fingerprint = bytes.TrimSpace(bruMetaBlockPattern.ReplaceAll(fingerprint, nil))
	case ".yml", ".yaml":
		var value map[string]any
		if yaml.Unmarshal(fingerprint, &value) == nil {
			if info, ok := value["info"].(map[string]any); ok {
				delete(info, "name")
				delete(info, "uid")
				delete(info, "seq")
			}
			if encoded, err := json.Marshal(value); err == nil {
				fingerprint = encoded
			}
		}
	}
	if len(bytes.TrimSpace(fingerprint)) == 0 {
		fingerprint = []byte(name)
	}
	hash := sha256.Sum256(append([]byte(itemType+"\x00"), fingerprint...))
	return hex.EncodeToString(hash[:16])
}

func isBuiltinVariable(name string) bool {
	for _, prefix := range []string{"fn.", "fake.", "process.", "secret.", "file."} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func splitTags(value string) []string {
	value = strings.Trim(value, "[]")
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.Trim(strings.TrimSpace(item), `"'`)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func anyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

func anyInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		result, _ := strconv.Atoi(typed)
		return result
	default:
		return 0
	}
}

func anyStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if value := strings.TrimSpace(anyString(item)); value != "" {
				out = append(out, value)
			}
		}
		return out
	case string:
		return splitTags(typed)
	default:
		return nil
	}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
