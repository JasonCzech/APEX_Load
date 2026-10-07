package bruno

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"mime/multipart"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	nativeImporterVersion  = "apexload-bruno-native-importer"
	nativeMaxTimeoutMS     = dsl.MaxConfiguredProtocolTimeoutMS
	nativeMaxRedirects     = 50
	nativeDefaultTimeoutMS = 30_000
	nativeDefaultRedirects = 5
)

var (
	nativeJSTestStartPattern  = regexp.MustCompile(`^test\s*\(\s*(?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*,\s*(?:function\s*\(\s*\)|\(\s*\)\s*=>)\s*\{\s*$`)
	nativeJSTestEndPattern    = regexp.MustCompile(`^\}\s*\)\s*;?\s*$`)
	nativeJSBodyPattern       = regexp.MustCompile(`^const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*res\.getBody\(\)\s*;?\s*$`)
	nativeJSStatusPattern     = regexp.MustCompile(`^expect\s*\(\s*res\.getStatus\(\)\s*\)\.to\.equal\(\s*([0-9]+)\s*\)\s*;?\s*$`)
	nativeJSHeaderPattern     = regexp.MustCompile(`^expect\s*\(\s*res\.getHeader\(\s*((?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*'))(?:\.toLowerCase\(\))?\s*\)\s*\)\.to\.include\(\s*((?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*'))\s*\)\s*;?\s*$`)
	nativeJSRegexPattern      = regexp.MustCompile(`^const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*([A-Za-z_$][A-Za-z0-9_$]*)\.match\(\s*/((?:\\/|[^/\r\n])*)/\s*\)\s*;?\s*$`)
	nativeJSCapturePattern    = regexp.MustCompile(`^const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\?\s*([A-Za-z_$][A-Za-z0-9_$]*)\[1\]\s*:\s*null\s*;?\s*$`)
	nativeJSEqualsPattern     = regexp.MustCompile(`^expect\s*\(\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\)\.to\.equal\(\s*((?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*'))\s*\)\s*;?\s*$`)
	nativeJSOneLineStatus     = regexp.MustCompile(`^test\s*\(\s*(?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*,\s*\(\s*\)\s*=>\s*expect\s*\(\s*res\.getStatus\(\)\s*\)\.to\.equal\(\s*([0-9]+)\s*\)\s*\)\s*;?\s*$`)
	nativeTemplateNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	nativeSOAPBodyPattern     = regexp.MustCompile(`(?is)<(?:[A-Za-z_][\w.-]*:)?Body\b[^>]*>\s*<(?:[A-Za-z_][\w.-]*:)?([A-Za-z_][\w.-]*)\b`)
	nativeCamelCaseBoundary   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

type nativeHeader struct {
	Name          string
	Value         string
	Disabled      bool
	ValueIsString bool
	SourcePath    string
	Pointer       string
}

type nativeVariable struct {
	Name              string
	Value             string
	ValueIsString     bool
	Disabled          bool
	SourcePath        string
	DefinitionPointer string
}

type nativeHook struct {
	Type string
	Code string
	Path string
}

type nativeAuth struct {
	Present          bool
	Mode             string
	Config           dsl.AuthConfig
	CredentialFields map[string]bool
	SourcePath       string
	Pointer          string
}

type nativeSettings struct {
	EncodeURL       *bool
	TimeoutMS       *int
	FollowRedirects *bool
	MaxRedirects    *int
}

type nativeLayer struct {
	Path      string
	Name      string
	UID       string
	Sequence  int
	Headers   []nativeHeader
	Variables []nativeVariable
	Auth      nativeAuth
	Hooks     []nativeHook
	Settings  nativeSettings
}

type nativeEffective struct {
	Headers   []nativeHeader
	Variables []nativeVariable
	Auth      nativeAuth
	Hooks     []nativeHook
	Settings  nativeSettings
}

type nativeTemplateContext int

const (
	nativeTemplateOrdinary nativeTemplateContext = iota
	nativeTemplateBody
	nativeTemplateAuthUsername
	nativeTemplateAuthPassword
	nativeTemplateAuthToken
	nativeTemplateSensitiveHeader
	nativeTemplateXMLCredential
)

type nativeTemplateState struct {
	sourcePath      string
	defined         map[string]nativeVariable
	usedParams      map[string]struct{}
	parameterValues map[string]string
	missing         map[string]struct{}
	generatedReq    map[string]SecretRequirement
	generatedErr    error
	resolved        map[string]nativeVariableResolution
	resolving       map[string]int
	resolutionStack []string
}

type nativeVariableResolution struct {
	Value        string
	Complete     bool
	RuntimeBound bool
	Dependencies []nativeVariable
	Missing      []string
}

type nativeHTTPParam struct {
	Name        string
	Value       string
	Type        string
	Disabled    bool
	SourceIndex int
}

// nativeLayerCache memoizes the parsed collection/folder metadata layers of a
// single import pass. Without it every request re-parses each of its ancestor
// folder.yml documents, so metadata parsing is O(requests x depth): a
// 3,000-request collection nested four folders deep parses the same handful of
// folder documents ~12,000 times per pass. Downstream consumers treat
// nativeLayer as immutable — mergeNativeLayers only reads it and
// buildNativeCandidate only reads Path/UID — so handing the same parsed value
// to every request is safe. Request layers are deliberately not cached: callers
// overwrite their Name/UID/Sequence fallbacks and each request file is parsed
// once per pass anyway. Keying on the path alone is exact because the cache is
// only ever fed from nativeMetadataFiles, which resolves each directory to a
// single collection/folder document and rejects ambiguous ones.
type nativeLayerCache struct {
	entries map[string]nativeLayer
}

func newNativeLayerCache() *nativeLayerCache {
	return &nativeLayerCache{entries: make(map[string]nativeLayer)}
}

func (c *nativeLayerCache) metadataLayer(file sourceFile) (nativeLayer, error) {
	if c == nil {
		return parseNativeLayer(file, false)
	}
	if cached, ok := c.entries[file.Path]; ok {
		return cached, nil
	}
	layer, err := parseNativeLayer(file, false)
	if err != nil {
		return nativeLayer{}, err
	}
	c.entries[file.Path] = layer
	return layer, nil
}

func validateNativeEffectiveCredentialShapes(files []sourceFile, root string) error {
	collectionFile, folderFiles, err := nativeMetadataFiles(files, root)
	if err != nil {
		return err
	}
	layerCache := newNativeLayerCache()
	var collectionLayer nativeLayer
	if collectionFile != nil {
		collectionLayer, err = layerCache.metadataLayer(*collectionFile)
		if err != nil {
			return fmt.Errorf("parse Bruno collection metadata %q: %w", collectionFile.Path, err)
		}
	}
	for _, file := range files {
		if classifyFileRole(file.Path, file.Data) != "request" {
			continue
		}
		relative, ok := nativeRelativePath(file.Path, root)
		if !ok {
			continue
		}
		requestLayer, parseErr := parseNativeLayer(file, true)
		if parseErr != nil {
			return fmt.Errorf("parse Bruno request %q: %w", file.Path, parseErr)
		}
		layers := make([]nativeLayer, 0, 2+strings.Count(relative, "/"))
		if collectionFile != nil {
			layers = append(layers, collectionLayer)
		}
		for _, ancestor := range nativeAncestorPaths(path.Dir(relative)) {
			fullPath := ancestor
			if root != "" {
				fullPath = path.Join(root, ancestor)
			}
			folderFile, exists := folderFiles[fullPath]
			if !exists {
				continue
			}
			folderLayer, folderErr := layerCache.metadataLayer(folderFile)
			if folderErr != nil {
				return fmt.Errorf("parse Bruno folder metadata %q: %w", folderFile.Path, folderErr)
			}
			layers = append(layers, folderLayer)
		}
		layers = append(layers, requestLayer)
		effective, _ := mergeNativeLayers(layers, file.Path)
		if err := validateEffectiveNativeCredentialTypes(effective); err != nil {
			return fmt.Errorf("Bruno request %q: %w", relative, err)
		}
	}
	return nil
}

func nativeRequestScripts(
	input Input,
	files []sourceFile,
	root string,
	collection Collection,
	manifest Manifest,
	sourceRequirements []SecretRequirement,
	findings []SecurityFinding,
) ([]NativeScriptCandidate, []SecretRequirement, []string, []SecurityFinding, error) {
	if err := validateNativeYAMLFiles(files); err != nil {
		return nil, nil, nil, findings, err
	}
	collectionFile, folderFiles, err := nativeMetadataFiles(files, root)
	if err != nil {
		return nil, nil, nil, findings, err
	}

	layerCache := newNativeLayerCache()
	var collectionLayer nativeLayer
	if collectionFile != nil {
		collectionLayer, err = layerCache.metadataLayer(*collectionFile)
		if err != nil {
			return nil, nil, nil, findings, fmt.Errorf("parse Bruno collection metadata %q: %w", collectionFile.Path, err)
		}
	}

	// Per-request failures are collected rather than returned on the first hit.
	// The bundle is still rejected as a whole — a partial native import would
	// silently drop requests, which the importer's no-silent-drop contract
	// forbids — but the operator now learns about every offending request from a
	// single inspect instead of re-uploading a large collection once per bad file.
	failures := &nativeRequestFailures{}

	requestFiles := make(map[string]sourceFile)
	// Requests rejected by the container-format gate below are recorded so the
	// ordered-item walk does not report them a second time as "not found in the
	// sanitized bundle".
	rejectedRequestFiles := make(map[string]struct{})
	for _, file := range files {
		// A wholly legacy bundle would otherwise report one failure per file, and
		// the joined message is returned verbatim to the HTTP client. Stopping
		// here also stops populating rejectedRequestFiles, which is safe because
		// the ordered-item walk below is saturated too and breaks on its first
		// iteration, so it cannot double-report an unrecorded rejection.
		if failures.saturated() {
			break
		}
		if classifyFileRole(file.Path, file.Data) != "request" {
			continue
		}
		relative, ok := nativeRelativePath(file.Path, root)
		if !ok {
			continue
		}
		extension := strings.ToLower(path.Ext(file.Path))
		if extension != ".yml" && extension != ".yaml" {
			failures.add(fmt.Errorf(
				"Bruno Runner has been retired; native_requests supports OpenCollection YAML HTTP items only; %q has unsupported extension %q",
				relative,
				extension,
			))
			rejectedRequestFiles[relative] = struct{}{}
			continue
		}
		requestFiles[relative] = file
	}

	orderedItems := flattenNativeHTTPItems(collection.Items)
	if unsupported := nativeUnsupportedItemTypes(collection.Items); len(unsupported) > 0 {
		// Aggregate rather than return directly: container-format failures
		// collected above would otherwise be discarded, and the operator would
		// only learn about them after fixing the item types and re-uploading.
		if !failures.saturated() {
			failures.add(fmt.Errorf(
				"Bruno Runner has been retired; native_requests supports OpenCollection YAML HTTP items only; unsupported items: %s",
				strings.Join(unsupported, ", "),
			))
		}
		return nil, nil, nil, findings, failures.err()
	}
	if len(orderedItems) == 0 {
		// Rejected containers are the reason there is nothing to import; report
		// them rather than the generic "no requests" message.
		if err := failures.err(); err != nil {
			return nil, nil, nil, findings, err
		}
		return nil, nil, nil, findings, fmt.Errorf("native_requests found no OpenCollection YAML HTTP requests")
	}

	requirementsByName := make(map[string][]SecretRequirement)
	for _, requirement := range sourceRequirements {
		name := strings.ToUpper(strings.TrimSpace(requirement.SuggestedName))
		requirementsByName[name] = append(requirementsByName[name], requirement)
	}
	manifestHashes := make(map[string]string, len(manifest.Files))
	for _, file := range manifest.Files {
		manifestHashes[file.Path] = file.SHA256
	}

	candidates := make([]NativeScriptCandidate, 0, len(orderedItems))
	aggregateRequirements := make(map[string]SecretRequirement)
	aggregateRequirementIDsByName := make(map[string]string)
	aggregateMissing := make(map[string]struct{})
	seenFinding := make(map[string]struct{})
	for _, finding := range findings {
		seenFinding[finding.ID] = struct{}{}
	}

	for _, item := range orderedItems {
		if failures.saturated() {
			break
		}
		file, ok := requestFiles[item.Path]
		if !ok {
			if _, rejected := rejectedRequestFiles[item.Path]; rejected {
				// Already reported by the container-format gate.
				continue
			}
			failures.add(fmt.Errorf("native request source %q was not found in the sanitized bundle", item.Path))
			continue
		}
		layer, err := parseNativeLayer(file, true)
		if err != nil {
			failures.add(fmt.Errorf("parse Bruno request %q: %w", file.Path, err))
			continue
		}
		layer.Name = firstNonEmptyNative(layer.Name, item.Name)
		layer.UID = firstNonEmptyNative(layer.UID, item.UID)
		if layer.Sequence == 0 {
			layer.Sequence = item.Sequence
		}

		layers := make([]nativeLayer, 0, 2+strings.Count(item.Path, "/"))
		if collectionFile != nil {
			layers = append(layers, collectionLayer)
		}
		ancestorPaths := nativeAncestorPaths(path.Dir(item.Path))
		ancestorFailed := false
		for _, ancestor := range ancestorPaths {
			fullPath := ancestor
			if root != "" {
				fullPath = path.Join(root, ancestor)
			}
			folderFile, exists := folderFiles[fullPath]
			if !exists {
				continue
			}
			folderLayer, parseErr := layerCache.metadataLayer(folderFile)
			if parseErr != nil {
				failures.add(fmt.Errorf("parse Bruno folder metadata %q: %w", folderFile.Path, parseErr))
				ancestorFailed = true
				break
			}
			layers = append(layers, folderLayer)
		}
		if ancestorFailed {
			continue
		}
		layers = append(layers, layer)

		effective, mergeDiagnostics := mergeNativeLayers(layers, file.Path)
		candidate, candidateFindings, buildErr := buildNativeCandidate(
			input,
			collection,
			manifest,
			item,
			file,
			layers,
			effective,
			mergeDiagnostics,
			requirementsByName,
			manifestHashes,
		)
		if buildErr != nil {
			failures.add(buildErr)
			continue
		}
		conflicted := false
		for _, requirement := range candidate.SecretRequirements {
			normalizedName := strings.ToUpper(strings.TrimSpace(requirement.SuggestedName))
			if existingID, exists := aggregateRequirementIDsByName[normalizedName]; exists &&
				existingID != requirement.ID {
				failures.add(fmt.Errorf(
					"native credential requirements share destination binding %q",
					requirement.SuggestedName,
				))
				conflicted = true
				break
			}
			if existing, exists := aggregateRequirements[requirement.ID]; exists {
				if existing.SourceVariable != "" && requirement.SourceVariable != "" &&
					existing.SourceVariable != requirement.SourceVariable {
					failures.add(fmt.Errorf(
						"native credential requirement %q refers to distinct Bruno variables %q and %q",
						requirement.SuggestedName,
						existing.SourceVariable,
						requirement.SourceVariable,
					))
					conflicted = true
					break
				}
				if existing.Value != "" && requirement.Value != "" &&
					existing.Value != requirement.Value {
					failures.add(fmt.Errorf(
						"native credential requirement %q resolves to conflicting values across requests",
						requirement.SuggestedName,
					))
					conflicted = true
					break
				}
				aggregateRequirements[requirement.ID] = mergeNativeSecretRequirements(existing, requirement)
			} else {
				initializeNativeSecretRequirementMergeState(&requirement)
				aggregateRequirements[requirement.ID] = requirement
			}
			aggregateRequirementIDsByName[normalizedName] = requirement.ID
		}
		if conflicted {
			continue
		}
		for _, missing := range candidate.MissingVariables {
			aggregateMissing[missing] = struct{}{}
		}
		for _, finding := range candidateFindings {
			if _, exists := seenFinding[finding.ID]; exists {
				continue
			}
			seenFinding[finding.ID] = struct{}{}
			findings = append(findings, finding)
		}
		candidates = append(candidates, candidate)
	}
	if err := failures.err(); err != nil {
		return nil, nil, nil, findings, err
	}

	aggregateRequirementList := make([]SecretRequirement, 0, len(aggregateRequirements))
	for _, requirement := range aggregateRequirements {
		aggregateRequirementList = append(
			aggregateRequirementList,
			finalizeNativeSecretRequirement(requirement),
		)
	}
	sort.Slice(aggregateRequirementList, func(i, j int) bool {
		if aggregateRequirementList[i].SuggestedName == aggregateRequirementList[j].SuggestedName {
			return aggregateRequirementList[i].ID < aggregateRequirementList[j].ID
		}
		return aggregateRequirementList[i].SuggestedName < aggregateRequirementList[j].SuggestedName
	})
	missing := sortedNativeSet(aggregateMissing)
	return candidates, aggregateRequirementList, missing, findings, nil
}

// maxReportedNativeRequestFailures bounds both the size of the aggregated
// import error and the work a wholly malformed bundle can force: once this many
// distinct failures have been collected the remaining files/requests are not
// inspected.
const maxReportedNativeRequestFailures = 10

// nativeRequestFailures accumulates per-file import failures so one bad file no
// longer hides every other broken request in the same collection. Distinct
// messages are kept in first-seen order, which is deterministic because source
// files are path-sorted and request items are traversed in collection order.
type nativeRequestFailures struct {
	// subject names what the aggregated message is counting. It defaults to
	// "Bruno requests"; the pre-parse YAML validator sets it to "Bruno YAML
	// documents" because it also rejects collection and folder metadata.
	subject  string
	failures []error
	seen     map[string]struct{}
	skipped  bool
}

func (f *nativeRequestFailures) add(err error) {
	if err == nil {
		return
	}
	if f.seen == nil {
		f.seen = make(map[string]struct{})
	}
	message := err.Error()
	if _, duplicate := f.seen[message]; duplicate {
		return
	}
	f.seen[message] = struct{}{}
	f.failures = append(f.failures, err)
}

func (f *nativeRequestFailures) saturated() bool {
	if len(f.failures) >= maxReportedNativeRequestFailures {
		f.skipped = true
		return true
	}
	return false
}

func (f *nativeRequestFailures) err() error {
	switch len(f.failures) {
	case 0:
		return nil
	case 1:
		// Preserve the single-failure message (and its wrapped chain) exactly as
		// it read before failures were aggregated.
		return f.failures[0]
	}
	messages := make([]string, 0, len(f.failures))
	for _, failure := range f.failures {
		messages = append(messages, failure.Error())
	}
	subject := f.subject
	if subject == "" {
		subject = "Bruno requests"
	}
	suffix := ""
	if f.skipped {
		suffix = "; remaining " + subject + " were not inspected"
	}
	return fmt.Errorf(
		"%d %s could not be imported%s: %s",
		len(f.failures),
		subject,
		suffix,
		strings.Join(messages, "; "),
	)
}

// validateNativeYAMLFiles rejects malformed per-document YAML (duplicate or
// non-scalar mapping keys, merge aliases, unsupported layer shapes) before any
// layer is parsed. Like the per-request projection below it reports every
// offending document from a single inspect rather than stopping at the first
// one: malformed request shape is the most common authoring error, and aborting
// on file one forces a fix-and-re-upload loop over a large collection. The
// bundle is still rejected as a whole.
func validateNativeYAMLFiles(files []sourceFile) error {
	failures := &nativeRequestFailures{subject: "Bruno YAML documents"}
	for _, file := range files {
		if failures.saturated() {
			break
		}
		extension := strings.ToLower(path.Ext(file.Path))
		if extension != ".yml" && extension != ".yaml" {
			continue
		}
		// classifyFileRole hashes and (on a cache miss) parses the file, so it is
		// resolved once per file rather than once per use.
		role := classifyFileRole(file.Path, file.Data)
		switch role {
		case "collection", "folder", "request":
		default:
			continue
		}
		if err := validateNativeYAMLMappingKeys(file.Data); err != nil {
			failures.add(fmt.Errorf("Bruno YAML %q: %w", file.Path, err))
			continue
		}
		if err := validateNativeLayerShape(file, role); err != nil {
			failures.add(fmt.Errorf("Bruno YAML %q: %w", file.Path, err))
			continue
		}
	}
	return failures.err()
}

func validateNativeYAMLMappingKeys(data []byte) error {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 {
		return nil
	}
	return validateNativeYAMLNodeMappingKeys(document.Content[0], "$")
}

func validateNativeYAMLNodeMappingKeys(node *yaml.Node, location string) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		seen := make(map[string]string, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			keyNode := node.Content[index]
			if keyNode.Kind != yaml.ScalarNode {
				return fmt.Errorf("mapping %s contains a non-scalar key", location)
			}
			key := strings.TrimSpace(keyNode.Value)
			if key == "<<" {
				return fmt.Errorf("mapping %s uses a YAML merge key; merge aliases are not supported", location)
			}
			folded := cases.Fold().String(norm.NFC.String(key))
			if prior, duplicate := seen[folded]; duplicate {
				return fmt.Errorf("mapping %s contains case-fold duplicate keys %q and %q", location, prior, keyNode.Value)
			}
			seen[folded] = keyNode.Value
			childLocation := location + "." + key
			if err := validateNativeYAMLNodeMappingKeys(node.Content[index+1], childLocation); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			if err := validateNativeYAMLNodeMappingKeys(child, location+"["+strconv.Itoa(index)+"]"); err != nil {
				return err
			}
		}
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := validateNativeYAMLNodeMappingKeys(child, location); err != nil {
				return err
			}
		}
	}
	return nil
}

func nativeMetadataFiles(files []sourceFile, root string) (*sourceFile, map[string]sourceFile, error) {
	type metadataGroup struct {
		kind  string
		files []sourceFile
	}
	groups := make(map[string]*metadataGroup)
	for _, file := range files {
		base := strings.ToLower(path.Base(file.Path))
		kind := ""
		switch base {
		case "opencollection.yml", "opencollection.yaml":
			kind = "collection"
		case "folder.yml", "folder.yaml":
			kind = "folder"
		default:
			continue
		}
		dir := path.Dir(file.Path)
		if dir == "." {
			dir = ""
		}
		key := strings.ToLower(dir) + "\x00" + kind
		group := groups[key]
		if group == nil {
			group = &metadataGroup{kind: kind}
			groups[key] = group
		}
		group.files = append(group.files, file)
	}
	groupKeys := make([]string, 0, len(groups))
	for key := range groups {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)
	for _, key := range groupKeys {
		group := groups[key]
		if len(group.files) <= 1 {
			continue
		}
		names := make([]string, 0, len(group.files))
		for _, file := range group.files {
			names = append(names, file.Path)
		}
		sort.Strings(names)
		return nil, nil, fmt.Errorf("ambiguous Bruno %s metadata files: %s", group.kind, strings.Join(names, ", "))
	}

	collectionFiles := make([]sourceFile, 0, 1)
	for _, key := range groupKeys {
		group := groups[key]
		if group.kind == "collection" {
			collectionFiles = append(collectionFiles, group.files[0])
		}
	}
	if len(collectionFiles) > 1 {
		names := make([]string, 0, len(collectionFiles))
		for _, file := range collectionFiles {
			names = append(names, file.Path)
		}
		sort.Strings(names)
		return nil, nil, fmt.Errorf("multiple Bruno OpenCollection roots are not supported: %s", strings.Join(names, ", "))
	}

	var collectionFile *sourceFile
	folderFiles := make(map[string]sourceFile)
	for _, key := range groupKeys {
		group := groups[key]
		file := group.files[0]
		dir := path.Dir(file.Path)
		if dir == "." {
			dir = ""
		}
		if group.kind == "collection" {
			if dir != root {
				continue
			}
			copy := file
			collectionFile = &copy
			continue
		}
		folderFiles[dir] = file
	}
	return collectionFile, folderFiles, nil
}

func nativeRelativePath(filename, root string) (string, bool) {
	if root == "" {
		return filename, true
	}
	prefix := strings.TrimSuffix(root, "/") + "/"
	if !strings.HasPrefix(filename, prefix) {
		return "", false
	}
	return strings.TrimPrefix(filename, prefix), true
}

func flattenNativeHTTPItems(items []CollectionItem) []CollectionItem {
	var out []CollectionItem
	var walk func([]CollectionItem)
	walk = func(current []CollectionItem) {
		for _, item := range current {
			if item.Type == "folder" {
				walk(item.Children)
				continue
			}
			if item.Type == "http" && item.Executable {
				out = append(out, item)
			}
		}
	}
	walk(items)
	return out
}

func nativeUnsupportedItemTypes(items []CollectionItem) []string {
	seen := make(map[string]struct{})
	var walk func([]CollectionItem)
	walk = func(current []CollectionItem) {
		for _, item := range current {
			if item.Type == "folder" {
				walk(item.Children)
				continue
			}
			if item.Executable && item.Type != "http" {
				seen[item.Type] = struct{}{}
			}
		}
	}
	walk(items)
	return sortedNativeSet(seen)
}

func nativeAncestorPaths(folder string) []string {
	folder = strings.Trim(strings.TrimSpace(folder), "/")
	if folder == "" || folder == "." {
		return nil
	}
	parts := strings.Split(folder, "/")
	out := make([]string, 0, len(parts))
	for index := range parts {
		out = append(out, strings.Join(parts[:index+1], "/"))
	}
	return out
}

// nativeLayerParses counts the YAML documents parseNativeLayer has actually
// parsed. It exists only so importer performance regression tests can assert
// that folder/collection metadata is parsed once per pass instead of once per
// request, and is never read in production.
var nativeLayerParses atomic.Int64

func parseNativeLayer(file sourceFile, request bool) (nativeLayer, error) {
	nativeLayerParses.Add(1)
	var document map[string]any
	if err := yaml.Unmarshal(file.Data, &document); err != nil {
		return nativeLayer{}, err
	}
	info := nativeMapValue(document, "info")
	layer := nativeLayer{
		Path:     file.Path,
		Name:     anyString(nativeLookup(info, "name")),
		UID:      anyString(nativeLookup(info, "uid")),
		Sequence: anyInt(nativeLookup(info, "seq")),
	}

	containerName := "request"
	if request {
		containerName = "http"
	}
	container := nativeMapValue(document, containerName)
	layer.Headers = parseNativeHeaders(
		nativeLookup(container, "headers"),
		file.Path,
		"/"+nativeJSONPointerEscape(containerName)+"/headers",
	)
	layer.Auth = parseNativeAuth(
		nativeLookup(container, "auth"),
		file.Path,
		"/"+nativeJSONPointerEscape(containerName)+"/auth",
	)
	layer.Variables = mergeNativeVariableLists(
		parseNativeVariables(nativeLookup(document, "vars"), file.Path, "/vars"),
		parseNativeVariables(nativeLookup(document, "variables"), file.Path, "/variables"),
		parseNativeVariables(nativeLookup(container, "vars"), file.Path, "/"+nativeJSONPointerEscape(containerName)+"/vars"),
		parseNativeVariables(nativeLookup(container, "variables"), file.Path, "/"+nativeJSONPointerEscape(containerName)+"/variables"),
	)
	layer.Settings = mergeNativeSettings(
		parseNativeSettings(nativeMapValue(document, "settings")),
		parseNativeSettings(nativeMapValue(container, "settings")),
	)
	layer.Hooks = append(layer.Hooks, parseNativeRuntimeHooks(nativeMapValue(document, "runtime"), file.Path)...)
	layer.Hooks = append(layer.Hooks, parseNativeRuntimeHooks(nativeMapValue(container, "runtime"), file.Path)...)
	layer.Hooks = append(layer.Hooks, parseNativeHookList(nativeLookup(container, "scripts"), file.Path)...)
	return layer, nil
}

func parseNativeHeaders(value any, sourcePath, pointer string) []nativeHeader {
	var headers []nativeHeader
	switch typed := value.(type) {
	case []any:
		for index, raw := range typed {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name := strings.TrimSpace(anyString(nativeLookup(item, "name")))
			if name == "" {
				continue
			}
			headers = append(headers, nativeHeader{
				Name:          name,
				Value:         anyString(nativeLookup(item, "value")),
				Disabled:      nativeBool(nativeLookup(item, "disabled")),
				ValueIsString: nativeStringScalar(nativeLookup(item, "value")),
				SourcePath:    sourcePath,
				Pointer: pointer + "/" + strconv.Itoa(index) +
					"/value",
			})
		}
	case map[string]any:
		names := make([]string, 0, len(typed))
		for name := range typed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			headers = append(headers, nativeHeader{
				Name:          name,
				Value:         anyString(typed[name]),
				ValueIsString: nativeStringScalar(typed[name]),
				SourcePath:    sourcePath,
				Pointer:       pointer + "/" + nativeJSONPointerEscape(name),
			})
		}
	}
	return headers
}

func parseNativeHTTPParams(value any) ([]nativeHTTPParam, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, errorsNewNativeShape("http.params must be an array")
	}
	params := make([]nativeHTTPParam, 0, len(items))
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, errorsNewNativeShape(fmt.Sprintf("http.params[%d] must be an object", index))
		}
		name := strings.TrimSpace(anyString(nativeLookup(item, "name")))
		kind := strings.ToLower(strings.TrimSpace(anyString(nativeLookup(item, "type"))))
		if name == "" {
			return nil, errorsNewNativeShape(fmt.Sprintf("http.params[%d].name is required", index))
		}
		if kind != "query" && kind != "path" {
			return nil, errorsNewNativeShape(fmt.Sprintf("http.params[%d].type must be query or path", index))
		}
		params = append(params, nativeHTTPParam{
			Name:        name,
			Value:       anyString(nativeLookup(item, "value")),
			Type:        kind,
			Disabled:    nativeBool(nativeLookup(item, "disabled")),
			SourceIndex: index,
		})
	}
	return params, nil
}

func errorsNewNativeShape(message string) error {
	return fmt.Errorf("unsupported Bruno native shape: %s", message)
}

func replaceNativeColonPathParameter(target, name, value string) (string, bool) {
	token := ":" + name
	if name == "" || !strings.Contains(target, token) {
		return target, false
	}
	var out strings.Builder
	matched := false
	cursor := 0
	for cursor < len(target) {
		offset := strings.Index(target[cursor:], token)
		if offset < 0 {
			break
		}
		start := cursor + offset
		end := start + len(token)
		validStart := start == 0 || target[start-1] == '/'
		nextRune, _ := utf8.DecodeRuneInString(target[end:])
		validEnd := end == len(target) || !nativePathParameterNameRune(nextRune)
		if !validStart || !validEnd {
			out.WriteString(target[cursor:end])
			cursor = end
			continue
		}
		out.WriteString(target[cursor:start])
		out.WriteString(value)
		cursor = end
		matched = true
	}
	if !matched {
		return target, false
	}
	out.WriteString(target[cursor:])
	return out.String(), true
}

func nativePathParameterNameRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) ||
		value == '_' || value == '-'
}

func applyNativeHTTPParams(
	rawTarget string,
	params []nativeHTTPParam,
	encode bool,
	state *nativeTemplateState,
	sourceRequirements map[string][]SecretRequirement,
) (string, error) {
	if len(params) == 0 {
		return rawTarget, nil
	}
	target := rawTarget
	queryParams := make([]nativeHTTPParam, 0, len(params))
	for paramIndex, param := range params {
		switch param.Type {
		case "path":
			if param.Disabled {
				continue
			}
			value := ""
			if nativeSensitiveURLValue(param.Name, param.Value) &&
				brunoTemplatePattern.MatchString(param.Value) &&
				!isExactGovernedSecretReference(param.Value) {
				pointer := "/http/params/" + strconv.Itoa(paramIndex) + "/value"
				value, _ = rewriteNativeComposedURLCredential(
					param.Value,
					pointer,
					param.Name,
					paramIndex+1,
					state,
					sourceRequirements,
					nativeSecretScrubTarget{
						Path:    state.sourcePath,
						Pointer: pointer,
					},
				)
			} else {
				context := nativeTemplateOrdinary
				if nativeSensitiveURLValue(param.Name, param.Value) {
					context = nativeTemplateSensitiveHeader
				}
				value = rewriteNativeTemplates(param.Value, context, param.Name, state)
			}
			if encode && !strings.Contains(value, "{{") {
				value = url.PathEscape(value)
			}
			braceToken := "{" + param.Name + "}"
			if replaced, matched := replaceNativeColonPathParameter(target, param.Name, value); matched {
				target = replaced
			} else if strings.Contains(target, braceToken) {
				target = strings.ReplaceAll(target, braceToken, value)
			} else {
				return "", errorsNewNativeShape(fmt.Sprintf("path parameter %q has no matching URL placeholder", param.Name))
			}
		case "query":
			queryParams = append(queryParams, param)
		}
	}
	if len(queryParams) == 0 {
		return target, nil
	}

	fragment := ""
	if index := strings.Index(target, "#"); index >= 0 {
		fragment = target[index:]
		target = target[:index]
	}
	base, rawQuery, _ := strings.Cut(target, "?")
	paramNames := make(map[string]struct{}, len(queryParams))
	for _, param := range queryParams {
		paramNames[param.Name] = struct{}{}
	}
	parts := make([]string, 0)
	if rawQuery != "" {
		for _, part := range strings.Split(rawQuery, "&") {
			key := part
			if index := strings.IndexByte(key, '='); index >= 0 {
				key = key[:index]
			}
			decoded, err := url.QueryUnescape(key)
			if err != nil {
				decoded = key
			}
			if _, managed := paramNames[decoded]; !managed {
				parts = append(parts, part)
			}
		}
	}
	for _, param := range queryParams {
		if param.Disabled {
			continue
		}
		value := ""
		if nativeSensitiveURLValue(param.Name, param.Value) &&
			brunoTemplatePattern.MatchString(param.Value) &&
			!isExactGovernedSecretReference(param.Value) {
			sourceIndex := param.SourceIndex
			pointer := "/http/params/" + strconv.Itoa(sourceIndex) + "/value"
			value, _ = rewriteNativeComposedURLCredential(
				param.Value,
				pointer,
				param.Name,
				sourceIndex+1,
				state,
				sourceRequirements,
				nativeSecretScrubTarget{
					Path:    state.sourcePath,
					Pointer: pointer,
				},
			)
		} else {
			context := nativeTemplateOrdinary
			if nativeSensitiveURLValue(param.Name, param.Value) {
				context = nativeTemplateSensitiveHeader
			}
			value = rewriteNativeTemplates(param.Value, context, param.Name, state)
		}
		name := param.Name
		if encode {
			name = nativeQueryEscape(name)
			if !strings.Contains(value, "{{") {
				value = nativeQueryEscape(value)
			}
		}
		parts = append(parts, name+"="+value)
	}
	target = base
	if len(parts) > 0 {
		target += "?" + strings.Join(parts, "&")
	}
	return target + fragment, nil
}

func nativeURLComponentsContainTemplates(rawTarget string, missing map[string]struct{}) bool {
	target := strings.TrimSpace(rawTarget)
	if target == "" {
		return false
	}
	if fragment := strings.IndexByte(target, '#'); fragment >= 0 {
		target = target[:fragment]
	}

	componentStart := 0
	if scheme := strings.Index(target, "://"); scheme >= 0 {
		authorityStart := scheme + len("://")
		componentStart = len(target)
		for _, separator := range []byte{'/', '?'} {
			if offset := strings.IndexByte(target[authorityStart:], separator); offset >= 0 {
				position := authorityStart + offset
				if position < componentStart {
					componentStart = position
				}
			}
		}
	} else if match := brunoTemplatePattern.FindStringIndex(target); match != nil && match[0] == 0 {
		// One or more leading templates commonly supply the complete Bruno base
		// URL. Inspect the authored path/query suffix beginning at its first
		// separator.
		componentStart = len(target)
		for _, separator := range []byte{'/', '?'} {
			if position := strings.IndexByte(target, separator); position >= 0 && position < componentStart {
				componentStart = position
			}
		}
	}
	for _, match := range brunoTemplatePattern.FindAllStringSubmatch(target[componentStart:], -1) {
		if len(match) < 2 {
			continue
		}
		expression := strings.TrimSpace(match[1])
		// Only an importer-generated unresolved variable is already made
		// non-executable by the missing-variable blocker. Authored APEX var
		// templates remain runtime-capable and need the encoding review gate.
		if strings.HasPrefix(expression, "var.") {
			name := strings.TrimSpace(strings.TrimPrefix(expression, "var."))
			if _, blocked := missing[name]; blocked {
				continue
			}
		}
		return true
	}
	return false
}

func nativeHTTPParamNamesContainTemplates(params []nativeHTTPParam) bool {
	for _, param := range params {
		if !param.Disabled && brunoTemplatePattern.MatchString(param.Name) {
			return true
		}
	}
	return false
}

func nativeQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func nativeScalarBody(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case fmt.Stringer:
		return typed.String(), nil
	default:
		return "", errorsNewNativeShape(fmt.Sprintf("raw body must be a scalar string, got %T", value))
	}
}

func serializeNativeHTTPBody(
	bodyBlock map[string]any,
	sourcePath string,
	state *nativeTemplateState,
	sourceRequirements map[string][]SecretRequirement,
) (string, string, *NativeDiagnostic, error) {
	bodyType := strings.ToLower(strings.TrimSpace(anyString(nativeLookup(bodyBlock, "type"))))
	data := nativeLookup(bodyBlock, "data")
	switch bodyType {
	case "", "none":
		body, err := nativeScalarBody(data)
		return body, "", nil, err
	case "json":
		if text, ok := data.(string); ok {
			return text, "application/json", nil, nil
		}
		payload, err := json.Marshal(data)
		if err != nil {
			return "", "", nil, fmt.Errorf("serialize JSON body: %w", err)
		}
		return string(payload), "application/json", nil, nil
	case "text":
		body, err := nativeScalarBody(data)
		return body, "text/plain", nil, err
	case "xml":
		body, err := nativeScalarBody(data)
		return body, "application/xml", nil, err
	case "form-urlencoded", "form_urlencoded":
		items, ok := data.([]any)
		if !ok {
			return "", "", nil, errorsNewNativeShape("form-urlencoded body data must be an array")
		}
		parts := make([]string, 0, len(items))
		hasTemplates := false
		for index, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				return "", "", nil, errorsNewNativeShape(fmt.Sprintf("form-urlencoded data[%d] must be an object", index))
			}
			if nativeBool(nativeLookup(item, "disabled")) {
				continue
			}
			name := anyString(nativeLookup(item, "name"))
			value := anyString(nativeLookup(item, "value"))
			if strings.TrimSpace(name) == "" {
				return "", "", nil, errorsNewNativeShape(fmt.Sprintf("form-urlencoded data[%d].name is required", index))
			}
			if nativeSensitiveBodyFieldName(name) &&
				brunoTemplatePattern.MatchString(value) &&
				!isExactGovernedSecretReference(value) {
				pointer := "/http/body/data/" + strconv.Itoa(index) + "/value"
				requirement := nativeBodyFieldSecretRequirement(
					sourcePath,
					pointer,
					"form",
					name,
					index+1,
				)
				value, _ = rewriteNativeComposedCredential(
					value,
					requirement,
					state,
					sourceRequirements,
					nativeSecretScrubTarget{
						Path:    sourcePath,
						Pointer: pointer,
					},
				)
			}
			hasTemplates = hasTemplates || brunoTemplatePattern.MatchString(name) || brunoTemplatePattern.MatchString(value)
			parts = append(parts, nativeQueryEscapePreservingTemplates(name)+"="+nativeQueryEscapePreservingTemplates(value))
		}
		if hasTemplates {
			diagnostic := nativeDiagnostic(
				sourcePath,
				"warning",
				"bruno_form_template_review_required",
				"APEX cannot guarantee Bruno form-urlencoding after runtime template expansion; review and translate this body before execution.",
				true,
			)
			return strings.Join(parts, "&"), "application/x-www-form-urlencoded", &diagnostic, nil
		}
		return strings.Join(parts, "&"), "application/x-www-form-urlencoded", nil, nil
	case "multipart-form", "multipart_form":
		items, ok := data.([]any)
		if !ok {
			return "", "", nil, errorsNewNativeShape("multipart-form body data must be an array")
		}
		var payload bytes.Buffer
		writer := multipart.NewWriter(&payload)
		boundaryHash := sha256.Sum256([]byte("bruno-native-multipart\x00" + sourcePath))
		boundary := "apexload-" + hex.EncodeToString(boundaryHash[:12])
		if err := writer.SetBoundary(boundary); err != nil {
			return "", "", nil, fmt.Errorf("set multipart boundary: %w", err)
		}
		fileReviewRequired := false
		templateReviewRequired := false
		for index, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				return "", "", nil, errorsNewNativeShape(fmt.Sprintf("multipart-form data[%d] must be an object", index))
			}
			if nativeBool(nativeLookup(item, "disabled")) {
				continue
			}
			name := anyString(nativeLookup(item, "name"))
			if strings.TrimSpace(name) == "" {
				return "", "", nil, errorsNewNativeShape(fmt.Sprintf("multipart-form data[%d].name is required", index))
			}
			if brunoTemplatePattern.MatchString(name) {
				templateReviewRequired = true
			}
			partType := strings.ToLower(strings.TrimSpace(anyString(nativeLookup(item, "type"))))
			if partType == "file" {
				fileReviewRequired = true
				continue
			}
			value := anyString(nativeLookup(item, "value"))
			if nativeSensitiveBodyFieldName(name) &&
				brunoTemplatePattern.MatchString(value) &&
				!isExactGovernedSecretReference(value) {
				pointer := "/http/body/data/" + strconv.Itoa(index) + "/value"
				requirement := nativeBodyFieldSecretRequirement(
					sourcePath,
					pointer,
					"multipart",
					name,
					index+1,
				)
				value, _ = rewriteNativeComposedCredential(
					value,
					requirement,
					state,
					sourceRequirements,
					nativeSecretScrubTarget{
						Path:    sourcePath,
						Pointer: pointer,
					},
				)
			}
			if brunoTemplatePattern.MatchString(value) {
				templateReviewRequired = true
			}
			part, err := writer.CreateFormField(name)
			if err != nil {
				return "", "", nil, fmt.Errorf("create multipart field: %w", err)
			}
			if _, err := part.Write([]byte(value)); err != nil {
				return "", "", nil, fmt.Errorf("write multipart field: %w", err)
			}
		}
		if err := writer.Close(); err != nil {
			return "", "", nil, fmt.Errorf("finish multipart body: %w", err)
		}
		if templateReviewRequired && fileReviewRequired {
			diagnostic := nativeDiagnostic(
				sourcePath,
				"warning",
				"bruno_multipart_template_and_file_review_required",
				"Bruno multipart templates require runtime MIME escaping and file parts were omitted; translate both behaviors before execution.",
				true,
			)
			return payload.String(), writer.FormDataContentType(), &diagnostic, nil
		}
		if templateReviewRequired {
			diagnostic := nativeDiagnostic(
				sourcePath,
				"warning",
				"bruno_multipart_template_review_required",
				"Bruno multipart field templates expand after MIME serialization in APEX; translate and MIME-escape runtime field values before execution.",
				true,
			)
			return payload.String(), writer.FormDataContentType(), &diagnostic, nil
		}
		if fileReviewRequired {
			diagnostic := nativeDiagnostic(
				sourcePath,
				"warning",
				"bruno_multipart_file_review_required",
				"Bruno multipart file parts require an APEX file-body translation before execution.",
				true,
			)
			return payload.String(), writer.FormDataContentType(), &diagnostic, nil
		}
		return payload.String(), writer.FormDataContentType(), nil, nil
	case "graphql":
		diagnostic := nativeDiagnostic(
			sourcePath,
			"warning",
			"bruno_graphql_body_review_required",
			"Bruno GraphQL body behavior requires review before native HTTP execution.",
			true,
		)
		payload, err := json.Marshal(data)
		if err != nil {
			return "", "", nil, fmt.Errorf("serialize GraphQL body: %w", err)
		}
		return string(payload), "application/json", &diagnostic, nil
	default:
		return "", "", nil, errorsNewNativeShape(fmt.Sprintf("body type %q is not supported", bodyType))
	}
}

func nativeQueryEscapePreservingTemplates(value string) string {
	matches := brunoTemplatePattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return nativeQueryEscape(value)
	}
	var out strings.Builder
	cursor := 0
	for _, match := range matches {
		out.WriteString(nativeQueryEscape(value[cursor:match[0]]))
		out.WriteString(value[match[0]:match[1]])
		cursor = match[1]
	}
	out.WriteString(nativeQueryEscape(value[cursor:]))
	return out.String()
}

func nativeHeaderListContains(headers dsl.HeaderList, name string) bool {
	for _, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header.Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func parseNativeVariables(value any, sourcePath, pointer string) []nativeVariable {
	var variables []nativeVariable
	switch typed := value.(type) {
	case []any:
		for index, raw := range typed {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name := strings.TrimSpace(anyString(nativeLookup(item, "name")))
			if name == "" {
				continue
			}
			variables = append(variables, nativeVariable{
				Name:              name,
				Value:             anyString(nativeLookup(item, "value")),
				ValueIsString:     nativeStringScalar(nativeLookup(item, "value")),
				Disabled:          nativeBool(nativeLookup(item, "disabled")),
				SourcePath:        sourcePath,
				DefinitionPointer: pointer + "/" + strconv.Itoa(index) + "/value",
			})
		}
	case map[string]any:
		names := make([]string, 0, len(typed))
		for name := range typed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			variables = append(variables, nativeVariable{
				Name:              name,
				Value:             anyString(typed[name]),
				ValueIsString:     nativeStringScalar(typed[name]),
				SourcePath:        sourcePath,
				DefinitionPointer: pointer + "/" + nativeJSONPointerEscape(name),
			})
		}
	}
	return variables
}

func nativeJSONPointerEscape(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func mergeNativeVariableLists(lists ...[]nativeVariable) []nativeVariable {
	var out []nativeVariable
	for _, list := range lists {
		for _, variable := range list {
			if variable.Disabled {
				continue
			}
			replaced := false
			for index := range out {
				if out[index].Name == variable.Name {
					out[index] = variable
					replaced = true
					break
				}
			}
			if !replaced {
				out = append(out, variable)
			}
		}
	}
	return out
}

func parseNativeAuth(value any, sourcePath, pointer string) nativeAuth {
	if value == nil {
		return nativeAuth{}
	}
	if scalar, ok := value.(string); ok {
		mode := strings.ToLower(strings.TrimSpace(scalar))
		return nativeAuth{
			Present:    true,
			Mode:       mode,
			SourcePath: sourcePath,
			Pointer:    pointer,
		}
	}
	authMap, ok := value.(map[string]any)
	if !ok {
		return nativeAuth{
			Present:    true,
			Mode:       "unsupported",
			SourcePath: sourcePath,
			Pointer:    pointer,
		}
	}
	mode := strings.ToLower(strings.TrimSpace(anyString(nativeLookup(authMap, "type"))))
	configMap := authMap
	if nested := nativeMapValue(authMap, mode); len(nested) > 0 {
		configMap = nested
		pointer += "/" + nativeJSONPointerEscape(mode)
	}
	credentialFields := make(map[string]bool)
	for _, field := range []string{"username", "password", "token", "domain"} {
		if raw, present := nativeLookupPresent(configMap, field); present {
			credentialFields[field] = nativeStringScalar(raw)
		}
	}
	return nativeAuth{
		Present:          true,
		Mode:             mode,
		CredentialFields: credentialFields,
		SourcePath:       sourcePath,
		Pointer:          pointer,
		Config: dsl.AuthConfig{
			Type:     mode,
			Username: anyString(nativeLookup(configMap, "username")),
			Password: anyString(nativeLookup(configMap, "password")),
			Token:    anyString(nativeLookup(configMap, "token")),
			Domain:   anyString(nativeLookup(configMap, "domain")),
		},
	}
}

func nativeAuthModeExecutable(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "inherit", "none", "noauth", "no_auth", "basic", "bearer", "digest":
		return true
	default:
		return false
	}
}

func parseNativeSettings(value map[string]any) nativeSettings {
	var settings nativeSettings
	if raw, ok := nativeLookupPresent(value, "encodeUrl"); ok {
		parsed := nativeBool(raw)
		settings.EncodeURL = &parsed
	}
	if raw, ok := nativeLookupPresent(value, "timeout"); ok {
		parsed := anyInt(raw)
		settings.TimeoutMS = &parsed
	}
	if raw, ok := nativeLookupPresent(value, "followRedirects"); ok {
		parsed := nativeBool(raw)
		settings.FollowRedirects = &parsed
	}
	if raw, ok := nativeLookupPresent(value, "maxRedirects"); ok {
		parsed := anyInt(raw)
		settings.MaxRedirects = &parsed
	}
	return settings
}

func mergeNativeSettings(values ...nativeSettings) nativeSettings {
	var out nativeSettings
	for _, value := range values {
		if value.EncodeURL != nil {
			out.EncodeURL = nativeBoolPointer(*value.EncodeURL)
		}
		if value.TimeoutMS != nil {
			out.TimeoutMS = nativeIntPointer(*value.TimeoutMS)
		}
		if value.FollowRedirects != nil {
			out.FollowRedirects = nativeBoolPointer(*value.FollowRedirects)
		}
		if value.MaxRedirects != nil {
			out.MaxRedirects = nativeIntPointer(*value.MaxRedirects)
		}
	}
	return out
}

func parseNativeRuntimeHooks(runtime map[string]any, sourcePath string) []nativeHook {
	hooks := parseNativeHookList(nativeLookup(runtime, "scripts"), sourcePath)
	if assertions, present := nativeLookupPresent(runtime, "assertions"); present && nativeValueHasContent(assertions) {
		payload, err := yaml.Marshal(assertions)
		if err != nil {
			payload = []byte(fmt.Sprintf("%v", assertions))
		}
		hooks = append(hooks, nativeHook{
			Type: "assertions",
			Code: string(payload),
			Path: sourcePath,
		})
	}
	return hooks
}

func nativeValueHasContent(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func parseNativeHookList(value any, sourcePath string) []nativeHook {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	hooks := make([]nativeHook, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		code := anyString(nativeLookup(item, "code"))
		if strings.TrimSpace(code) == "" {
			continue
		}
		hooks = append(hooks, nativeHook{
			Type: strings.ToLower(strings.TrimSpace(anyString(nativeLookup(item, "type")))),
			Code: code,
			Path: sourcePath,
		})
	}
	return hooks
}

func mergeNativeLayers(layers []nativeLayer, requestPath string) (nativeEffective, []NativeDiagnostic) {
	var effective nativeEffective
	var diagnostics []NativeDiagnostic
	for _, layer := range layers {
		overlaidNames := make(map[string]struct{})
		for _, header := range layer.Headers {
			if header.Disabled || strings.TrimSpace(header.Name) == "" {
				continue
			}
			normalizedName := strings.ToLower(strings.TrimSpace(header.Name))
			if _, overlaid := overlaidNames[normalizedName]; !overlaid {
				filtered := effective.Headers[:0]
				for _, existing := range effective.Headers {
					if !strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(header.Name)) {
						filtered = append(filtered, existing)
					}
				}
				effective.Headers = filtered
				overlaidNames[normalizedName] = struct{}{}
			}
			for _, existing := range effective.Headers {
				if strings.EqualFold(existing.Name, header.Name) && existing.Name != header.Name {
					diagnostics = append(diagnostics, nativeDiagnostic(
						requestPath,
						"warning",
						"bruno_header_case_collision",
						fmt.Sprintf("Same-layer headers %q and %q differ only by case; both authored occurrences were preserved in source order.", existing.Name, header.Name),
						false,
					))
					break
				}
			}
			effective.Headers = append(effective.Headers, header)
		}
		effective.Variables = mergeNativeVariableLists(effective.Variables, layer.Variables)
		if layer.Auth.Present {
			switch layer.Auth.Mode {
			case "", "inherit":
				// Inherit preserves the closest configured ancestor.
			case "none", "noauth", "no_auth":
				effective.Auth = nativeAuth{Present: true, Mode: "noauth"}
			default:
				effective.Auth = layer.Auth
			}
		}
		effective.Hooks = append(effective.Hooks, layer.Hooks...)
		effective.Settings = mergeNativeSettings(effective.Settings, layer.Settings)
	}
	return effective, diagnostics
}

func validateEffectiveNativeCredentialTypes(effective nativeEffective) error {
	for _, variable := range effective.Variables {
		if nativeSensitiveVariableName(variable.Name) && !variable.ValueIsString {
			return errorsNewNativeShape(fmt.Sprintf(
				"effective variable %q from %q must be a string because it is credential material",
				variable.Name,
				variable.SourcePath,
			))
		}
	}
	for field, isString := range effective.Auth.CredentialFields {
		if !isString {
			return errorsNewNativeShape(fmt.Sprintf(
				"effective auth field %q from %q must be a string",
				field,
				effective.Auth.SourcePath,
			))
		}
	}
	return nil
}

func buildNativeCandidate(
	input Input,
	collection Collection,
	manifest Manifest,
	item CollectionItem,
	file sourceFile,
	layers []nativeLayer,
	effective nativeEffective,
	diagnostics []NativeDiagnostic,
	sourceRequirements map[string][]SecretRequirement,
	manifestHashes map[string]string,
) (NativeScriptCandidate, []SecurityFinding, error) {
	if err := validateEffectiveNativeCredentialTypes(effective); err != nil {
		return NativeScriptCandidate{}, nil, fmt.Errorf("Bruno request %q: %w", item.Path, err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(file.Data, &document); err != nil {
		return NativeScriptCandidate{}, nil, fmt.Errorf("parse Bruno request %q: %w", file.Path, err)
	}
	httpBlock := nativeMapValue(document, "http")
	method := strings.ToUpper(strings.TrimSpace(anyString(nativeLookup(httpBlock, "method"))))
	if method == "" {
		method = "GET"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		return NativeScriptCandidate{}, nil, fmt.Errorf("Bruno request %q uses unsupported HTTP method %q", item.Path, method)
	}
	rawTarget := anyString(nativeLookup(httpBlock, "url"))
	if strings.TrimSpace(rawTarget) == "" {
		return NativeScriptCandidate{}, nil, fmt.Errorf("Bruno request %q has no HTTP URL", item.Path)
	}

	defined := make(map[string]nativeVariable, len(effective.Variables))
	for _, variable := range effective.Variables {
		defined[variable.Name] = variable
	}
	templateState := &nativeTemplateState{
		sourcePath:      file.Path,
		defined:         defined,
		usedParams:      make(map[string]struct{}),
		parameterValues: make(map[string]string),
		missing:         make(map[string]struct{}),
		generatedReq:    make(map[string]SecretRequirement),
		resolved:        make(map[string]nativeVariableResolution),
		resolving:       make(map[string]int),
	}
	params, err := parseNativeHTTPParams(nativeLookup(httpBlock, "params"))
	if err != nil {
		return NativeScriptCandidate{}, nil, fmt.Errorf("Bruno request %q: %w", item.Path, err)
	}
	encodeURL := effective.Settings.EncodeURL == nil || *effective.Settings.EncodeURL
	rawTarget, err = applyNativeHTTPParams(
		rawTarget,
		params,
		encodeURL,
		templateState,
		sourceRequirements,
	)
	if err != nil {
		return NativeScriptCandidate{}, nil, fmt.Errorf("Bruno request %q: %w", item.Path, err)
	}
	target := rewriteNativeURLTemplates(rawTarget, templateState, sourceRequirements)
	for parameterName, parameterValue := range templateState.parameterValues {
		if nativeURLHasUserinfo(parameterValue) {
			return NativeScriptCandidate{}, nil, fmt.Errorf(
				"Bruno request %q URL parameter %q resolves to unsupported authored userinfo",
				item.Path,
				parameterName,
			)
		}
		if nativeURLHasSensitiveQuery(parameterValue) {
			return NativeScriptCandidate{}, nil, fmt.Errorf(
				"Bruno request %q URL parameter %q resolves to a sensitive query value that cannot be safely projected",
				item.Path,
				parameterName,
			)
		}
		if nativeURLHasSensitiveFragment(parameterValue) ||
			(!strings.Contains(parameterValue, "://") &&
				scrub.URLTokenSensitive(parameterValue)) {
			return NativeScriptCandidate{}, nil, fmt.Errorf(
				"Bruno request %q URL parameter %q resolves to credential-shaped material that cannot be safely projected",
				item.Path,
				parameterName,
			)
		}
	}
	reviewReasons := make([]string, 0)
	if nativeURLHasSensitiveQuery(rawTarget) ||
		nativeHTTPParamsContainSensitiveCredentials(params) {
		diagnostic := nativeDiagnostic(
			file.Path,
			"warning",
			"bruno_url_secret_encoding_review_required",
			"APEX cannot safely apply Bruno URL-component encoding to governed runtime credential values; translate the credential transport before execution.",
			true,
		)
		diagnostics = append(diagnostics, diagnostic)
		reviewReasons = append(reviewReasons, diagnostic.Code)
	}

	headers := append([]nativeHeader(nil), effective.Headers...)
	if effective.Auth.Present && effective.Auth.Mode == "basic" {
		filtered := headers[:0]
		shadowed := false
		for _, header := range headers {
			if strings.EqualFold(strings.TrimSpace(header.Name), "Authorization") {
				shadowed = true
				continue
			}
			filtered = append(filtered, header)
		}
		headers = filtered
		if shadowed {
			diagnostics = append(diagnostics, nativeDiagnostic(
				file.Path,
				"info",
				"bruno_authorization_header_shadowed",
				"An authored Authorization header was omitted because Bruno applies explicit Basic auth after request headers.",
				false,
			))
		}
	}
	for _, header := range headers {
		if nativeSensitiveHeaderName(header.Name) && !header.ValueIsString {
			return NativeScriptCandidate{}, nil, fmt.Errorf(
				"Bruno request %q active sensitive header %q must have a string value",
				item.Path,
				header.Name,
			)
		}
	}

	headerList := make(dsl.HeaderList, 0, len(headers))
	for headerIndex, header := range headers {
		if nativeSensitiveHeaderName(header.Name) &&
			brunoTemplatePattern.MatchString(header.Value) &&
			!isGovernedSecretHeaderValue(
				header.Name,
				header.Value,
				[]string{"headers"},
				"request",
			) &&
			!isNativeCredentialHeaderSyntaxTemplate(
				header.Name,
				header.Value,
				[]string{"headers"},
				"request",
			) {
			rewritten, complete := rewriteNativeComposedSensitiveHeader(
				header.Name,
				header.Value,
				firstNonEmptyNative(header.SourcePath, file.Path),
				header.Pointer,
				headerIndex+1,
				templateState,
				sourceRequirements,
			)
			headerList = append(headerList, dsl.HeaderEntry{
				Name:  header.Name,
				Value: rewritten,
			})
			if !complete {
				diagnostic := nativeDiagnostic(
					file.Path,
					"warning",
					"bruno_sensitive_header_resolution_review_required",
					fmt.Sprintf("Sensitive header %q contains a composed template that could not be fully resolved; bind and review the governed whole-header value before execution.", header.Name),
					true,
				)
				diagnostics = append(diagnostics, diagnostic)
				reviewReasons = append(reviewReasons, diagnostic.Code)
			}
			continue
		}
		context := nativeTemplateOrdinary
		if nativeSensitiveHeaderName(header.Name) {
			context = nativeTemplateSensitiveHeader
		}
		headerList = append(headerList, dsl.HeaderEntry{
			Name:  header.Name,
			Value: rewriteNativeTemplates(header.Value, context, header.Name, templateState),
		})
	}

	if encodeURL &&
		(nativeURLComponentsContainTemplates(target, templateState.missing) ||
			nativeHTTPParamNamesContainTemplates(params)) {
		diagnostic := nativeDiagnostic(
			file.Path,
			"warning",
			"bruno_url_template_encoding_review_required",
			"APEX cannot apply Bruno URL-component encoding after runtime path or query template expansion; translate the URL before execution.",
			true,
		)
		diagnostics = append(diagnostics, diagnostic)
		reviewReasons = append(reviewReasons, diagnostic.Code)
	}
	body := ""
	bodyBlock := nativeMapValue(httpBlock, "body")
	bodyType := strings.ToLower(strings.TrimSpace(anyString(nativeLookup(bodyBlock, "type"))))
	bodyContentType := ""
	var bodyReview *NativeDiagnostic
	if len(bodyBlock) > 0 {
		body, bodyContentType, bodyReview, err = serializeNativeHTTPBody(
			bodyBlock,
			file.Path,
			templateState,
			sourceRequirements,
		)
		if err != nil {
			return NativeScriptCandidate{}, nil, fmt.Errorf("Bruno request %q body: %w", item.Path, err)
		}
	} else if rawBody, exists := nativeLookupPresent(httpBlock, "body"); exists {
		body, err = nativeScalarBody(rawBody)
		if err != nil {
			return NativeScriptCandidate{}, nil, fmt.Errorf("Bruno request %q body: %w", item.Path, err)
		}
	}
	jsonLikeBody := (bodyType == "json" || bodyType == "graphql" || nativeBodyIsJSON(body)) &&
		strings.TrimSpace(body) != ""
	if jsonLikeBody {
		bodyPointer := "/http/body"
		embeddedJSON := true
		if len(bodyBlock) > 0 {
			bodyPointer = "/http/body/data"
			_, embeddedJSON = nativeLookup(bodyBlock, "data").(string)
		}
		rewrittenBody, rewriteErr := rewriteNativeJSONCredentialTemplates(
			body,
			templateState,
			sourceRequirements,
			bodyPointer,
			embeddedJSON,
		)
		if rewriteErr != nil {
			return NativeScriptCandidate{}, nil, fmt.Errorf("rewrite Bruno JSON credential templates in %q: %w", item.Path, rewriteErr)
		}
		body = rewrittenBody
	}
	if strings.Contains(bodyType, "xml") || strings.HasPrefix(strings.TrimSpace(body), "<") {
		bodyPointer := "/http/body"
		if len(bodyBlock) > 0 {
			bodyPointer = "/http/body/data"
		}
		rewrittenBody, rewriteErr := rewriteNativeXMLCredentialTemplates(
			body,
			templateState,
			bodyPointer,
			sourceRequirements,
		)
		if rewriteErr != nil {
			return NativeScriptCandidate{}, nil, fmt.Errorf("rewrite Bruno XML credential templates in %q: %w", item.Path, rewriteErr)
		}
		body = rewrittenBody
	}
	body = rewriteNativeTemplates(body, nativeTemplateBody, "", templateState)
	if jsonLikeBody && strings.Contains(body, "{{secret.") {
		diagnostic := nativeDiagnostic(
			file.Path,
			"warning",
			"bruno_json_secret_escaping_review_required",
			"APEX interpolates secret values as raw text and cannot guarantee JSON string escaping; translate governed JSON values before execution.",
			true,
		)
		diagnostics = append(diagnostics, diagnostic)
		reviewReasons = append(reviewReasons, diagnostic.Code)
	}
	if bodyContentType != "" && !nativeHeaderListContains(headerList, "Content-Type") {
		headerList = append(headerList, dsl.HeaderEntry{Name: "Content-Type", Value: bodyContentType})
	}
	if bodyReview != nil {
		diagnostics = append(diagnostics, *bodyReview)
		reviewReasons = append(reviewReasons, bodyReview.Code)
	}
	if strings.Contains(body, nativeXMLReviewMarker) {
		diagnostic := nativeDiagnostic(
			file.Path,
			"warning",
			"bruno_xml_unsupported_markup_review_required",
			"Credential-bearing XML markup was redacted because it cannot be represented as a wire-exact governed binding; replace and verify the XML body before execution.",
			true,
		)
		diagnostics = append(diagnostics, diagnostic)
		reviewReasons = append(reviewReasons, diagnostic.Code)
	}

	var auth *dsl.AuthConfig
	if effective.Auth.Present && effective.Auth.Mode != "noauth" && effective.Auth.Mode != "" && effective.Auth.Mode != "inherit" {
		config := effective.Auth.Config
		config.Type = effective.Auth.Mode
		config.Username = rewriteNativeAuthCredential(
			config.Username,
			"username",
			nativeTemplateAuthUsername,
			effective.Auth,
			templateState,
			sourceRequirements,
		)
		config.Password = rewriteNativeAuthCredential(
			config.Password,
			"password",
			nativeTemplateAuthPassword,
			effective.Auth,
			templateState,
			sourceRequirements,
		)
		config.Token = rewriteNativeAuthCredential(
			config.Token,
			"token",
			nativeTemplateAuthToken,
			effective.Auth,
			templateState,
			sourceRequirements,
		)
		config.Domain = rewriteNativeTemplates(config.Domain, nativeTemplateOrdinary, "domain", templateState)
		auth = &config
		if config.Type == "basic" && config.Username == "" && config.Password == "" {
			diagnostics = append(diagnostics, nativeDiagnostic(
				file.Path,
				"warning",
				"bruno_empty_basic_auth",
				"Bruno sends an empty Basic credential (Basic Og==); the native action preserves that behavior.",
				false,
			))
		}
	}
	if templateState.generatedErr != nil {
		return NativeScriptCandidate{}, nil, fmt.Errorf(
			"Bruno request %q generated conflicting credential bindings: %w",
			item.Path,
			templateState.generatedErr,
		)
	}

	action := dsl.Action{
		Name:       item.Name,
		Type:       method,
		Method:     method,
		Target:     target,
		Headers:    headerList.ToMap(),
		HeaderList: headerList,
		Body:       body,
		Auth:       auth,
		Metadata: map[string]any{
			"source_profile": "bruno_native",
			"source_step_id": item.UID,
			"source_path":    item.Path,
		},
	}

	if auth != nil && !nativeAuthModeExecutable(auth.Type) {
		diagnostic := nativeDiagnostic(
			file.Path,
			"warning",
			"bruno_auth_review_required",
			fmt.Sprintf("Bruno %s authentication cannot be represented by the native APEX HTTP DSL; translate the auth behavior before execution.", auth.Type),
			true,
		)
		diagnostics = append(diagnostics, diagnostic)
		reviewReasons = append(reviewReasons, diagnostic.Code)
	}
	if effective.Settings.TimeoutMS == nil {
		action.TimeoutMs = nativeDefaultTimeoutMS
	} else {
		switch timeout := *effective.Settings.TimeoutMS; {
		case timeout == 0:
			action.TimeoutMs = nativeMaxTimeoutMS
			diagnostics = append(diagnostics, nativeDiagnostic(
				file.Path,
				"warning",
				"bruno_unbounded_timeout_clamped",
				fmt.Sprintf("Bruno timeout 0 is unbounded; APEX applies its maximum request timeout of %d ms.", nativeMaxTimeoutMS),
				false,
			))
		case timeout < 0:
			return NativeScriptCandidate{}, nil, fmt.Errorf("Bruno request %q has a negative timeout", item.Path)
		case timeout > nativeMaxTimeoutMS:
			action.TimeoutMs = nativeMaxTimeoutMS
			diagnostics = append(diagnostics, nativeDiagnostic(
				file.Path,
				"warning",
				"bruno_timeout_clamped",
				fmt.Sprintf("Bruno timeout %d ms exceeds the APEX maximum and was clamped to %d ms.", timeout, nativeMaxTimeoutMS),
				false,
			))
		default:
			action.TimeoutMs = timeout
		}
	}
	if effective.Settings.FollowRedirects != nil && !*effective.Settings.FollowRedirects {
		action.MaxRedirects = nativeIntPointer(0)
	} else if effective.Settings.MaxRedirects == nil {
		action.MaxRedirects = nativeIntPointer(nativeDefaultRedirects)
	} else {
		redirects := *effective.Settings.MaxRedirects
		if redirects < 0 {
			return NativeScriptCandidate{}, nil, fmt.Errorf("Bruno request %q has a negative maxRedirects", item.Path)
		}
		if redirects > nativeMaxRedirects {
			action.MaxRedirects = nativeIntPointer(nativeMaxRedirects)
			diagnostics = append(diagnostics, nativeDiagnostic(
				file.Path,
				"warning",
				"bruno_max_redirects_clamped",
				fmt.Sprintf("Bruno maxRedirects %d exceeds the APEX maximum; the saved draft is clamped to %d and must be reviewed before execution.", redirects, nativeMaxRedirects),
				true,
			))
			reviewReasons = append(reviewReasons, "bruno_max_redirects_clamped")
		} else {
			action.MaxRedirects = nativeIntPointer(redirects)
		}
	}
	if effective.Settings.EncodeURL != nil && !*effective.Settings.EncodeURL {
		diagnostic := nativeDiagnostic(
			file.Path,
			"warning",
			"bruno_encode_url_false_review_required",
			"APEX cannot guarantee Bruno's encodeUrl=false wire behavior; review and translate this request before execution.",
			true,
		)
		diagnostics = append(diagnostics, diagnostic)
		reviewReasons = append(reviewReasons, diagnostic.Code)
	}

	convertedHookCount := 0
	reviewHookCount := 0
	hookHashes := make([]string, 0, len(effective.Hooks))
	for _, hook := range effective.Hooks {
		hookHashes = append(hookHashes, sha256Hex([]byte(hook.Code)))
		if hook.Type != "after-response" && hook.Type != "after_response" && hook.Type != "tests" {
			diagnostic := nativeDiagnostic(
				hook.Path,
				"warning",
				"bruno_javascript_review_required",
				fmt.Sprintf("The %s JavaScript hook cannot be represented by the native HTTP DSL.", firstNonEmptyNative(hook.Type, "unspecified")),
				true,
			)
			diagnostics = append(diagnostics, diagnostic)
			reviewReasons = append(reviewReasons, diagnostic.Code)
			reviewHookCount++
			continue
		}
		assertions, recognized := translateNativeAfterResponse(hook.Code)
		if !recognized {
			diagnostic := nativeDiagnostic(
				hook.Path,
				"warning",
				"bruno_javascript_review_required",
				"An after-response JavaScript hook is outside the safe static translation allowlist.",
				true,
			)
			diagnostics = append(diagnostics, diagnostic)
			reviewReasons = append(reviewReasons, diagnostic.Code)
			reviewHookCount++
			continue
		}
		action.Assertions = append(action.Assertions, assertions...)
		convertedHookCount++
	}

	if len(reviewReasons) > 0 {
		action.Metadata["review_required"] = true
		action.Metadata["promotion_blocker"] = true
		action.Metadata["review_reasons"] = append([]string(nil), reviewReasons...)
		action.Metadata["review_gate"] = reviewReasons[0]
		for _, reason := range reviewReasons {
			if reason == "bruno_javascript_review_required" {
				action.Metadata["review_gate"] = "bruno_javascript"
				break
			}
			if reason == "bruno_auth_review_required" {
				action.Metadata["review_gate"] = "bruno_auth"
			}
		}
	}

	action, requestReport, err := finalizeNativeRequestAction(action, item, file, bodyType)
	if err != nil {
		return NativeScriptCandidate{}, nil, fmt.Errorf(
			"finalize Bruno native request %q through the shared request pipeline: %w",
			item.Path,
			err,
		)
	}

	parameters := make([]dsl.Parameter, 0, len(templateState.parameterValues))
	for _, variable := range effective.Variables {
		value, used := templateState.parameterValues[variable.Name]
		if !used {
			continue
		}
		parameters = append(parameters, dsl.Parameter{
			Name:      variable.Name,
			Type:      "list",
			Update:    "once",
			Selection: "sequential",
			Values:    []string{value},
		})
	}

	traceparent := false
	cookiesEnabled := true
	clearCookies := true
	plannedID := uuid.NewString()
	createdAt := time.Now().UTC()
	script := &dsl.Script{
		ID:            plannedID,
		Name:          item.Name,
		Version:       "1.0",
		Protocol:      "http",
		ProjectID:     input.ProjectID,
		GeneratedBy:   nativeImporterVersion,
		GeneratedFrom: "open_collection_yaml",
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
		Config: dsl.ScriptConfig{
			// Native Bruno actions retain their authored absolute/template URL.
			// APEX requires a non-empty HTTP base URL structurally; using the
			// same source URL is inert once the action resolves to an absolute
			// URL and avoids inventing a synthetic network destination.
			BaseURL: target,
			HTTP: dsl.HTTPConfig{
				CookiesEnabled:      &cookiesEnabled,
				ClearCookiesPerIter: &clearCookies,
				Traceparent:         &traceparent,
			},
			RunLogic: dsl.RunLogicConfig{AutoTransactions: "none"},
			// Unset: the scenario continue-on-error policy decides.
			ErrorHandling: dsl.ErrorHandlingConfig{},
		},
		Parameters: parameters,
		Actions:    []dsl.Action{action},
	}

	missing := sortedNativeSet(templateState.missing)
	if len(missing) > 0 {
		diagnostics = append(diagnostics, nativeDiagnostic(
			file.Path,
			"warning",
			"bruno_missing_variables",
			fmt.Sprintf("%d unresolved Bruno variable(s) must be seeded before execution.", len(missing)),
			false,
		))
		action.Metadata["execution_blocker"] = "bruno_missing_variables"
		action.Metadata["missing_variables"] = append([]string(nil), missing...)
		script.Actions[0] = action
	}

	projectionRequirementsByID := make(map[string]SecretRequirement)
	for _, requirementGroup := range sourceRequirements {
		for _, requirement := range requirementGroup {
			projectionRequirementsByID[requirement.ID] = requirement
		}
	}
	for _, requirement := range templateState.generatedReq {
		requirement = finalizeNativeSecretRequirement(requirement)
		projectionRequirementsByID[requirement.ID] = requirement
	}
	projectionRequirements := make([]SecretRequirement, 0, len(projectionRequirementsByID))
	for _, requirement := range projectionRequirementsByID {
		projectionRequirements = append(projectionRequirements, requirement)
	}
	sort.Slice(projectionRequirements, func(i, j int) bool {
		return projectionRequirements[i].ID < projectionRequirements[j].ID
	})
	publicScript, _, err := finalizeNativeScriptCredentialTargets(
		script,
		projectionRequirements,
	)
	if err != nil {
		return NativeScriptCandidate{}, nil, fmt.Errorf(
			"project Bruno native credential targets for %q: %w",
			item.Path,
			err,
		)
	}

	secretNames := collectNativeSecretNames(publicScript)
	requirements := make([]SecretRequirement, 0, len(secretNames))
	seenRequirements := make(map[string]struct{})
	for _, secretName := range secretNames {
		normalized := strings.ToUpper(secretName)
		for _, requirement := range sourceRequirements[normalized] {
			if _, seen := seenRequirements[requirement.ID]; seen {
				continue
			}
			requirement = finalizeNativeSecretRequirement(requirement)
			requirement.AffectedPaths = nativeSecretRequirementPaths(file.Path)
			seenRequirements[requirement.ID] = struct{}{}
			requirements = append(requirements, requirement)
		}
		if generated, exists := templateState.generatedReq[normalized]; exists {
			generated = finalizeNativeSecretRequirement(generated)
			if _, seen := seenRequirements[generated.ID]; !seen {
				generated.AffectedPaths = nativeSecretRequirementPaths(file.Path)
				seenRequirements[generated.ID] = struct{}{}
				requirements = append(requirements, generated)
			}
		}
		if len(sourceRequirements[normalized]) == 0 {
			if _, exists := templateState.generatedReq[normalized]; !exists {
				generated := nativeSecretRequirement(secretName, file.Path, "Existing native secret reference")
				if _, seen := seenRequirements[generated.ID]; !seen {
					seenRequirements[generated.ID] = struct{}{}
					requirements = append(requirements, generated)
				}
			}
		}
	}
	sort.Slice(requirements, func(i, j int) bool {
		if requirements[i].SuggestedName == requirements[j].SuggestedName {
			return requirements[i].ID < requirements[j].ID
		}
		return requirements[i].SuggestedName < requirements[j].SuggestedName
	})

	soapVersion, soapOperation := nativeSOAPHint(body)
	sourceMappings := make([]SourceMapping, 0, len(layers))
	ancestorSummary := make([]map[string]any, 0, len(layers)-1)
	collectionSHA256 := ""
	for _, layer := range layers {
		role := nativeSourceMappingRole(layer.Path, file.Path)
		hash := firstNonEmptyNative(manifestHashes[layer.Path], sha256Hex(file.Data))
		sourceMappings = append(sourceMappings, SourceMapping{Path: layer.Path, Role: role, SHA256: hash})
		switch role {
		case "collection":
			collectionSHA256 = hash
		case "folder":
			ancestorSummary = append(ancestorSummary, map[string]any{
				"path":   layer.Path,
				"uid":    layer.UID,
				"sha256": hash,
			})
		}
	}

	diagnosticIDs := make([]string, 0, len(diagnostics))
	findingIDs := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		diagnosticIDs = append(diagnosticIDs, diagnostic.ID)
		if diagnostic.FindingID != "" {
			findingIDs = append(findingIDs, diagnostic.FindingID)
		}
	}
	effectiveFields := map[string]any{
		"header_names":      nativeHeaderNames(headers),
		"variable_names":    nativeVariableNames(effective.Variables),
		"auth_mode":         effective.Auth.Mode,
		"timeout_ms":        action.TimeoutMs,
		"max_redirects":     *action.MaxRedirects,
		"traceparent":       false,
		"clear_cookies":     true,
		"request_method":    method,
		"request_body_type": anyString(nativeLookup(bodyBlock, "type")),
	}
	if effective.Settings.EncodeURL != nil {
		effectiveFields["encode_url"] = *effective.Settings.EncodeURL
	}
	script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Source:           "bruno",
		SourceScriptName: item.Name,
		Preserved: map[string]any{
			"schema_version":         ImporterVersion,
			"native_importer":        nativeImporterVersion,
			"collection_name":        collection.Name,
			"collection_uid":         collection.UID,
			"collection_sha256":      collectionSHA256,
			"manifest_sha256":        manifest.SHA256,
			"source_path":            item.Path,
			"source_uid":             item.UID,
			"source_sequence":        item.Sequence,
			"source_sha256":          manifestHashes[file.Path],
			"ancestor_folders":       ancestorSummary,
			"effective_headers":      nativeHeaderNames(headers),
			"effective_auth":         effective.Auth.Mode,
			"soap_version_hint":      soapVersion,
			"soap_operation_hint":    soapOperation,
			"hook_hashes":            hookHashes,
			"converted_hook_count":   convertedHookCount,
			"review_hook_count":      reviewHookCount,
			"diagnostic_ids":         diagnosticIDs,
			"finding_ids":            findingIDs,
			"effective_fields":       effectiveFields,
			"missing_variable_count": len(missing),
		},
	}}

	finalized, err := requestimport.FinalizeScript(
		script,
		[]requestimport.Report{requestReport},
		requestimport.FinalizeOptions{},
	)
	if err != nil {
		return NativeScriptCandidate{}, nil, fmt.Errorf(
			"finalize Bruno native script %q through the shared request pipeline: %w",
			item.Path,
			err,
		)
	}
	script = finalized.Script
	script, credentialTargets, err := finalizeNativeScriptCredentialTargets(
		script,
		projectionRequirements,
	)
	if err != nil {
		return NativeScriptCandidate{}, nil, fmt.Errorf(
			"finalize Bruno native credential targets for %q: %w",
			item.Path,
			err,
		)
	}

	if issues := dsl.ValidateScriptIssues(script); len(issues) > 0 {
		return NativeScriptCandidate{}, nil, fmt.Errorf("native Bruno script %q is invalid: %s", item.Path, strings.Join(issues, "; "))
	}

	fidelity := "exact"
	for _, diagnostic := range diagnostics {
		if diagnostic.AcknowledgementRequired {
			fidelity = "review_required"
			break
		}
		if strings.Contains(diagnostic.Code, "clamped") {
			fidelity = "exact_with_runtime_bound"
		}
	}
	candidateSeed := item.UID + "\x00" + item.Path
	candidateHash := sha256.Sum256([]byte(candidateSeed))
	candidateID := hex.EncodeToString(candidateHash[:16])
	candidate := NativeScriptCandidate{
		CandidateID:        candidateID,
		PlannedScriptID:    plannedID,
		SourcePath:         item.Path,
		SourceUID:          item.UID,
		Sequence:           item.Sequence,
		Method:             method,
		Target:             script.Actions[0].Target,
		Script:             script,
		Fidelity:           fidelity,
		Diagnostics:        diagnostics,
		MissingVariables:   missing,
		SecretRequirements: requirements,
		SourceMappings:     sourceMappings,
		CredentialTargets:  credentialTargets,
	}

	var candidateFindings []SecurityFinding
	for _, diagnostic := range diagnostics {
		if !diagnostic.AcknowledgementRequired {
			continue
		}
		candidateFindings = append(candidateFindings, SecurityFinding{
			ID:       diagnostic.FindingID,
			Severity: diagnostic.Severity,
			Code:     diagnostic.Code,
			Path:     diagnostic.Path,
			Summary:  diagnostic.Message,
		})
	}
	return candidate, candidateFindings, nil
}

func rewriteNativeTemplates(value string, context nativeTemplateContext, contextName string, state *nativeTemplateState) string {
	return brunoTemplatePattern.ReplaceAllStringFunc(value, func(template string) string {
		match := brunoTemplatePattern.FindStringSubmatch(template)
		if len(match) < 2 {
			return template
		}
		expression := strings.TrimSpace(match[1])
		for _, prefix := range []string{"env.", "data.", "var.", "param.", "secret.", "fn.", "fake.", "file."} {
			if strings.HasPrefix(expression, prefix) {
				if prefix == "secret." {
					name, err := engine.NormalizeLogicalSecretName(strings.TrimSpace(expression[len(prefix):]))
					if err == nil {
						return "{{secret." + name + "}}"
					}
				}
				if nativeTemplateRequiresSecret(expression, context, contextName) {
					requirement := nativeUnresolvedSourceVariableSecretRequirement(
						expression,
						state.sourcePath,
						nativeTemplateContextLabel(context, contextName),
					)
					registerNativeGeneratedRequirement(state, requirement)
					return nativeCredentialProjectionReplacement(requirement)
				}
				return "{{" + expression + "}}"
			}
		}
		if strings.Contains(expression, "(") || !nativeTemplateNamePattern.MatchString(expression) {
			state.missing[expression] = struct{}{}
			return "{{var." + expression + "}}"
		}
		if variable, defined := state.defined[expression]; defined {
			requiresSecret := nativeTemplateRequiresSecret(expression, context, contextName)
			beforeMissing := make(map[string]struct{}, len(state.missing))
			if requiresSecret {
				for missing := range state.missing {
					beforeMissing[missing] = struct{}{}
				}
			}
			resolution := resolveNativeVariable(expression, state)
			if requiresSecret {
				// The source expression is replaced wholesale by a governed
				// secret, so unresolved dependencies inside it are no longer
				// executable inputs.
				for missing := range state.missing {
					if _, existed := beforeMissing[missing]; !existed {
						delete(state.missing, missing)
					}
				}
				if (context == nativeTemplateAuthUsername || context == nativeTemplateAuthPassword) &&
					value == template &&
					resolution.Complete &&
					resolution.Value == "" {
					return ""
				}
				if secretReference, ok := exactNativeSecretReference(resolution.Value); ok {
					return secretReference
				}
				candidateScope := ""
				if len(resolution.Dependencies) > 1 {
					candidateScope = state.sourcePath
				}
				requirement := nativeVariableSecretRequirement(
					variable,
					state.sourcePath,
					nativeTemplateContextLabel(context, contextName),
					candidateScope,
				)
				requirement.scrubTargets = nativeVariableScrubTargets(resolution.Dependencies)
				requirement.DisplayLabel = nativeSourceVariableDisplayLabel(expression)
				requirement.SourceVariable = expression
				if resolution.Complete && !strings.Contains(resolution.Value, "{{") {
					requirement.Value = recoverableBrunoCredentialValue(resolution.Value)
				}
				requirement.UnresolvedSourceVariable = requirement.Value == ""
				registerNativeGeneratedRequirement(state, requirement)
				return nativeCredentialProjectionReplacement(requirement)
			}
			// APEX expands action fields once. Keep values containing an APEX
			// namespace in the action itself instead of hiding that template
			// behind a one-value parameter that would resolve too late.
			if !resolution.Complete || resolution.RuntimeBound ||
				strings.Contains(resolution.Value, "{{") {
				return resolution.Value
			}
			state.usedParams[expression] = struct{}{}
			state.parameterValues[expression] = resolution.Value
			return "{{param." + expression + "}}"
		}
		if nativeTemplateRequiresSecret(expression, context, contextName) {
			requirement := nativeUnresolvedSourceVariableSecretRequirement(
				expression,
				state.sourcePath,
				nativeTemplateContextLabel(context, contextName),
			)
			registerNativeGeneratedRequirement(state, requirement)
			return nativeCredentialProjectionReplacement(requirement)
		}
		state.missing[expression] = struct{}{}
		return "{{var." + expression + "}}"
	})
}

func rewriteNativeComposedSensitiveHeader(
	name, value, sourcePath, pointer string,
	occurrence int,
	state *nativeTemplateState,
	sourceRequirements map[string][]SecretRequirement,
) (string, bool) {
	context := nativeTemplateContextLabel(nativeTemplateSensitiveHeader, name)
	requirement := nativeComposedHeaderSecretRequirement(
		sourcePath,
		pointer,
		name,
		occurrence,
		context,
		state.sourcePath,
	)
	_, projectedRequirementAvailable := nativeSourceSecretValue(
		requirement.Replacement,
		sourceRequirements,
	)
	beforeMissing := make(map[string]struct{}, len(state.missing))
	for missing := range state.missing {
		beforeMissing[missing] = struct{}{}
	}
	dependencies := make([]nativeVariable, 0)
	complete := true
	projectedWholeCredential := false
	resolved := brunoTemplatePattern.ReplaceAllStringFunc(value, func(template string) string {
		match := brunoTemplatePattern.FindStringSubmatch(template)
		if len(match) < 2 {
			complete = false
			return template
		}
		expression := strings.TrimSpace(match[1])
		for _, prefix := range []string{"env.", "data.", "var.", "param.", "secret.", "fn.", "fake.", "file."} {
			if strings.HasPrefix(expression, prefix) {
				complete = false
				return template
			}
		}
		if strings.Contains(expression, "(") || !nativeTemplateNamePattern.MatchString(expression) {
			complete = false
			return template
		}
		if _, exists := state.defined[expression]; !exists {
			complete = false
			return template
		}
		resolution := resolveNativeVariable(expression, state)
		dependencies = appendNativeVariableDependencies(dependencies, resolution.Dependencies...)
		if secretReference, ok := exactNativeSecretReference(resolution.Value); ok {
			if projectedRequirementAvailable {
				projectedWholeCredential = true
				return resolution.Value
			}
			if sourceValue, available := nativeSourceSecretValue(secretReference, sourceRequirements); available {
				return sourceValue
			}
		}
		if !resolution.Complete {
			complete = false
		}
		return resolution.Value
	})
	if brunoTemplatePattern.MatchString(resolved) {
		complete = false
	}
	if projectedWholeCredential && len(dependencies) > 0 {
		complete = true
	}
	for missing := range state.missing {
		if _, existed := beforeMissing[missing]; !existed {
			delete(state.missing, missing)
		}
	}

	requirement.scrubTargets = nativeVariableScrubTargets(dependencies)
	if strings.TrimSpace(pointer) != "" {
		requirement.scrubTargets = append(
			requirement.scrubTargets,
			nativeSecretScrubTarget{
				Path:    sourcePath,
				Pointer: pointer,
			},
		)
	}
	if complete && !brunoTemplatePattern.MatchString(resolved) {
		requirement.Value = recoverableBrunoCredentialValue(resolved)
	}
	requirement = describeNativeHeaderSecretRequirement(requirement, name, value)
	registerNativeGeneratedRequirement(state, requirement)
	return nativeCredentialProjectionReplacement(requirement), complete
}

func registerNativeGeneratedRequirement(
	state *nativeTemplateState,
	requirement SecretRequirement,
) {
	requirement.Value = recoverableBrunoCredentialValue(requirement.Value)
	key := strings.ToUpper(strings.TrimSpace(requirement.SuggestedName))
	existing, exists := state.generatedReq[key]
	if !exists {
		initializeNativeSecretRequirementMergeState(&requirement)
		state.generatedReq[key] = requirement
		return
	}
	if existing.ID != requirement.ID {
		state.generatedErr = fmt.Errorf(
			"distinct credential requirements share destination binding %q",
			requirement.SuggestedName,
		)
		return
	}
	state.generatedReq[key] = mergeNativeSecretRequirements(existing, requirement)
}

func mergeNativeSecretRequirements(
	existing SecretRequirement,
	additional SecretRequirement,
) SecretRequirement {
	existing.Value = recoverableBrunoCredentialValue(existing.Value)
	additional.Value = recoverableBrunoCredentialValue(additional.Value)
	initializeNativeSecretRequirementMergeState(&existing)
	appendNativeSecretRequirementPaths(&existing, additional)
	appendNativeSecretRequirementScrubTargets(&existing, additional.scrubTargets)
	if existing.Value == "" {
		existing.Value = additional.Value
	} else if additional.Value != "" && existing.Value != additional.Value {
		// A single logical secret cannot safely migrate two different source
		// values. Retain the binding requirement but force an explicit value.
		existing.Value = ""
	}
	if existing.Context == "" {
		existing.Context = additional.Context
	}
	if existing.DisplayLabel == "" {
		existing.DisplayLabel = additional.DisplayLabel
	}
	if existing.SourceVariable == "" {
		existing.SourceVariable = additional.SourceVariable
	}
	existing.UnresolvedSourceVariable = existing.UnresolvedSourceVariable || additional.UnresolvedSourceVariable
	return existing
}

func initializeNativeSecretRequirementMergeState(requirement *SecretRequirement) {
	if requirement == nil {
		return
	}
	if requirement.affectedPathSet == nil {
		paths := requirement.AffectedPaths
		if len(paths) == 0 && strings.TrimSpace(requirement.Path) != "" {
			paths = []string{requirement.Path}
		}
		requirement.affectedPathSet = make(map[string]struct{}, len(paths))
		requirement.AffectedPaths = requirement.AffectedPaths[:0]
		for _, sourcePath := range paths {
			sourcePath = strings.TrimSpace(sourcePath)
			if sourcePath == "" {
				continue
			}
			if _, duplicate := requirement.affectedPathSet[sourcePath]; duplicate {
				continue
			}
			requirement.affectedPathSet[sourcePath] = struct{}{}
			requirement.AffectedPaths = append(requirement.AffectedPaths, sourcePath)
		}
	}
	if requirement.scrubTargetSet == nil {
		requirement.scrubTargetSet = make(map[string]struct{}, len(requirement.scrubTargets))
		targets := requirement.scrubTargets
		requirement.scrubTargets = requirement.scrubTargets[:0]
		appendNativeSecretRequirementScrubTargets(requirement, targets)
	}
}

func appendNativeSecretRequirementPaths(
	existing *SecretRequirement,
	additional SecretRequirement,
) {
	paths := additional.AffectedPaths
	if len(paths) == 0 && strings.TrimSpace(additional.Path) != "" {
		paths = []string{additional.Path}
	}
	for _, sourcePath := range paths {
		sourcePath = strings.TrimSpace(sourcePath)
		if sourcePath == "" {
			continue
		}
		if _, duplicate := existing.affectedPathSet[sourcePath]; duplicate {
			continue
		}
		existing.affectedPathSet[sourcePath] = struct{}{}
		existing.AffectedPaths = append(existing.AffectedPaths, sourcePath)
	}
}

func appendNativeSecretRequirementScrubTargets(
	requirement *SecretRequirement,
	additional []nativeSecretScrubTarget,
) {
	for _, target := range additional {
		identity := nativeSecretScrubTargetIdentity(target)
		if _, duplicate := requirement.scrubTargetSet[identity]; duplicate {
			continue
		}
		requirement.scrubTargetSet[identity] = struct{}{}
		requirement.scrubTargets = append(requirement.scrubTargets, target)
	}
}

func finalizeNativeSecretRequirement(requirement SecretRequirement) SecretRequirement {
	requirement.Value = recoverableBrunoCredentialValue(requirement.Value)
	paths := requirement.AffectedPaths
	if len(paths) == 0 && strings.TrimSpace(requirement.Path) != "" {
		paths = []string{requirement.Path}
	}
	requirement.AffectedPaths = nativeSecretRequirementPaths(paths...)
	requirement.scrubTargets = append([]nativeSecretScrubTarget(nil), requirement.scrubTargets...)
	sort.Slice(requirement.scrubTargets, func(i, j int) bool {
		return nativeSecretScrubTargetIdentity(requirement.scrubTargets[i]) <
			nativeSecretScrubTargetIdentity(requirement.scrubTargets[j])
	})
	requirement.affectedPathSet = nil
	requirement.scrubTargetSet = nil
	return requirement
}

func nativeSecretScrubTargetIdentity(target nativeSecretScrubTarget) string {
	return target.Path + "\x00" + target.Pointer + "\x00" + target.Kind + "\x00" +
		target.Selector + "\x00" + strconv.Itoa(target.Index)
}

func rewriteNativeAuthCredential(
	value, field string,
	context nativeTemplateContext,
	auth nativeAuth,
	state *nativeTemplateState,
	sourceRequirements map[string][]SecretRequirement,
) string {
	if !brunoTemplatePattern.MatchString(value) || isExactGovernedSecretReference(value) {
		return rewriteNativeTemplates(value, context, field, state)
	}
	if matches := brunoTemplatePattern.FindAllStringIndex(value, -1); len(matches) == 1 &&
		matches[0][0] == 0 && matches[0][1] == len(value) {
		submatch := brunoTemplatePattern.FindStringSubmatch(value)
		if len(submatch) > 1 {
			expression := strings.TrimSpace(submatch[1])
			if variable, defined := state.defined[expression]; defined {
				resolution := resolveNativeVariable(variable.Name, state)
				if resolution.Complete && resolution.Value == "" {
					return ""
				}
				if len(resolution.Dependencies) <= 1 {
					return rewriteNativeTemplates(value, context, field, state)
				}
			} else {
				return rewriteNativeTemplates(value, context, field, state)
			}
		}
	}
	sourcePath := firstNonEmptyNative(auth.SourcePath, state.sourcePath)
	pointer := auth.Pointer + "/" + nativeJSONPointerEscape(field)
	requirement := nativeAuthSecretRequirement(
		sourcePath,
		pointer,
		field,
		nativeTemplateContextLabel(context, field),
		state.sourcePath,
	)
	rewritten, _ := rewriteNativeComposedCredential(
		value,
		requirement,
		state,
		sourceRequirements,
		nativeSecretScrubTarget{
			Path:    sourcePath,
			Pointer: pointer,
		},
	)
	return rewritten
}

func nativeSourceSecretValue(
	reference string,
	sourceRequirements map[string][]SecretRequirement,
) (string, bool) {
	canonical, ok := exactNativeSecretReference(reference)
	if !ok {
		return "", false
	}
	if match := nativeCredentialMarkerPattern.FindStringSubmatch(canonical); len(match) == 2 {
		requirementID := strings.ToLower(match[1])
		for _, requirements := range sourceRequirements {
			for _, requirement := range requirements {
				value := recoverableBrunoCredentialValue(requirement.Value)
				if strings.EqualFold(requirement.ID, requirementID) && value != "" {
					return value, true
				}
			}
		}
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(canonical, "{{secret."), "}}")
	for _, requirement := range sourceRequirements[strings.ToUpper(name)] {
		if value := recoverableBrunoCredentialValue(requirement.Value); value != "" {
			return value, true
		}
	}
	return "", false
}

func resolveNativeVariable(name string, state *nativeTemplateState) nativeVariableResolution {
	if resolution, ok := state.resolved[name]; ok {
		restoreNativeResolutionMissing(state, resolution)
		return resolution
	}
	if stackPosition, cyclic := state.resolving[name]; cyclic {
		missing := append([]string(nil), state.resolutionStack[stackPosition-1:]...)
		missing = append(missing, name)
		for _, cyclicName := range state.resolutionStack[stackPosition-1:] {
			state.missing[cyclicName] = struct{}{}
		}
		state.missing[name] = struct{}{}
		return nativeVariableResolution{
			Value:    "{{var." + name + "}}",
			Complete: false,
			Missing:  sortedNativeSet(nativeStringSet(missing)),
		}
	}
	variable, exists := state.defined[name]
	if !exists {
		state.missing[name] = struct{}{}
		return nativeVariableResolution{
			Value:    "{{var." + name + "}}",
			Complete: false,
			Missing:  []string{name},
		}
	}

	state.resolutionStack = append(state.resolutionStack, name)
	state.resolving[name] = len(state.resolutionStack)
	complete := true
	runtimeBound := false
	resolutionMissing := make(map[string]struct{})
	dependencies := []nativeVariable{variable}
	value := brunoTemplatePattern.ReplaceAllStringFunc(variable.Value, func(template string) string {
		match := brunoTemplatePattern.FindStringSubmatch(template)
		if len(match) < 2 {
			complete = false
			return template
		}
		expression := strings.TrimSpace(match[1])
		for _, prefix := range []string{"env.", "data.", "var.", "param.", "secret.", "fn.", "fake.", "file."} {
			if !strings.HasPrefix(expression, prefix) {
				continue
			}
			runtimeBound = true
			if prefix == "secret." {
				secretName, err := engine.NormalizeLogicalSecretName(strings.TrimSpace(expression[len(prefix):]))
				if err != nil {
					complete = false
					state.missing[expression] = struct{}{}
					resolutionMissing[expression] = struct{}{}
					return template
				}
				return "{{secret." + secretName + "}}"
			}
			return "{{" + expression + "}}"
		}
		if strings.Contains(expression, "(") || !nativeTemplateNamePattern.MatchString(expression) {
			complete = false
			state.missing[expression] = struct{}{}
			resolutionMissing[expression] = struct{}{}
			return "{{var." + expression + "}}"
		}
		nested, nestedExists := state.defined[expression]
		if !nestedExists {
			complete = false
			state.missing[expression] = struct{}{}
			resolutionMissing[expression] = struct{}{}
			return "{{var." + expression + "}}"
		}
		_ = nested
		resolution := resolveNativeVariable(expression, state)
		dependencies = appendNativeVariableDependencies(dependencies, resolution.Dependencies...)
		if !resolution.Complete {
			complete = false
		}
		for _, missing := range resolution.Missing {
			resolutionMissing[missing] = struct{}{}
		}
		if resolution.RuntimeBound {
			runtimeBound = true
		}
		return resolution.Value
	})
	delete(state.resolving, name)
	state.resolutionStack = state.resolutionStack[:len(state.resolutionStack)-1]

	resolution := nativeVariableResolution{
		Value:        value,
		Complete:     complete,
		RuntimeBound: runtimeBound,
		Dependencies: dependencies,
		Missing:      sortedNativeSet(resolutionMissing),
	}
	state.resolved[name] = resolution
	restoreNativeResolutionMissing(state, resolution)
	return resolution
}

func restoreNativeResolutionMissing(
	state *nativeTemplateState,
	resolution nativeVariableResolution,
) {
	for _, missing := range resolution.Missing {
		state.missing[missing] = struct{}{}
	}
}

func nativeStringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func appendNativeVariableDependencies(
	current []nativeVariable,
	extra ...nativeVariable,
) []nativeVariable {
	seen := make(map[string]struct{}, len(current)+len(extra))
	for _, variable := range current {
		seen[variable.SourcePath+"\x00"+variable.DefinitionPointer] = struct{}{}
	}
	for _, variable := range extra {
		identity := variable.SourcePath + "\x00" + variable.DefinitionPointer
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		current = append(current, variable)
	}
	return current
}

func nativeVariableScrubTargets(variables []nativeVariable) []nativeSecretScrubTarget {
	targets := make([]nativeSecretScrubTarget, 0, len(variables))
	seen := make(map[string]struct{}, len(variables))
	for _, variable := range variables {
		if strings.TrimSpace(variable.SourcePath) == "" ||
			strings.TrimSpace(variable.DefinitionPointer) == "" {
			continue
		}
		identity := variable.SourcePath + "\x00" + variable.DefinitionPointer
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		targets = append(targets, nativeSecretScrubTarget{
			Path:    variable.SourcePath,
			Pointer: variable.DefinitionPointer,
		})
	}
	return targets
}

func exactNativeSecretReference(value string) (string, bool) {
	value = strings.TrimSpace(value)
	match := brunoTemplatePattern.FindStringSubmatch(value)
	if len(match) < 2 || match[0] != value {
		return "", false
	}
	name, isSecret, err := canonicalBrunoSecretExpression(match[1])
	if err != nil || !isSecret {
		return "", false
	}
	return "{{secret." + name + "}}", true
}

func nativeTemplateRequiresSecret(name string, context nativeTemplateContext, contextName string) bool {
	if context == nativeTemplateAuthUsername || context == nativeTemplateAuthPassword ||
		context == nativeTemplateAuthToken ||
		context == nativeTemplateSensitiveHeader || context == nativeTemplateXMLCredential {
		return true
	}
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(name))
	switch normalized {
	case "password", "passwd", "pwd", "pass", "partnerpassword", "token", "authtoken",
		"accesstoken", "refreshtoken", "apikey", "clientsecret", "privatekey",
		"secret", "sessionid":
		return true
	}
	if context == nativeTemplateBody && strings.Contains(normalized, "password") {
		return true
	}
	if context == nativeTemplateSensitiveHeader && strings.TrimSpace(contextName) != "" {
		return true
	}
	return false
}

func nativeTemplateContextLabel(context nativeTemplateContext, name string) string {
	switch context {
	case nativeTemplateAuthUsername:
		return "Bruno HTTP auth username"
	case nativeTemplateAuthPassword:
		return "Bruno HTTP auth password"
	case nativeTemplateAuthToken:
		return "Bruno HTTP auth token"
	case nativeTemplateSensitiveHeader:
		return "Bruno sensitive header " + name
	case nativeTemplateXMLCredential:
		return "Bruno XML body credential " + name
	case nativeTemplateBody:
		return "Bruno request body credential"
	default:
		return "Bruno credential variable"
	}
}

func nativeSecretRequirement(rawName, sourcePath, context string) SecretRequirement {
	name, err := engine.NormalizeLogicalSecretName(rawName)
	if err != nil {
		fallbackHash := sha256.Sum256([]byte(rawName))
		name = "BRUNO_SECRET_" + strings.ToUpper(hex.EncodeToString(fallbackHash[:6]))
	}
	idHash := sha256.Sum256([]byte("bruno-native-secret\x00" + name))
	return SecretRequirement{
		ID:            hex.EncodeToString(idHash[:12]),
		Path:          sourcePath,
		Key:           rawName,
		SuggestedName: name,
		Replacement:   "{{secret." + name + "}}",
		Context:       context,
		AffectedPaths: nativeSecretRequirementPaths(sourcePath),
	}
}

func nativeSourceVariableSecretName(sourceVariable string) string {
	name := nativeCamelCaseBoundary.ReplaceAllString(strings.TrimSpace(sourceVariable), `${1}_${2}`)
	name = strings.NewReplacer("-", "_", ".", "_").Replace(name)
	name = strings.ReplaceAll(strings.ToUpper(name), "USER_NAME", "USERNAME")
	// Bruno variable names are case-sensitive while APEX logical secret names
	// are normalized. Always scope unresolved authored variables with their raw
	// identity so names such as clientId and client_id cannot silently share a
	// runtime secret. Repeated uses of the same raw name retain the same suffix.
	identityHash := sha256.Sum256([]byte("bruno-native-unresolved-variable-name\x00" + sourceVariable))
	// Keep the same 96-bit identity strength as requirement IDs. This name is
	// also the generated-requirement map key, so a shorter, attacker-searchable
	// suffix could merge two raw variables before the aggregate collision gate.
	suffix := "_" + strings.ToUpper(hex.EncodeToString(identityHash[:12]))
	if len(name)+len(suffix) > credential.MaxLogicalNameBytes {
		name = strings.TrimRight(name[:credential.MaxLogicalNameBytes-len(suffix)], "_")
	}
	return name + suffix
}

func nativeSourceVariableDisplayLabel(sourceVariable string) string {
	label := nativeCamelCaseBoundary.ReplaceAllString(strings.TrimSpace(sourceVariable), `${1} ${2}`)
	label = strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(label)
	words := strings.Fields(strings.ToLower(label))
	if len(words) == 0 {
		return "Credential"
	}
	for index, word := range words {
		switch word {
		case "id", "api", "url", "uri", "jwt", "csrf", "xsrf":
			words[index] = strings.ToUpper(word)
		}
	}
	label = strings.ReplaceAll(strings.Join(words, " "), "user name", "username")
	runes := []rune(label)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func nativeHeaderDisplayLabel(headerName string) string {
	label := nativeCamelCaseBoundary.ReplaceAllString(strings.TrimSpace(headerName), `${1} ${2}`)
	parts := strings.Fields(strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(label))
	words := make([]string, 0, len(parts)+1)
	for _, part := range parts {
		switch strings.ToLower(part) {
		case "idpctx":
			words = append(words, "IDP", "context")
		case "idp":
			words = append(words, "IDP")
		case "ixp":
			words = append(words, "IXP")
		case "cdf":
			words = append(words, "CDF")
		case "api":
			words = append(words, "API")
		case "csrf":
			words = append(words, "CSRF")
		case "xsrf":
			words = append(words, "XSRF")
		case "jwt":
			words = append(words, "JWT")
		case "oauth":
			words = append(words, "OAuth")
		case "id":
			words = append(words, "ID")
		case "x":
			words = append(words, "X")
		default:
			words = append(words, strings.ToLower(part))
		}
	}
	if len(words) == 0 {
		return "Sensitive header"
	}
	if words[0] == "X" && len(words) > 1 && words[1] == strings.ToUpper(words[1]) {
		words[0] += "-" + words[1]
		words = append(words[:1], words[2:]...)
	}
	if words[0] == strings.ToLower(words[0]) {
		runes := []rune(words[0])
		runes[0] = unicode.ToUpper(runes[0])
		words[0] = string(runes)
	}
	return strings.Join(words, " ")
}

func describeNativeHeaderSecretRequirement(
	requirement SecretRequirement,
	headerName, authoredValue string,
) SecretRequirement {
	headerName = strings.TrimSpace(headerName)
	if headerName == "" {
		return requirement
	}
	requirement.DisplayLabel = nativeHeaderDisplayLabel(headerName)
	requirement.AffectedPaths = nativeSecretRequirementPaths(
		append(requirement.AffectedPaths, requirement.Path)...,
	)
	if sourceVariable, exact := exactNativeCredentialSourceReference(authoredValue); exact {
		requirement.SourceVariable = sourceVariable
		requirement.UnresolvedSourceVariable = requirement.Value == ""
		if requirement.Value == "" {
			requirement.Context = fmt.Sprintf(
				"Sensitive Bruno request header %q references {{%s}}, but the collection does not include a value for that source variable. A configured destination value is required when this request runs.",
				headerName,
				sourceVariable,
			)
		} else {
			requirement.Context = fmt.Sprintf(
				"Sensitive Bruno request header %q references {{%s}}. Its imported value can be preserved for this request.",
				headerName,
				sourceVariable,
			)
		}
		return requirement
	}
	if requirement.Value != "" {
		requirement.Context = fmt.Sprintf(
			"Sensitive Bruno request header %q contains an imported value that can be preserved for this request.",
			headerName,
		)
	} else {
		requirement.Context = fmt.Sprintf(
			"Sensitive Bruno request header %q has no recoverable imported value. A configured destination value is required when this request runs.",
			headerName,
		)
	}
	return requirement
}

func nativeUnresolvedSourceVariableSecretRequirement(
	sourceVariable, sourcePath, context string,
) SecretRequirement {
	requirement := nativeSecretRequirement(
		nativeSourceVariableSecretName(sourceVariable),
		sourcePath,
		context,
	)
	requirement.Key = sourceVariable
	requirement.DisplayLabel = nativeSourceVariableDisplayLabel(sourceVariable)
	requirement.SourceVariable = sourceVariable
	requirement.UnresolvedSourceVariable = true
	return requirement
}

func exactNativeCredentialSourceReference(value string) (string, bool) {
	value = strings.TrimSpace(value)
	match := brunoTemplatePattern.FindStringSubmatch(value)
	if len(match) < 2 || match[0] != value {
		return "", false
	}
	expression := strings.TrimSpace(match[1])
	if expression == "" || strings.Contains(expression, "(") ||
		!nativeTemplateNamePattern.MatchString(expression) {
		return "", false
	}
	lower := strings.ToLower(expression)
	// Namespaced runtime expressions are still authored source identities in a
	// credential scalar. Preserve that identity when generating a project-secret
	// binding. A valid secret.* reference was already returned by
	// exactNativeSecretReference; do not reclassify one as an unresolved source.
	if strings.HasPrefix(lower, "secret.") {
		return "", false
	}
	return expression, true
}

func nativeSecretRequirementPaths(paths ...string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, sourcePath := range paths {
		sourcePath = strings.TrimSpace(sourcePath)
		if sourcePath == "" {
			continue
		}
		if _, exists := seen[sourcePath]; exists {
			continue
		}
		seen[sourcePath] = struct{}{}
		out = append(out, sourcePath)
	}
	sort.Strings(out)
	return out
}

func nativeComposedHeaderSecretRequirement(
	sourcePath, pointer, headerName string,
	occurrence int,
	context string,
	candidatePaths ...string,
) SecretRequirement {
	identity := pointer + "\x00" + strings.ToLower(strings.TrimSpace(headerName)) +
		"\x00" + strconv.Itoa(occurrence)
	if len(candidatePaths) > 0 && strings.TrimSpace(candidatePaths[0]) != "" &&
		candidatePaths[0] != sourcePath {
		identity += "\x00candidate\x00" + candidatePaths[0]
	}
	nameHash := sha256.Sum256([]byte("bruno-native-header-name\x00" + sourcePath + "\x00" + identity))
	suffix := "_" + strings.ToUpper(hex.EncodeToString(nameHash[:6]))
	name := secretName(sourcePath, "header_"+headerName)
	if len(name)+len(suffix) > credential.MaxLogicalNameBytes {
		name = strings.TrimRight(name[:credential.MaxLogicalNameBytes-len(suffix)], "_")
	}
	name += suffix
	idHash := sha256.Sum256([]byte("bruno-native-header\x00" + sourcePath + "\x00" + identity))
	return SecretRequirement{
		ID:            hex.EncodeToString(idHash[:12]),
		Path:          sourcePath,
		Key:           "native-header:" + strings.ReplaceAll(identity, "\x00", ":"),
		SuggestedName: name,
		Replacement:   "{{secret." + name + "}}",
		Context:       context,
		DisplayLabel:  nativeHeaderDisplayLabel(headerName),
		AffectedPaths: nativeCandidateAffectedPaths(sourcePath, candidatePaths...),
	}
}

func nativeVariableSecretRequirement(
	variable nativeVariable,
	fallbackPath, context string,
	candidateScopes ...string,
) SecretRequirement {
	if strings.TrimSpace(variable.SourcePath) == "" || strings.TrimSpace(variable.DefinitionPointer) == "" {
		return nativeSecretRequirement(variable.Name, fallbackPath, context)
	}
	sourceIdentity := variable.SourcePath + "\x00" + variable.DefinitionPointer
	if len(candidateScopes) > 0 && strings.TrimSpace(candidateScopes[0]) != "" &&
		candidateScopes[0] != variable.SourcePath {
		sourceIdentity += "\x00candidate\x00" + candidateScopes[0]
	}
	nameHash := sha256.Sum256([]byte("bruno-native-variable-name\x00" + sourceIdentity))
	suffix := "_" + strings.ToUpper(hex.EncodeToString(nameHash[:6]))
	name := secretName(variable.SourcePath, variable.Name)
	if len(name)+len(suffix) > credential.MaxLogicalNameBytes {
		name = strings.TrimRight(name[:credential.MaxLogicalNameBytes-len(suffix)], "_")
	}
	name += suffix
	idHash := sha256.Sum256([]byte("bruno-native-variable\x00" + sourceIdentity))
	return SecretRequirement{
		ID:            hex.EncodeToString(idHash[:12]),
		Path:          variable.SourcePath,
		Key:           nativeVariableRequirementKeyPrefix + variable.DefinitionPointer,
		SuggestedName: name,
		Replacement:   "{{secret." + name + "}}",
		Context:       context,
		AffectedPaths: nativeSecretRequirementPaths(fallbackPath),
	}
}

func nativeAuthSecretRequirement(
	sourcePath, pointer, field, context string,
	candidatePaths ...string,
) SecretRequirement {
	identity := pointer + "\x00" + strings.ToLower(field)
	if len(candidatePaths) > 0 && strings.TrimSpace(candidatePaths[0]) != "" &&
		candidatePaths[0] != sourcePath {
		identity += "\x00candidate\x00" + candidatePaths[0]
	}
	nameHash := sha256.Sum256([]byte("bruno-native-auth-name\x00" + sourcePath + "\x00" + identity))
	suffix := "_" + strings.ToUpper(hex.EncodeToString(nameHash[:6]))
	name := secretName(sourcePath, "auth_"+field)
	if len(name)+len(suffix) > credential.MaxLogicalNameBytes {
		name = strings.TrimRight(name[:credential.MaxLogicalNameBytes-len(suffix)], "_")
	}
	name += suffix
	idHash := sha256.Sum256([]byte("bruno-native-auth\x00" + sourcePath + "\x00" + identity))
	return SecretRequirement{
		ID:            hex.EncodeToString(idHash[:12]),
		Path:          sourcePath,
		Key:           "native-auth:" + pointer,
		SuggestedName: name,
		Replacement:   "{{secret." + name + "}}",
		Context:       context,
		AffectedPaths: nativeCandidateAffectedPaths(sourcePath, candidatePaths...),
	}
}

func nativeCandidateAffectedPaths(sourcePath string, candidatePaths ...string) []string {
	paths := nativeSecretRequirementPaths(candidatePaths...)
	if len(paths) > 0 {
		return paths
	}
	return nativeSecretRequirementPaths(sourcePath)
}

func nativeSensitiveBodyFieldName(name string) bool {
	return scrub.IsCredentialField(name) || scrub.SensitiveName(name)
}

func nativeBodyFieldSecretRequirement(
	sourcePath, pointer, bodyKind, field string,
	occurrence int,
) SecretRequirement {
	identity := pointer + "\x00" + bodyKind + "\x00" +
		strings.ToLower(field) + "\x00" + strconv.Itoa(occurrence)
	nameHash := sha256.Sum256([]byte("bruno-native-body-field-name\x00" + sourcePath + "\x00" + identity))
	suffix := "_" + strings.ToUpper(hex.EncodeToString(nameHash[:6]))
	name := secretName(sourcePath, "body_"+bodyKind+"_"+field)
	if len(name)+len(suffix) > credential.MaxLogicalNameBytes {
		name = strings.TrimRight(name[:credential.MaxLogicalNameBytes-len(suffix)], "_")
	}
	name += suffix
	idHash := sha256.Sum256([]byte("bruno-native-body-field\x00" + sourcePath + "\x00" + identity))
	return SecretRequirement{
		ID:            hex.EncodeToString(idHash[:12]),
		Path:          sourcePath,
		Key:           "native-body-field:" + pointer,
		SuggestedName: name,
		Replacement:   "{{secret." + name + "}}",
		Context:       "Bruno " + bodyKind + " body credential " + field,
		AffectedPaths: nativeSecretRequirementPaths(sourcePath),
	}
}

func collectNativeSecretNames(script *dsl.Script) []string {
	payload, err := yaml.Marshal(script)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	for _, match := range brunoTemplatePattern.FindAllSubmatch(payload, -1) {
		if len(match) < 2 {
			continue
		}
		expression := strings.TrimSpace(string(match[1]))
		if !strings.HasPrefix(strings.ToLower(expression), "secret.") {
			continue
		}
		name, err := engine.NormalizeLogicalSecretName(expression[len("secret."):])
		if err == nil {
			seen[name] = struct{}{}
		}
	}
	return sortedNativeSet(seen)
}

func translateNativeAfterResponse(code string) ([]dsl.Assertion, bool) {
	lines := strings.Split(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
	assertions := make([]dsl.Assertion, 0, 8)
	type regexAssignment struct {
		pattern string
		used    bool
	}
	type captureAssignment struct {
		pattern string
		used    bool
	}
	type testBlock struct {
		bodyVariables  map[string]bool
		regexMatches   map[string]regexAssignment
		captureMatches map[string]captureAssignment
		declarations   map[string]struct{}
		assertionStart int
		statements     int
	}
	var block *testBlock
	recognizedTests := 0
	inBlockComment := false

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if inBlockComment {
			end := strings.Index(line, "*/")
			if end < 0 {
				continue
			}
			inBlockComment = false
			if strings.TrimSpace(line[end+2:]) != "" {
				return nil, false
			}
			continue
		}
		if strings.HasPrefix(line, "/*") {
			end := strings.Index(line[2:], "*/")
			if end < 0 {
				inBlockComment = true
				continue
			}
			end += 2
			if strings.TrimSpace(line[end+2:]) != "" {
				return nil, false
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if comment := strings.Index(line, "//"); comment > 0 && strings.HasSuffix(strings.TrimSpace(line[:comment]), ";") {
			line = strings.TrimSpace(line[:comment])
		}
		if match := nativeJSOneLineStatus.FindStringSubmatch(line); len(match) == 2 {
			if block != nil {
				return nil, false
			}
			status, err := strconv.Atoi(match[1])
			if err != nil {
				return nil, false
			}
			assertions = append(assertions, dsl.Assertion{Type: "status_code", Expected: status})
			recognizedTests++
			continue
		}
		if nativeJSTestStartPattern.MatchString(line) {
			if block != nil {
				return nil, false
			}
			block = &testBlock{
				bodyVariables:  make(map[string]bool),
				regexMatches:   make(map[string]regexAssignment),
				captureMatches: make(map[string]captureAssignment),
				declarations:   make(map[string]struct{}),
				assertionStart: len(assertions),
			}
			continue
		}
		if nativeJSTestEndPattern.MatchString(line) {
			if block == nil || block.statements == 0 || len(assertions) == block.assertionStart {
				return nil, false
			}
			for _, used := range block.bodyVariables {
				if !used {
					return nil, false
				}
			}
			for _, assignment := range block.regexMatches {
				if !assignment.used {
					return nil, false
				}
			}
			for _, assignment := range block.captureMatches {
				if !assignment.used {
					return nil, false
				}
			}
			block = nil
			recognizedTests++
			continue
		}
		if block == nil {
			return nil, false
		}
		if match := nativeJSBodyPattern.FindStringSubmatch(line); len(match) == 2 {
			if _, duplicate := block.declarations[match[1]]; duplicate {
				return nil, false
			}
			block.declarations[match[1]] = struct{}{}
			block.bodyVariables[match[1]] = false
			block.statements++
			continue
		}
		if match := nativeJSStatusPattern.FindStringSubmatch(line); len(match) == 2 {
			status, err := strconv.Atoi(match[1])
			if err != nil {
				return nil, false
			}
			assertions = append(assertions, dsl.Assertion{Type: "status_code", Expected: status})
			block.statements++
			continue
		}
		if match := nativeJSHeaderPattern.FindStringSubmatch(line); len(match) == 3 {
			header, ok := nativeJSString(match[1])
			if !ok {
				return nil, false
			}
			expected, ok := nativeJSString(match[2])
			if !ok {
				return nil, false
			}
			assertions = append(assertions, dsl.Assertion{
				Type:     "header_contains",
				Value:    header,
				Expected: expected,
			})
			block.statements++
			continue
		}
		if match := nativeJSRegexPattern.FindStringSubmatch(line); len(match) == 4 {
			if _, duplicate := block.declarations[match[1]]; duplicate {
				return nil, false
			}
			if _, isBody := block.bodyVariables[match[2]]; !isBody {
				return nil, false
			}
			authoredPattern := strings.ReplaceAll(match[3], `\/`, `/`)
			if len(authoredPattern) == 0 || len(authoredPattern) > 4096 {
				return nil, false
			}
			pattern, compatible := translateNativeJSRegexToRE2(authoredPattern)
			if !compatible || len(pattern) > 4096 {
				return nil, false
			}
			compiled, err := regexp.Compile(pattern)
			if err != nil || compiled.NumSubexp() < 1 {
				return nil, false
			}
			block.bodyVariables[match[2]] = true
			block.declarations[match[1]] = struct{}{}
			block.regexMatches[match[1]] = regexAssignment{pattern: pattern}
			block.statements++
			continue
		}
		if match := nativeJSCapturePattern.FindStringSubmatch(line); len(match) == 4 {
			if match[2] != match[3] {
				return nil, false
			}
			if _, duplicate := block.declarations[match[1]]; duplicate {
				return nil, false
			}
			regexAssignment, exists := block.regexMatches[match[2]]
			if !exists {
				return nil, false
			}
			regexAssignment.used = true
			block.regexMatches[match[2]] = regexAssignment
			block.declarations[match[1]] = struct{}{}
			block.captureMatches[match[1]] = captureAssignment{pattern: regexAssignment.pattern}
			block.statements++
			continue
		}
		if match := nativeJSEqualsPattern.FindStringSubmatch(line); len(match) == 3 {
			captureAssignment, exists := block.captureMatches[match[1]]
			if !exists {
				return nil, false
			}
			expected, ok := nativeJSString(match[2])
			if !ok {
				return nil, false
			}
			assertions = append(assertions, dsl.Assertion{
				Type:     "body_regex_capture_equals",
				Value:    captureAssignment.pattern,
				Expected: expected,
			})
			captureAssignment.used = true
			block.captureMatches[match[1]] = captureAssignment
			block.statements++
			continue
		}
		return nil, false
	}
	if inBlockComment || block != nil || recognizedTests == 0 || len(assertions) == 0 {
		return nil, false
	}
	return assertions, true
}

const nativeJSWhitespaceClass = `\t\n\x0B\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}`

// translateNativeJSRegexToRE2 accepts only syntax whose ECMAScript behavior
// can be represented by Go/RE2. JavaScript wildcard and whitespace semantics
// are normalized explicitly; constructs with engine-dependent anchoring,
// Unicode properties, control escapes, or backreferences remain review-gated.
func translateNativeJSRegexToRE2(pattern string) (string, bool) {
	var out strings.Builder
	inClass := false
	for index := 0; index < len(pattern); index++ {
		current := pattern[index]
		if current == '\\' {
			if index+1 >= len(pattern) {
				return "", false
			}
			index++
			escaped := pattern[index]
			switch escaped {
			case 's':
				if inClass {
					out.WriteString(nativeJSWhitespaceClass)
				} else {
					out.WriteByte('[')
					out.WriteString(nativeJSWhitespaceClass)
					out.WriteByte(']')
				}
			case 'S':
				if inClass {
					return "", false
				}
				out.WriteString(`[^` + nativeJSWhitespaceClass + `]`)
			case 'v':
				out.WriteString(`\x0B`)
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				return "", false
			case 'd', 'D', 'w', 'W', 't', 'n', 'r', 'f':
				out.WriteByte('\\')
				out.WriteByte(escaped)
			case 'b', 'B':
				if inClass {
					return "", false
				}
				out.WriteByte('\\')
				out.WriteByte(escaped)
			case 'x':
				if index+2 >= len(pattern) ||
					!nativeASCIIHex(pattern[index+1]) ||
					!nativeASCIIHex(pattern[index+2]) {
					return "", false
				}
				out.WriteString(pattern[index-1 : index+3])
				index += 2
			default:
				if !strings.ContainsRune(`^$\\.*+?()[]{}|/-`, rune(escaped)) {
					return "", false
				}
				out.WriteByte('\\')
				out.WriteByte(escaped)
			}
			continue
		}
		switch current {
		case '(':
			if !inClass && index+1 < len(pattern) && pattern[index+1] == '?' {
				if index+2 >= len(pattern) || pattern[index+2] != ':' {
					return "", false
				}
			}
			out.WriteByte(current)
		case '[':
			if inClass {
				return "", false
			}
			inClass = true
			out.WriteByte(current)
		case ']':
			if !inClass {
				return "", false
			}
			inClass = false
			out.WriteByte(current)
		case '.':
			if inClass {
				out.WriteByte(current)
			} else {
				// A single ECMAScript dot consumes one UTF-16 code unit while
				// Go/RE2 consumes one Unicode rune. Only retain the repeated
				// wildcard form used by the allowlisted XML capture idiom; its
				// capture spans the same source substring rather than exposing
				// an individual surrogate.
				if index+1 >= len(pattern) || pattern[index+1] != '*' {
					return "", false
				}
				out.WriteString(`[^\r\n\x{2028}\x{2029}]`)
			}
		case '^', '$':
			if !inClass {
				return "", false
			}
			out.WriteByte(current)
		default:
			out.WriteByte(current)
		}
	}
	if inClass {
		return "", false
	}
	return out.String(), true
}

func nativeASCIIHex(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

func nativeJSString(literal string) (string, bool) {
	literal = strings.TrimSpace(literal)
	if len(literal) < 2 {
		return "", false
	}
	quote := literal[0]
	if (quote != '\'' && quote != '"') || literal[len(literal)-1] != quote {
		return "", false
	}
	inner := literal[1 : len(literal)-1]
	var decoded strings.Builder
	decoded.Grow(len(inner))
	for index := 0; index < len(inner); index++ {
		if inner[index] != '\\' {
			decoded.WriteByte(inner[index])
			continue
		}
		if index+1 >= len(inner) {
			return "", false
		}
		index++
		if inner[index] != '\\' && inner[index] != quote {
			return "", false
		}
		decoded.WriteByte(inner[index])
	}
	return decoded.String(), true
}

func nativeSOAPHint(body string) (version, operation string) {
	switch {
	case strings.Contains(body, "http://schemas.xmlsoap.org/soap/envelope/"):
		version = "1.1"
	case strings.Contains(body, "http://www.w3.org/2003/05/soap-envelope"):
		version = "1.2"
	}
	if match := nativeSOAPBodyPattern.FindStringSubmatch(body); len(match) == 2 {
		operation = match[1]
	}
	return version, operation
}

func nativeSourceMappingRole(layerPath, requestPath string) string {
	if layerPath == requestPath {
		return "request"
	}
	switch strings.ToLower(path.Base(layerPath)) {
	case "opencollection.yml", "opencollection.yaml":
		return "collection"
	default:
		return "folder"
	}
}

func nativeDiagnostic(sourcePath, severity, code, message string, acknowledgement bool) NativeDiagnostic {
	hash := sha256.Sum256([]byte(sourcePath + "\x00" + code + "\x00" + message))
	id := "bruno-native-" + hex.EncodeToString(hash[:12])
	diagnostic := NativeDiagnostic{
		ID:                      id,
		Severity:                severity,
		Code:                    code,
		Path:                    sourcePath,
		Message:                 message,
		AcknowledgementRequired: acknowledgement,
	}
	if acknowledgement {
		diagnostic.FindingID = id
	}
	return diagnostic
}

func nativeHeaderNames(headers []nativeHeader) []string {
	names := make([]string, 0, len(headers))
	for _, header := range headers {
		names = append(names, header.Name)
	}
	return names
}

func nativeVariableNames(variables []nativeVariable) []string {
	names := make([]string, 0, len(variables))
	for _, variable := range variables {
		if variable.Disabled || strings.TrimSpace(variable.Name) == "" {
			continue
		}
		names = append(names, variable.Name)
	}
	return names
}

func nativeMapValue(parent map[string]any, key string) map[string]any {
	value := nativeLookup(parent, key)
	mapped, _ := value.(map[string]any)
	return mapped
}

func nativeLookup(parent map[string]any, key string) any {
	value, _ := nativeLookupPresent(parent, key)
	return value
}

func nativeLookupPresent(parent map[string]any, key string) (any, bool) {
	for candidate, value := range parent {
		if strings.EqualFold(strings.TrimSpace(candidate), key) {
			return value, true
		}
	}
	return nil, false
}

func nativeBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return false
	}
}

func nativeBoolPointer(value bool) *bool {
	copy := value
	return &copy
}

func nativeIntPointer(value int) *int {
	copy := value
	return &copy
}

func firstNonEmptyNative(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func sortedNativeSet[T ~string](values map[T]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, string(value))
	}
	sort.Strings(out)
	return out
}
