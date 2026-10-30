package importreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

// importTemplatePattern is the runtime's template token (templatePattern in
// pkg/engine/vuser.go): everything between {{ and }}.
var importTemplatePattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// importMaxNameVariants bounds the dotted tails tried for a parallel.* name. A
// name with more than this holds everything instead.
const importMaxNameVariants = 64

// importEveryVariable is the expression of a read that names no variable: a CEL
// condition that tests a whole namespace (size(vars), "x" in vars).
const importEveryVariable = "*"

// importEmptyFingerprint is the fingerprint of a component that nothing
// publishes and that reads nothing: an edge to it changes nothing.
const importEmptyFingerprint = "-"

// importProtocolVariableFamilies are the variables the engine and the protocol
// adapters publish under fixed names, as a variable-name prefix and the steps
// that publish them (an action type prefix, or the script protocols whose
// every step does). Their values come from a server's response, a connection
// or a step's arguments, so a step added or retargeted to reach a server the
// editor picks would redirect a request that interpolates one. The runtime
// publishes them from many call sites (http_async.go, websocket_callbacks.go,
// vuser_source_actions.go and the adapters' Result.Extractions), so the table
// works at the level of the family and pins every step in it.
var importProtocolVariableFamilies = []struct {
	variablePrefix string
	typePrefix     string
	protocols      []string
	// peers marks the families whose steps hand data to one another: a
	// producer writes what a consumer, or an echo server, reads back. A pinned
	// step of such a family holds the whole family.
	peers bool
}{
	{"async_", "http_async_", nil, false},
	{"ws_", "ws_", []string{"websocket"}, true},
	{"kafka_", "kafka_", []string{"kafka"}, true},
	{"jms_", "jms_", []string{"jms"}, true},
	{"soap_", "soap", []string{"soap"}, false},
	{"eventhubs_", "eventhubs_", []string{"eventhubs"}, true},
	{"java_http_", "java_http", []string{"java_http"}, false},
	{"java_vuser_", "java_vuser", []string{"java_vuser"}, false},
	// lr_exit stores its arguments, and the IP spoofing steps their state,
	// under these names (handleSourceRuntimeAction). Other names that begin
	// with two underscores, such as __VIEWSTATE, are ordinary correlation
	// names.
	{"__source_", "source_runtime", nil, false},
	{"__lr_ip_spoofing", "source_runtime", nil, false},
}

// importResponseMappingFamily is the family key of the steps whose responses a
// java_http response mapping publishes variables from.
const importResponseMappingFamily = "response_mapping"

// importTableStateTypes are the steps that share the run's live tables: a
// live_table step, a native_javascript step through its table functions and a
// Java Vuser through lr's table calls. A pinned one holds the others.
var importTableStateTypes = map[string]struct{}{
	"live_table": {}, "native_javascript": {}, "java_vuser_run": {},
}

// importControlActionKeys are the keys of a control block that hold nested
// steps. Everything else in the block (a loop's count, condition, for_each) is
// part of the block itself.
var importControlActionKeys = map[string]struct{}{
	"steps": {}, "else": {}, "cases": {}, "default": {}, "branches": {},
}

// importSelectionParameterFields are the fields of a list or file parameter
// that only choose among the values it has, or how a pool is shared out: they
// supply no value of their own. The exhaustion policy and the rows a pool
// starts from are held, because running out can make a parameter empty, and an
// empty host label is another host. For any other parameter every field
// decides what it yields (a counter's start, step, block and seed, a range, a
// padding, whether it advances), and a number can be a port, an address octet
// or a host label.
var importSelectionParameterFields = map[string]struct{}{
	"selection": {}, "auto_allocate": {}, "value_for_each_vuser": {}, "seed": {},
}

// importReferenceScope indexes the parts of a projected script that decide
// what a template reference in a request resolves to: its parameters, its data
// sources and the steps that write variables.
//
// The runtime interpolates a request's target, headers, body, auth and other
// wire fields before it sends them (resolveAction, pkg/engine/vuser_resolve.go).
// A {{param.*}} reads a script parameter, a {{var.*}} reads a runtime variable
// and falls back to a parameter of that name, and a {{data.*}} reads a data
// source column (a variable named <source>.<column>). A request that carries a
// kept credential is therefore redirected as surely by editing the parameter,
// data source or variable-writing step its target interpolates as by editing
// the target, and the editor cannot read the credential to notice.
//
// What a request resolves through is a graph: the request reads references,
// each reference is published by parameters, data sources, steps and
// extractors, and those read references in turn. The graph is condensed into
// strongly connected components and every component is fingerprinted from its
// content and the fingerprints of what it reads, so the cost of checking one
// request is the number of references it makes, however long the correlation
// chains behind them are.
type importReferenceScope struct {
	parameters    []map[string]any
	dataSources   []map[string]any
	paramsByName  map[string][]int
	sourcesByName map[string][]int
	// maxSourceName is the longest data source name, which bounds the
	// <source>. prefixes a variable name can carry.
	maxSourceName int
	protocol      string
	// defaultHeaders are the HTTP configuration's default headers, which the
	// runtime interpolates into every HTTP request it sends.
	defaultHeaders []any
	// responseMapping are the variables a java_http response mapping
	// publishes from every response.
	responseMapping map[string]struct{}
	nodes           map[string]map[string]any
	order           []string

	decoded        map[string]importDecodedStep
	asyncRequests  map[string]map[string]any
	defaultRefs    []string
	defaultScanned bool
	writersIndexed bool
	exactWriters   map[string][]importWriter
	slotWriters    map[string][]importWriter
	arrayWriters   map[string][]importWriter
	prefixWriters  map[string][]importWriter
	anyWriters     []importWriter
	// maxPatternName is the longest array or prefix writer name, which bounds
	// the name prefixes worth looking up.
	maxPatternName  int
	jsonDocuments   map[string][]string
	tableSteps      []string
	familyNodes     map[string][]string
	refs            map[string][]string
	valueRefs       map[string][]string
	accumulates     map[string]bool
	accumulatesDone bool
	reads           map[string]map[string]struct{}
	digests         map[string]string
	everyDigest     string
	graph           *importRefGraph
	memo            map[string]string
	everything      string
	everythingDone  bool
}

// importDecodedStep is a step decoded the way the engine decodes it.
type importDecodedStep struct {
	action dsl.Action
	ok     bool
}

// importWriter is a step, or one extractor of a step, that publishes
// variables.
type importWriter struct {
	path string
	// list names the extractor list ("extractors", or "request" for the
	// extractors on an async conversation's request) and index the extractor
	// in it. A writer that is the step itself has index -1.
	list  string
	index int
	name  string
	// array publishes name_1..name_n and name_count.
	array bool
	// prefix publishes name.<field>.
	prefix bool
	// any marks a step that may write any name: one whose outputs could not be
	// read, or whose output name is computed from a template.
	any bool
}

func newImportReferenceScope(script map[string]any) *importReferenceScope {
	scope := &importReferenceScope{
		parameters:      importMapEntries(script["parameters"]),
		dataSources:     importMapEntries(script["data_sources"]),
		paramsByName:    make(map[string][]int),
		sourcesByName:   make(map[string][]int),
		protocol:        strings.ToLower(strings.TrimSpace(StringJSONValue(script["protocol"]))),
		responseMapping: make(map[string]struct{}),
		nodes:           make(map[string]map[string]any),
		decoded:         make(map[string]importDecodedStep),
		asyncRequests:   make(map[string]map[string]any),
		jsonDocuments:   make(map[string][]string),
		familyNodes:     make(map[string][]string),
		refs:            make(map[string][]string),
		valueRefs:       make(map[string][]string),
		accumulates:     make(map[string]bool),
		reads:           make(map[string]map[string]struct{}),
		digests:         make(map[string]string),
		memo:            make(map[string]string),
	}
	for index, param := range scope.parameters {
		name := importCanonicalName(StringJSONValue(param["name"]))
		scope.paramsByName[name] = append(scope.paramsByName[name], index)
	}
	for index, source := range scope.dataSources {
		name := importCanonicalName(StringJSONValue(source["name"]))
		scope.sourcesByName[name] = append(scope.sourcesByName[name], index)
		if len(name) > scope.maxSourceName {
			scope.maxSourceName = len(name)
		}
	}
	config, _ := script["config"].(map[string]any)
	httpConfig, _ := config["http"].(map[string]any)
	scope.defaultHeaders = []any{httpConfig["default_headers"], httpConfig["default_header_list"]}
	javaHTTP, _ := config["java_http"].(map[string]any)
	mapping, _ := javaHTTP["response_mapping"].(map[string]any)
	for name := range mapping {
		scope.responseMapping[importCanonicalName(name)] = struct{}{}
	}
	walkImportActions(script, func(path string, action map[string]any) {
		if strings.Contains(path, "/params/requests/") {
			// A step nested in an HTTP sequence is a raw map the editor
			// spelled, and the engine decodes it case-insensitively.
			action = importCanonicalAction(action)
		}
		scope.nodes[path] = action
		scope.order = append(scope.order, path)
	})
	scope.graph = &importRefGraph{scope: scope, nodes: make(map[string]*importRefNode)}
	return scope
}

func importMapEntries(raw any) []map[string]any {
	items, _ := raw.([]any)
	entries := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if entry, _ := item.(map[string]any); entry != nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

// importCanonicalAction returns raw as the engine reads it: decoded into an
// action, so that "Extractors" is the extractors list as it is for the engine.
func importCanonicalAction(raw map[string]any) map[string]any {
	payload, err := json.Marshal(raw)
	if err != nil {
		return raw
	}
	var action dsl.Action
	if err := json.Unmarshal(payload, &action); err != nil {
		return raw
	}
	payload, err = json.Marshal(action)
	if err != nil {
		return raw
	}
	var canonical map[string]any
	if err := json.Unmarshal(payload, &canonical); err != nil {
		return raw
	}
	return canonical
}

// importCanonicalName is how the runtime keys a parameter name
// (ParameterRuntime.canonicalNames in pkg/engine/parameters.go).
func importCanonicalName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// importReferenceGuard compares what the requests of a candidate script resolve
// through with what the same requests resolved through in the prior script. The
// scopes read the executable script, not its disclosure projection: masking
// redacts a whole field it cannot read into (a base64 body, say), and the
// template inside is still filled in at run time. They only hash what they
// read, so no credential reaches a message.
type importReferenceGuard struct {
	candidate, prior           any
	projectedCandidate         map[string]any
	projectedPrior             map[string]any
	candidateScope, priorScope *importReferenceScope
	unreadable                 bool
}

func newImportReferenceGuard(candidate, projectedCandidate, prior, projectedPrior any) *importReferenceGuard {
	guard := &importReferenceGuard{candidate: candidate, prior: prior}
	guard.projectedCandidate, _ = projectedCandidate.(map[string]any)
	guard.projectedPrior, _ = projectedPrior.(map[string]any)
	return guard
}

func (g *importReferenceGuard) scopes() (*importReferenceScope, *importReferenceScope) {
	if g.candidateScope == nil {
		candidate, candidateOK := g.candidate.(map[string]any)
		prior, priorOK := g.prior.(map[string]any)
		if !candidateOK || !priorOK {
			// Without the executable script nothing can be compared: hold.
			g.unreadable = true
			candidate, prior = g.projectedCandidate, g.projectedPrior
		}
		g.candidateScope = newImportReferenceScope(candidate)
		g.priorScope = newImportReferenceScope(prior)
	}
	return g.candidateScope, g.priorScope
}

// requestChanged reports whether what the request at actionPath resolves
// through differs between the candidate and the prior script.
func (g *importReferenceGuard) requestChanged(actionPath string) bool {
	candidate, prior := g.scopes()
	return g.unreadable || candidate.dependencies(actionPath) != prior.dependencies(actionPath)
}

// scriptChanged reports whether what any request resolves through differs, for
// a credential kept at script level: it is sent with every request, and it can
// be copied into every value the runtime fills in.
func (g *importReferenceGuard) scriptChanged() bool {
	candidate, prior := g.scopes()
	return g.unreadable || candidate.allDependencies() != prior.allDependencies()
}

// dependencies returns a fingerprint of everything the request at actionPath
// resolves through: the parameters and data sources its wire fields
// interpolate (with the ones those read in turn), and the steps that write the
// variables they interpolate (with their wire fields and the extractors that
// publish them, and what those read). Anything the request does not reference
// is left out, so editing it changes nothing here.
func (s *importReferenceScope) dependencies(actionPath string) string {
	if cached, ok := s.memo[actionPath]; ok {
		return cached
	}
	roots := &importRootSet{keys: make(map[string]struct{})}
	s.addRequestRoots(roots, actionPath, false)
	fingerprint := s.fingerprint(roots)
	s.memo[actionPath] = fingerprint
	return fingerprint
}

// allDependencies fingerprints what every request reads, and every record
// header or message property a send step delivers.
func (s *importReferenceScope) allDependencies() string {
	if s.everythingDone {
		return s.everything
	}
	roots := &importRootSet{keys: make(map[string]struct{})}
	for _, path := range s.order {
		s.addRequestRoots(roots, path, true)
	}
	s.everything = s.fingerprint(roots)
	s.everythingDone = true
	return s.everything
}

// importRootSet collects the references a request starts from.
type importRootSet struct {
	keys map[string]struct{}
	all  bool
}

func (s *importReferenceScope) addRequestRoots(roots *importRootSet, path string, sentMetadata bool) {
	node := s.nodes[path]
	if node == nil {
		return
	}
	if importIsJavaVuser(node) {
		// resolveAction hands this step the VUser's current parameter and data
		// row values (withJavaVuserRuntimeParameters) whatever its fields name.
		roots.all = true
		roots.keys["java:"] = struct{}{}
	}
	if _, shared := importTableStateTypes[dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(StringJSONValue(node["type"]))))]; shared {
		// A Java Vuser step keeps one JVM across its init, action and end
		// steps, and the table steps share the run's live tables.
		roots.keys["tables:"] = struct{}{}
	}
	for _, expr := range s.expressions(path) {
		roots.addExpression(expr)
	}
	if s.readsLoopIndex(path) {
		// The loops around the request decide the numbers it reads.
		roots.keys["loops:"+path] = struct{}{}
	}
	for _, expr := range s.defaultHeaderExpressions() {
		roots.addExpression(expr)
	}
	if sentMetadata {
		for _, expr := range s.sentMetadataExpressions(path) {
			roots.addExpression(expr)
		}
	}
}

func (r *importRootSet) addExpression(expr string) {
	if key := importReferenceKey(expr); key != "" {
		r.keys[key] = struct{}{}
	}
}

func importIsJavaVuser(node map[string]any) bool {
	return dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(StringJSONValue(node["type"])))) == "java_vuser_run"
}

// importReferenceKey maps one template expression to the node of the
// reference graph it reads, as resolveTemplateValue
// (pkg/engine/vuser_resolve.go) resolves it. env, secret, fn, fake and run
// expressions read nothing a script edit can change.
func importReferenceKey(expr string) string {
	var kind, rest string
	switch {
	case expr == importEveryVariable:
		return "everything:"
	case strings.HasPrefix(expr, "param."):
		kind, rest = "param", strings.TrimPrefix(expr, "param.")
	case strings.HasPrefix(expr, "var."):
		kind, rest = "var", strings.TrimPrefix(expr, "var.")
	case strings.HasPrefix(expr, "data."):
		kind, rest = "bare", strings.TrimPrefix(expr, "data.")
	case strings.HasPrefix(expr, "file."):
		kind, rest = "source", strings.TrimPrefix(expr, "file.")
	default:
		return ""
	}
	name := importCanonicalName(rest)
	if name == "" {
		return ""
	}
	return kind + ":" + name
}

// fingerprint combines the fingerprints of the graph nodes the roots start
// from.
func (s *importReferenceScope) fingerprint(roots *importRootSet) string {
	parts := make(map[string]struct{}, len(roots.keys)+1)
	for key := range roots.keys {
		if fingerprint := s.graph.visit(key).fingerprint; fingerprint != importEmptyFingerprint {
			parts[fingerprint] = struct{}{}
		}
	}
	if roots.all {
		parts["all:"+s.allDigest()] = struct{}{}
	}
	return importHash(sortedImportKeys(parts)...)
}

func sortedImportKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func importHash(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = io.WriteString(hash, strconv.Itoa(len(part)))
		_, _ = hash.Write([]byte{':'})
		_, _ = io.WriteString(hash, part)
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

// allDigest is the digest of every parameter and data source, which a step
// that reads them all holds.
func (s *importReferenceScope) allDigest() string {
	if s.everyDigest == "" {
		parts := make(map[string]struct{}, len(s.parameters)+len(s.dataSources))
		for index := range s.parameters {
			parts["p:"+s.parameterDigest(index)] = struct{}{}
		}
		for index := range s.dataSources {
			parts["s:"+s.sourceDigest(index)] = struct{}{}
		}
		s.everyDigest = importHash(sortedImportKeys(parts)...)
	}
	return s.everyDigest
}

// digest returns a stable content digest of value, remembered under key.
func (s *importReferenceScope) digest(key string, value any) string {
	if cached, ok := s.digests[key]; ok {
		return cached
	}
	payload, err := json.Marshal(value)
	if err != nil {
		payload = []byte(fmt.Sprintf("%#v", value))
	}
	digest := importHash(string(payload))
	s.digests[key] = digest
	return digest
}

// parameterDigest digests a parameter without the fields that only choose
// among its values (importSelectionParameterFields).
func (s *importReferenceScope) parameterDigest(index int) string {
	key := fmt.Sprintf("p:%d", index)
	if cached, ok := s.digests[key]; ok {
		return cached
	}
	held := make(map[string]any, len(s.parameters[index]))
	chooses := false
	switch dsl.CanonicalParameterType(StringJSONValue(s.parameters[index]["type"])) {
	case "list", "file":
		chooses = true
	}
	for field, value := range s.parameters[index] {
		if _, selection := importSelectionParameterFields[field]; !chooses || !selection {
			held[field] = value
		}
	}
	return s.digest(key, held)
}

// sourceDigest digests a data source without its selection strategy, and
// hashes its inline rows directly rather than copying them into another
// encoding.
func (s *importReferenceScope) sourceDigest(index int) string {
	key := fmt.Sprintf("s:%d", index)
	if cached, ok := s.digests[key]; ok {
		return cached
	}
	held := make(map[string]any, len(s.dataSources[index]))
	for field, value := range s.dataSources[index] {
		if field != "strategy" && field != "inline_data" {
			held[field] = value
		}
	}
	payload, _ := json.Marshal(held)
	digest := importHash(string(payload), StringJSONValue(s.dataSources[index]["inline_data"]))
	s.digests[key] = digest
	return digest
}

// valueExpressions returns the template expressions inside the values of a
// parameter or the rows of a data source. The runtime does not expand them when
// it reads the value, but a source step that saves the value expands it again
// (setSourceVariable interpolates), so a template stored in one is read.
func (s *importReferenceScope) valueExpressions(key string, held any) []string {
	if cached, ok := s.valueRefs[key]; ok {
		return cached
	}
	var out []string
	collectImportTemplates(held, &out)
	s.valueRefs[key] = out
	return out
}

// step decodes the step at path the way the engine does, without its nested
// steps.
func (s *importReferenceScope) step(path string) importDecodedStep {
	if cached, ok := s.decoded[path]; ok {
		return cached
	}
	node := s.nodes[path]
	shell := make(map[string]any, len(node))
	for key, value := range node {
		if key != "control" {
			shell[key] = value
		}
	}
	var step importDecodedStep
	payload, err := json.Marshal(shell)
	if err == nil {
		err = json.Unmarshal(payload, &step.action)
	}
	step.ok = err == nil
	s.decoded[path] = step
	return step
}

// asyncRequest returns the request an http_async_start step sends, as the
// engine reads it (asyncRequestAction): params.request decoded into an action.
func (s *importReferenceScope) asyncRequest(path string) map[string]any {
	if cached, ok := s.asyncRequests[path]; ok {
		return cached
	}
	var request map[string]any
	if params, _ := s.nodes[path]["params"].(map[string]any); params != nil {
		if raw, _ := params["request"].(map[string]any); raw != nil {
			request = importCanonicalAction(raw)
		}
	}
	s.asyncRequests[path] = request
	return request
}

// extractorList returns an extractor list of a step: its own, or the one on an
// async conversation's request, which publishes when the conversation's
// responses are applied.
func (s *importReferenceScope) extractorList(path, list string) []any {
	if list == "request" {
		extractors, _ := s.asyncRequest(path)["extractors"].([]any)
		return extractors
	}
	extractors, _ := s.nodes[path]["extractors"].([]any)
	return extractors
}

func (s *importReferenceScope) indexWriters() {
	if s.writersIndexed {
		return
	}
	s.writersIndexed = true
	s.exactWriters = make(map[string][]importWriter)
	s.slotWriters = make(map[string][]importWriter)
	s.arrayWriters = make(map[string][]importWriter)
	s.prefixWriters = make(map[string][]importWriter)
	add := func(writer importWriter) {
		if strings.Contains(writer.name, "{{") {
			// The name is computed when the step runs (the runtime
			// interpolates an OAuth step's save_as, for one).
			writer.any = true
		}
		key := strings.ToLower(writer.name)
		switch {
		case writer.any:
			s.anyWriters = append(s.anyWriters, writer)
		case writer.array:
			s.arrayWriters[key] = append(s.arrayWriters[key], writer)
			s.maxPatternName = max(s.maxPatternName, len(key))
		case writer.prefix:
			s.prefixWriters[key] = append(s.prefixWriters[key], writer)
			s.maxPatternName = max(s.maxPatternName, len(key))
		default:
			s.exactWriters[key] = append(s.exactWriters[key], writer)
			// name_3 and name_count are slots of the array called name.
			if cut := strings.LastIndexByte(key, '_'); cut > 0 && importArraySuffix(key[cut+1:]) {
				s.slotWriters[key[:cut]] = append(s.slotWriters[key[:cut]], writer)
			}
		}
	}
	for _, path := range s.order {
		node := s.nodes[path]
		for _, list := range []string{"extractors", "request"} {
			for index, raw := range s.extractorList(path, list) {
				extractor, _ := raw.(map[string]any)
				name := strings.TrimSpace(StringJSONValue(extractor["name"]))
				if name == "" {
					continue
				}
				mode := strings.TrimSpace(StringJSONValue(extractor["mode"]))
				add(importWriter{path: path, list: list, index: index, name: name, array: strings.EqualFold(mode, "all")})
			}
		}
		if forEach := importForEach(node); forEach != nil {
			// bindForEachRow publishes every column as var.<var>.<column>.
			if name := strings.TrimSpace(StringJSONValue(forEach["var"])); name != "" {
				add(importWriter{path: path, index: -1, name: name, prefix: true})
			}
		}

		step := s.step(path)
		if !step.ok {
			add(importWriter{path: path, index: -1, any: true})
			continue
		}
		action := step.action
		canonicalType := dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type)))
		if canonicalType == "source_json" {
			// A lr_json_get_values or lr_json_stringify step reads the
			// document an lr_eval_json step stored under the same JsonObject,
			// and a later lr_eval_json under that name replaces it. The readers
			// depend on the document; only the steps that make it are its
			// content.
			switch strings.ToLower(importSourceFunction(action)) {
			case "lr_json_get_values", "lr_json_stringify":
			default:
				for _, document := range importJSONDocumentNames(action) {
					s.jsonDocuments[document] = append(s.jsonDocuments[document], path)
				}
			}
		}
		if _, shared := importTableStateTypes[canonicalType]; shared {
			s.tableSteps = append(s.tableSteps, path)
		}
		outputs := varflow.StepOutputs(action)
		for _, write := range outputs.Writes {
			if name := strings.TrimSpace(write.Name); name != "" {
				add(importWriter{path: path, index: -1, name: name, array: write.Array})
			}
		}
		for _, prefix := range outputs.Prefixes {
			if prefix = strings.TrimSpace(prefix); prefix != "" {
				add(importWriter{path: path, index: -1, name: prefix, prefix: true})
			}
		}
		// The model above lists what a step makes available. A request is
		// redirected as well by a step that sets a name to a value, or deletes
		// it so a parameter of that name answers.
		for _, name := range outputs.Removes {
			if name = strings.TrimSpace(name); name != "" {
				add(importWriter{path: path, index: -1, name: name})
			}
		}
		if name := importConvertedDateParameter(action); name != "" {
			add(importWriter{path: path, index: -1, name: name})
		}
	}
}

// importSourceFunction is sourceActionFunction in
// pkg/engine/vuser_source_actions.go: the LoadRunner function a source step
// runs.
func importSourceFunction(action dsl.Action) string {
	if value, ok := action.Params["function"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if value, ok := action.Metadata["source_function"].(string); ok {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(action.Type)
}

// importSourceArgs is sourceActionArgs: the step's args as trimmed text.
func importSourceArgs(action dsl.Action) []string {
	raw, ok := action.Params["args"]
	if !ok || raw == nil {
		return nil
	}
	var values []any
	switch typed := raw.(type) {
	case []any:
		values = typed
	case []string:
		for _, value := range typed {
			values = append(values, value)
		}
	default:
		values = []any{typed}
	}
	args := make([]string, 0, len(values))
	for _, value := range values {
		args = append(args, strings.TrimSpace(fmt.Sprint(value)))
	}
	return args
}

// importJSONDocumentNames returns every document name an lr_eval_json,
// lr_json_get_values or lr_json_stringify step mentions
// (sourceJSONParams in pkg/engine/vuser_source_json.go reads params.json_object
// or, for older imports, JsonObject= in args), lowercased and trimmed. A step
// is linked under all of them.
func importJSONDocumentNames(action dsl.Action) []string {
	var names []string
	if name, ok := action.Params["json_object"].(string); ok && importCanonicalName(name) != "" {
		names = append(names, importCanonicalName(name))
	}
	for _, arg := range importSourceArgs(action) {
		key, value, found := strings.Cut(arg, "=")
		if found && strings.EqualFold(strings.TrimSpace(key), "JsonObject") && importCanonicalName(value) != "" {
			names = append(names, importCanonicalName(value))
		}
	}
	return names
}

// importBareReads returns the variables a source step reads by name rather
// than by template, and that it does not also write: web_save_param_length
// measures the variable it names and saves <name>_Length. The other functions
// of handleSourceParameterAction that take a variable name (lr_param_increment,
// web_convert_date_param, lr_free_parameter) write the name they read, so the
// writers of that name are already held. The second result is the array a saved
// array entry is picked from (sourceParameterArrayValue reads name_count and
// name_<n>).
func importBareReads(action dsl.Action) (bare, arrays []string) {
	if dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type))) != "source_parameter" {
		return nil, nil
	}
	if strings.EqualFold(importSourceFunction(action), "web_save_param_length") {
		if args := importSourceArgs(action); len(args) > 0 && args[0] != "" {
			bare = append(bare, args[0])
		}
	}
	if array, ok := action.Params["source_array"].(map[string]any); ok {
		if name := StringJSONValue(array["name"]); importCanonicalName(name) != "" {
			arrays = append(arrays, name)
		}
	}
	return bare, arrays
}

// importConvertedDateParameter returns the variable a web_convert_date_param
// step rewrites. varflow lists no output for it, because it adds no name to
// the set of available ones, but handleWebConvertDateParamAction sets
// args[0] to the formatted target format, a literal when the format has no
// specifier.
func importConvertedDateParameter(action dsl.Action) string {
	if dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type))) != "source_parameter" ||
		!strings.EqualFold(importSourceFunction(action), "web_convert_date_param") {
		return ""
	}
	if args := importSourceArgs(action); len(args) > 0 {
		return args[0]
	}
	return ""
}

// importForEach returns the for_each block of a data-driven loop step.
func importForEach(node map[string]any) map[string]any {
	control, _ := node["control"].(map[string]any)
	forEach, _ := control["for_each"].(map[string]any)
	return forEach
}

// importControlBlock returns a control step's own block: everything but its
// nested steps.
func importControlBlock(node map[string]any) map[string]any {
	control, _ := node["control"].(map[string]any)
	block := make(map[string]any, len(control))
	for field, child := range control {
		if _, nested := importControlActionKeys[field]; !nested {
			block[field] = child
		}
	}
	return block
}

// importArraySuffix reports whether rest is what follows name_ in a name the
// runtime publishes for an array: an index or the count.
func importArraySuffix(rest string) bool {
	if rest == "count" {
		return true
	}
	if rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// namedWriters returns the steps and extractors that publish the variable
// name under a name of their own: an exact name, an array's slots, or a prefix.
// Writers that may publish any name are importReferenceScope.anyWriters.
func (s *importReferenceScope) namedWriters(name string) []importWriter {
	s.indexWriters()
	var matched []importWriter
	for _, variant := range importVariableNameVariants(name) {
		lower := strings.ToLower(variant)
		matched = append(matched, s.exactWriters[lower]...)
		// An array called name publishes name_<n> and name_count.
		matched = append(matched, s.arrayWriters[lower]...)
		matched = append(matched, s.prefixWriters[lower]...)
		for offset := 0; offset < len(lower) && offset <= s.maxPatternName; offset++ {
			switch lower[offset] {
			case '_':
				if importArraySuffix(lower[offset+1:]) {
					matched = append(matched, s.arrayWriters[lower[:offset]]...)
				}
			case '.':
				matched = append(matched, s.prefixWriters[lower[:offset]]...)
			}
		}
	}
	return matched
}

// arrayWriterSet returns the steps and extractors that publish the slots of the
// array called name: its elements and its count, however they are written.
func (s *importReferenceScope) arrayWriterSet(name string) []importWriter {
	matched := s.namedWriters(name)
	return append(matched, s.slotWriters[strings.ToLower(name)]...)
}

// familySteps returns the paths of the steps that publish the variables of one
// protocol family (importProtocolVariableFamilies).
func (s *importReferenceScope) familySteps(typePrefix string, protocols []string) []string {
	if cached, ok := s.familyNodes[typePrefix]; ok {
		return cached
	}
	whole := false
	for _, protocol := range protocols {
		if s.protocol == protocol {
			whole = true
		}
	}
	paths := make([]string, 0)
	for _, path := range s.order {
		actionType := dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(StringJSONValue(s.nodes[path]["type"]))))
		member := strings.HasPrefix(actionType, typePrefix)
		if whole && !member {
			// Every request of a script in the family's protocol is a member,
			// but the engine's own steps (a message, a transaction, a timer) are
			// not requests of it.
			spec, known := dsl.SpecForActionType(actionType)
			member = !known || !spec.EngineAction()
		}
		if member {
			paths = append(paths, path)
		}
	}
	s.familyNodes[typePrefix] = paths
	return paths
}

// importVariableNameVariants are the names a reference may read a variable
// under: the name itself and, for a value a parallel branch wrote, the name a
// step inside the branch saved it under. The runtime publishes it as
// parallel.<branch>.<name>, and both a branch name and a nested branch's own
// prefix can contain dots, so every shorter dotted tail is a candidate. The
// second result reports a name with more tails than are tried.
func importVariableNameVariants(name string) []string {
	variants, _ := importVariableNameVariantsBounded(name)
	return variants
}

func importVariableNameVariantsBounded(name string) ([]string, bool) {
	variants := []string{name}
	const parallel = "parallel."
	if len(name) <= len(parallel) || !strings.EqualFold(name[:len(parallel)], parallel) {
		return variants, false
	}
	for rest := name[len(parallel):]; ; {
		_, tail, found := strings.Cut(rest, ".")
		if !found || tail == "" {
			return variants, false
		}
		if len(variants) >= importMaxNameVariants {
			return variants, true
		}
		variants = append(variants, tail)
		rest = tail
	}
}

// expressions returns the template expressions the wire fields of the step at
// path interpolate.
func (s *importReferenceScope) expressions(path string) []string {
	if cached, ok := s.refs[path]; ok {
		return cached
	}
	node := s.nodes[path]
	var out []string
	if node != nil {
		// The wire context, as the action comparison defines it. Metadata is
		// left out as there: a Kafka or JMS send delivers it as record headers
		// or message properties, which do not pick where a header carrying a
		// kept credential goes (the topic or queue is the target).
		collectImportTemplates(ImportActionWireContext(node), &out)
		body := StringJSONValue(node["body"])
		// body_encoding describes how the body is stored; the runtime decodes
		// it before it interpolates (interpolateActionBody).
		if decoded, encoded, err := dsl.DecodeBodyEncoding(body, StringJSONValue(node["body_encoding"]), 0); encoded && err == nil {
			collectImportTemplates(string(decoded), &out)
		}
		// A form-encoded structured body (a Salesforce Aura message) is read
		// per decoded layer, so a template inside it is percent-encoded in
		// the stored text.
		if strings.Contains(body, "%") {
			if unescaped, err := url.QueryUnescape(body); err == nil {
				collectImportTemplates(unescaped, &out)
			}
		}
		metadata, _ := node["metadata"].(map[string]any)
		if policy, _ := metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey].(string); strings.TrimSpace(policy) == dsl.ImportAuraJSONTemplateValueEncoding {
			_, _ = aurapayload.VisitTemplatesContextualWithWorkAccounting(
				body,
				StringJSONValue(node["body_encoding"]),
				metadata[dsl.ImportAuraTemplateContractMetadataKey],
				func(value string, _ aurapayload.TemplateContext) error {
					collectImportTemplates(value, &out)
					return nil
				},
				aurapayload.MaxWorkBytes,
			)
		}
	}
	s.refs[path] = out
	return out
}

// sentMetadataExpressions returns the template expressions in the metadata
// entries a Kafka or JMS send delivers as record headers or message
// properties.
func (s *importReferenceScope) sentMetadataExpressions(path string) []string {
	node := s.nodes[path]
	metadata, _ := node["metadata"].(map[string]any)
	var out []string
	for key, value := range metadata {
		probe := dsl.Action{Type: StringJSONValue(node["type"]), Metadata: map[string]any{key: value}}
		if sent, _ := varflow.MetadataReads(probe, key); sent {
			collectImportTemplates(value, &out)
		}
	}
	return out
}

// defaultHeaderExpressions are the template expressions of the HTTP default
// headers.
func (s *importReferenceScope) defaultHeaderExpressions() []string {
	if !s.defaultScanned {
		s.defaultScanned = true
		collectImportTemplates(s.defaultHeaders, &s.defaultRefs)
	}
	return s.defaultRefs
}

// collectImportTemplates appends the trimmed expression of every template in
// the strings of value, its map keys included.
func collectImportTemplates(value any, out *[]string) {
	switch typed := value.(type) {
	case string:
		if !strings.Contains(typed, "{{") {
			return
		}
		for _, match := range importTemplatePattern.FindAllStringSubmatch(typed, -1) {
			*out = append(*out, strings.TrimSpace(match[1]))
		}
	case map[string]any:
		for key, child := range typed {
			collectImportTemplates(key, out)
			collectImportTemplates(child, out)
		}
	case []any:
		for _, child := range typed {
			collectImportTemplates(child, out)
		}
	}
}

// collectImportControlReferences appends the references in the block of a
// control step itself, leaving its nested steps: the templates in a loop's
// condition and counts, and the variables a CEL expr reads.
func collectImportControlReferences(control map[string]any, out *[]string) {
	for key, value := range control {
		if _, nested := importControlActionKeys[key]; nested {
			continue
		}
		collectImportTemplates(value, out)
		collectImportCELReferences(key, value, out)
	}
}

func collectImportCELReferences(key string, value any, out *[]string) {
	switch typed := value.(type) {
	case string:
		if key == "expr" {
			*out = append(*out, importCELReads(typed)...)
		}
	case map[string]any:
		for child, nested := range typed {
			collectImportCELReferences(child, nested, out)
		}
	case []any:
		for _, nested := range typed {
			collectImportCELReferences(key, nested, out)
		}
	}
}

// importCELReads returns the template-style expressions (var.name,
// data.source.column) of the variables a CEL condition reads, and
// importEveryVariable for a use of a namespace as a whole. The runtime exposes
// the variables as vars (spelled var in the DSL) and the data rows as data,
// both maps that nest a dotted name by its first dot, so vars.a.b, vars["a"]["b"]
// and vars.a["b"] all read the variable "a.b". A namespace that is not followed
// by a name, or is followed by a map method (vars.size(), vars.exists(...)) or
// by a computed key, reads any of its entries. Text inside quotes is not read.
func importCELReads(expr string) []string {
	var reads []string
	isWordByte := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	skipSpace := func(i int) int {
		for i < len(expr) && (expr[i] == ' ' || expr[i] == '\t' || expr[i] == '\n' || expr[i] == '\r') {
			i++
		}
		return i
	}
	for i := 0; i < len(expr); {
		switch ch := expr[i]; {
		case ch == '"' || ch == '\'':
			// Skip a quoted literal, honouring backslash escapes.
			for i++; i < len(expr) && expr[i] != ch; i++ {
				if expr[i] == '\\' {
					i++
				}
			}
			i++
		case isWordByte(ch):
			start := i
			for i < len(expr) && isWordByte(expr[i]) {
				i++
			}
			word := expr[start:i]
			// A word after a dot is a member of something else (foo.vars).
			member := strings.HasSuffix(strings.TrimRight(expr[:start], " \t\n\r"), ".")
			if member || (word != "var" && word != "vars" && word != "data") {
				continue
			}
			namespace := "var"
			if word == "data" {
				namespace = "data"
			}
			var chain []string
			every := false
			for {
				j := skipSpace(i)
				if j < len(expr) && expr[j] == '.' {
					j = skipSpace(j + 1)
					nameStart := j
					for j < len(expr) && isWordByte(expr[j]) {
						j++
					}
					if j == nameStart {
						every = true
						break
					}
					name := expr[nameStart:j]
					if k := skipSpace(j); k < len(expr) && expr[k] == '(' {
						// A method call: on the namespace it reads every entry,
						// on an entry it reads that entry.
						if len(chain) == 0 {
							every = true
						}
						i = j
						break
					}
					chain = append(chain, name)
					i = j
					continue
				}
				if j < len(expr) && expr[j] == '[' {
					j = skipSpace(j + 1)
					if j >= len(expr) || (expr[j] != '"' && expr[j] != '\'') {
						every = true
						break
					}
					quote := expr[j]
					j++
					keyStart := j
					for j < len(expr) && expr[j] != quote {
						if expr[j] == '\\' {
							j++
						}
						j++
					}
					if j >= len(expr) {
						every = true
						break
					}
					chain = append(chain, expr[keyStart:j])
					j = skipSpace(j + 1)
					if j >= len(expr) || expr[j] != ']' {
						every = true
						break
					}
					i = j + 1
					continue
				}
				break
			}
			if every || len(chain) == 0 {
				reads = append(reads, importEveryVariable)
			}
			if len(chain) > 0 {
				reads = append(reads, namespace+"."+strings.Join(chain, "."))
			}
		default:
			i++
		}
	}
	return reads
}

// importRefNode is one reference of the graph: the content that publishes it
// and the references that content reads.
type importRefNode struct {
	// digests are the parameters and data sources that publish it.
	digests []string
	// steps are the paths of the steps, and extractors the "path|list|index"
	// keys of the extractors, that publish it. A component holds a step as many
	// times as there are copies of it.
	steps      []string
	extractors []string
	succ       []string
	all        bool

	index, low  int
	onStack     bool
	fingerprint string
}

// importRefGraph is the graph of references, built on demand and condensed into
// strongly connected components with Tarjan's algorithm.
type importRefGraph struct {
	scope   *importReferenceScope
	nodes   map[string]*importRefNode
	counter int
	stack   []*importRefNode
}

// visit returns the node for key with its fingerprint computed. A component's
// fingerprint covers its content and the fingerprints of the components it
// reads, so it changes exactly when something it can reach changes.
func (g *importRefGraph) visit(key string) *importRefNode {
	if node, ok := g.nodes[key]; ok {
		return node
	}
	node := g.scope.buildRefNode(key)
	node.index, node.low = g.counter, g.counter
	g.counter++
	g.nodes[key] = node
	g.stack = append(g.stack, node)
	node.onStack = true
	for _, successor := range node.succ {
		if next, seen := g.nodes[successor]; !seen {
			if child := g.visit(successor); child.low < node.low {
				node.low = child.low
			}
		} else if next.onStack && next.index < node.low {
			node.low = next.index
		}
	}
	if node.low != node.index {
		return node
	}
	var members []*importRefNode
	for {
		member := g.stack[len(g.stack)-1]
		g.stack = g.stack[:len(g.stack)-1]
		member.onStack = false
		members = append(members, member)
		if member == node {
			break
		}
	}
	node.fingerprint = g.scope.componentFingerprint(g, members)
	for _, member := range members {
		member.fingerprint = node.fingerprint
	}
	return node
}

// componentFingerprint fingerprints a strongly connected component from the
// content of its members, with the multiplicity of the steps and extractors,
// and from the fingerprints of the components it reads.
func (s *importReferenceScope) componentFingerprint(g *importRefGraph, members []*importRefNode) string {
	content := make(map[string]struct{})
	steps := make(map[string]struct{})
	extractors := make(map[string]struct{})
	reads := make(map[string]struct{})
	all := false
	for _, member := range members {
		for _, digest := range member.digests {
			content[digest] = struct{}{}
		}
		for _, path := range member.steps {
			steps[path] = struct{}{}
		}
		for _, key := range member.extractors {
			extractors[key] = struct{}{}
		}
		all = all || member.all
		for _, successor := range member.succ {
			// A member of this component has no fingerprint yet, and a
			// component nothing publishes adds nothing.
			if next := g.nodes[successor]; next.fingerprint != "" && next.fingerprint != importEmptyFingerprint {
				reads[next.fingerprint] = struct{}{}
			}
		}
	}
	counts := make(map[string]int)
	for path := range steps {
		digest := "n:" + s.stepDigest(path)
		if s.accumulating(path) {
			counts[digest]++
		} else {
			// Another copy of a step that yields the same value each time adds
			// nothing.
			counts[digest] = 1
		}
	}
	for key := range extractors {
		digest := "x:" + s.extractorDigest(key)
		if s.accumulating(strings.SplitN(key, "|", 2)[0]) {
			counts[digest]++
		} else {
			counts[digest] = 1
		}
	}
	for digest, count := range counts {
		content["held:"+digest+"*"+strconv.Itoa(count)] = struct{}{}
	}
	parts := sortedImportKeys(content)
	if all {
		parts = append(parts, "all:"+s.allDigest())
	}
	if len(parts) == 0 && len(reads) == 0 {
		return importEmptyFingerprint
	}
	return importHash(append(append(parts, "reads"), sortedImportKeys(reads)...)...)
}

// importRefBuilder gathers the content and successors of one graph node.
type importRefBuilder struct {
	scope      *importReferenceScope
	digests    map[string]struct{}
	succ       map[string]struct{}
	steps      map[string]struct{}
	extractors map[string]struct{}
	all        bool
}

func (s *importReferenceScope) buildRefNode(key string) *importRefNode {
	b := &importRefBuilder{
		scope:      s,
		digests:    make(map[string]struct{}),
		succ:       make(map[string]struct{}),
		steps:      make(map[string]struct{}),
		extractors: make(map[string]struct{}),
	}
	kind, name, _ := strings.Cut(key, ":")
	switch kind {
	case "param":
		b.parameter(name)
	case "source":
		b.source(name)
	case "var":
		// A variable that nothing sets answers from the parameter of that name.
		b.succ["param:"+name] = struct{}{}
		b.variable(name)
	case "bare":
		b.variable(name)
	case "array":
		b.array(name)
	case "family":
		b.family(name)
	case "any":
		for _, writer := range s.anyWriters {
			b.pinWriter(writer)
		}
	case "json":
		s.indexWriters()
		for _, path := range s.jsonDocuments[name] {
			b.pinStep(path)
		}
	case "tables":
		s.indexWriters()
		for _, path := range s.tableSteps {
			b.pinStep(path)
		}
	case "java":
		b.javaVariables()
	case "writers":
		b.everyWriter()
	case "everything":
		// A condition that reads a namespace as a whole can see every variable,
		// parameter and data row.
		b.everyWriter()
		b.all = true
		b.succ["tables:"] = struct{}{}
		b.succ["family:"+importResponseMappingFamily] = struct{}{}
		for index := range importProtocolVariableFamilies {
			b.succ["family:"+strconv.Itoa(index)] = struct{}{}
		}
	case "loops":
		b.loops(name)
	}
	return &importRefNode{
		digests:    sortedImportKeys(b.digests),
		steps:      sortedImportKeys(b.steps),
		extractors: sortedImportKeys(b.extractors),
		succ:       sortedImportKeys(b.succ),
		all:        b.all,
	}
}

// javaVariables holds the variables a Java Vuser step receives besides the
// parameters and data rows: the values steps saved under a data source's name
// (<source>.<column>), which the runner passes to the JVM as well.
func (b *importRefBuilder) javaVariables() {
	scope := b.scope
	scope.indexWriters()
	for source := range scope.sourcesByName {
		if source == "" {
			continue
		}
		for _, writers := range []map[string][]importWriter{scope.exactWriters, scope.arrayWriters, scope.prefixWriters} {
			for name, named := range writers {
				if name == source || strings.HasPrefix(name, source+".") {
					for _, writer := range named {
						b.pinWriter(writer)
					}
				}
			}
		}
	}
	if len(scope.anyWriters) > 0 {
		b.succ["any:"] = struct{}{}
	}
}

// everyWriter holds every step and extractor that publishes a variable. A name
// the guard cannot resolve could be published by any of them.
func (b *importRefBuilder) everyWriter() {
	scope := b.scope
	scope.indexWriters()
	for _, writers := range []map[string][]importWriter{scope.exactWriters, scope.arrayWriters, scope.prefixWriters} {
		for _, named := range writers {
			for _, writer := range named {
				b.pinWriter(writer)
			}
		}
	}
	for _, writer := range scope.anyWriters {
		b.pinWriter(writer)
	}
}

// loops holds the loops around the step at path: their blocks, and what their
// counts and conditions read.
func (b *importRefBuilder) loops(path string) {
	scope := b.scope
	for _, loop := range scope.enclosingLoopPaths(path) {
		node := scope.nodes[loop]
		block := importControlBlock(node)
		b.digests["loop:"+scope.digest("loop:"+loop, block)] = struct{}{}
		var control []string
		collectImportControlReferences(block, &control)
		b.expressions(control)
		if forEach := importForEach(node); forEach != nil {
			if source := importCanonicalName(StringJSONValue(forEach["source"])); source != "" {
				b.succ["source:"+source] = struct{}{}
			}
		}
	}
}

// parameter holds the script parameters named name and what they read: the
// parameter they follow row for row, the data source they take values from,
// and the templates stored in their values.
func (b *importRefBuilder) parameter(name string) {
	scope := b.scope
	for _, index := range scope.paramsByName[name] {
		b.digests["p:"+scope.parameterDigest(index)] = struct{}{}
		param := scope.parameters[index]
		if follows := importCanonicalName(StringJSONValue(param["same_line_as"])); follows != "" {
			b.succ["param:"+follows] = struct{}{}
		}
		if source := importCanonicalName(StringJSONValue(param["data_source"])); source != "" {
			b.succ["source:"+source] = struct{}{}
		}
		b.expressions(scope.valueExpressions("p:"+strconv.Itoa(index), param["values"]))
	}
}

func (b *importRefBuilder) source(name string) {
	scope := b.scope
	for _, index := range scope.sourcesByName[name] {
		b.digests["s:"+scope.sourceDigest(index)] = struct{}{}
		b.expressions(scope.valueExpressions("s:"+strconv.Itoa(index), scope.dataSources[index]["inline_data"]))
	}
}

// variable holds what can publish the runtime variable name: the data source
// whose column it names (<source>.<column>), every step or extractor that
// writes it, and the steps of a protocol family whose fixed names it carries.
func (b *importRefBuilder) variable(name string) {
	if name == "" {
		return
	}
	scope := b.scope
	variants, truncated := importVariableNameVariantsBounded(name)
	if truncated {
		// More tails than are tried: hold everything rather than guess.
		b.all = true
		b.succ["writers:"] = struct{}{}
	}
	for _, variant := range variants {
		lower := strings.ToLower(variant)
		if len(scope.sourcesByName) > 0 {
			for offset := 0; offset < len(lower) && offset <= scope.maxSourceName+1; offset++ {
				if lower[offset] != '.' {
					continue
				}
				if source := importCanonicalName(lower[:offset]); len(scope.sourcesByName[source]) > 0 {
					b.succ["source:"+source] = struct{}{}
				}
			}
		}
		if _, mapped := scope.responseMapping[importCanonicalName(variant)]; mapped {
			b.succ["family:"+importResponseMappingFamily] = struct{}{}
		}
		for index, family := range importProtocolVariableFamilies {
			if strings.HasPrefix(lower, family.variablePrefix) {
				b.succ["family:"+strconv.Itoa(index)] = struct{}{}
			}
		}
	}
	for _, writer := range scope.namedWriters(name) {
		b.pinWriter(writer)
	}
	if len(scope.anyWriters) > 0 {
		b.succ["any:"] = struct{}{}
	}
}

// array holds what can publish the slots of the array called name: the
// extractors and steps that write name_<n> and name_count one by one as well
// as those that write the whole array.
func (b *importRefBuilder) array(name string) {
	if name == "" {
		return
	}
	for _, writer := range b.scope.arrayWriterSet(name) {
		b.pinWriter(writer)
	}
	if len(b.scope.anyWriters) > 0 {
		b.succ["any:"] = struct{}{}
	}
}

// family holds the steps of one protocol family: key is an index into
// importProtocolVariableFamilies, or importResponseMappingFamily.
func (b *importRefBuilder) family(key string) {
	if key == importResponseMappingFamily {
		for _, path := range b.scope.familySteps("java_http", []string{"java_http"}) {
			b.pinStep(path)
		}
		return
	}
	if index, err := strconv.Atoi(key); err == nil && index >= 0 && index < len(importProtocolVariableFamilies) {
		family := importProtocolVariableFamilies[index]
		for _, path := range b.scope.familySteps(family.typePrefix, family.protocols) {
			b.pinStep(path)
		}
	}
}

func (b *importRefBuilder) expression(expr string) {
	if key := importReferenceKey(expr); key != "" {
		b.succ[key] = struct{}{}
	}
}

func (b *importRefBuilder) expressions(exprs []string) {
	for _, expr := range exprs {
		b.expression(expr)
	}
}

func (b *importRefBuilder) pinWriter(writer importWriter) {
	b.pinStep(writer.path)
	if writer.index < 0 {
		return
	}
	key := fmt.Sprintf("%s|%s|%d", writer.path, writer.list, writer.index)
	if _, pinned := b.extractors[key]; pinned {
		return
	}
	list := b.scope.extractorList(writer.path, writer.list)
	if writer.index >= len(list) {
		return
	}
	b.extractors[key] = struct{}{}
	// An extractor's expression and default value are interpolated.
	var references []string
	collectImportTemplates(list[writer.index], &references)
	b.expressions(references)
}

// pinStep holds a step that publishes variables: its request fields, the block
// it is when it drives a loop, and everything those read, including the
// variables a source step names without a template.
func (b *importRefBuilder) pinStep(path string) {
	if _, pinned := b.steps[path]; pinned {
		return
	}
	scope := b.scope
	node := scope.nodes[path]
	if node == nil {
		return
	}
	b.steps[path] = struct{}{}
	if importIsJavaVuser(node) {
		b.all = true
	}
	b.expressions(scope.expressions(path))

	var control []string
	if block, _ := node["control"].(map[string]any); block != nil {
		collectImportControlReferences(block, &control)
	}
	b.expressions(control)
	if forEach := importForEach(node); forEach != nil {
		// The loop binds the rows of its source, so the rows are the value.
		if source := importCanonicalName(StringJSONValue(forEach["source"])); source != "" {
			b.succ["source:"+source] = struct{}{}
		}
	}

	step := scope.step(path)
	if !step.ok {
		return
	}
	bare, arrays := importBareReads(step.action)
	for _, name := range bare {
		if key := importReferenceKey("data." + name); key != "" {
			b.succ[key] = struct{}{}
		}
	}
	for _, name := range arrays {
		if name = importCanonicalName(name); name != "" {
			b.succ["array:"+name] = struct{}{}
		}
	}
	canonicalType := dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(step.action.Type)))
	if canonicalType == "source_json" {
		for _, document := range importJSONDocumentNames(step.action) {
			b.succ["json:"+document] = struct{}{}
		}
	}
	if _, shared := importTableStateTypes[canonicalType]; shared {
		b.succ["tables:"] = struct{}{}
	}
	if family := importPeerFamilyIndex(node); family >= 0 {
		// What a producer writes, a consumer of the family reads back, and the
		// record headers or message properties it sends are filled in first.
		b.succ["family:"+strconv.Itoa(family)] = struct{}{}
		b.expressions(scope.sentMetadataExpressions(path))
	}
	if importIsJavaVuser(node) {
		b.succ["java:"] = struct{}{}
	}
	if scope.readsLoopIndex(path) || scope.accumulating(path) {
		b.succ["loops:"+path] = struct{}{}
	}
}

// importPeerFamilyIndex returns the index in importProtocolVariableFamilies of
// the peer family the step belongs to (its producers and consumers), or -1.
func importPeerFamilyIndex(node map[string]any) int {
	actionType := dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(StringJSONValue(node["type"]))))
	for index, family := range importProtocolVariableFamilies {
		if family.peers && strings.HasPrefix(actionType, family.typePrefix) {
			return index
		}
	}
	return -1
}

// extractorDigest digests one extractor, given its "path|list|index" key.
func (s *importReferenceScope) extractorDigest(key string) string {
	if cached, ok := s.digests["x:"+key]; ok {
		return cached
	}
	parts := strings.Split(key, "|")
	index, _ := strconv.Atoi(parts[len(parts)-1])
	list := parts[len(parts)-2]
	path := strings.Join(parts[:len(parts)-2], "|")
	var extractor any
	if extractors := s.extractorList(path, list); index < len(extractors) {
		extractor = extractors[index]
	}
	return s.digest("x:"+key, map[string]any{"list": list, "extractor": extractor})
}

// stepDigest digests a step by what it does. Where it sits, its label and its
// analytics and timing attributes are left out, and the extractors of the step
// are held one by one by the writers that match. A loop around the step is
// part of what it does: it can run it again, and a step that accumulates
// (lr_param_increment, a lr_param_sprintf that appends to its own result)
// yields a new value each time.
func (s *importReferenceScope) stepDigest(path string) string {
	key := "n:" + path
	if cached, ok := s.digests[key]; ok {
		return cached
	}
	node := s.nodes[path]
	context := ImportActionWireContext(node)
	for field := range context {
		switch NormalizeScriptFieldName(field) {
		case "attribution", "pacingids", "timeoutms":
			delete(context, field)
		}
	}
	if request := s.asyncRequest(path); request != nil {
		if params, _ := context["params"].(map[string]any); params != nil {
			params["request"] = ImportActionWireContext(request)
		}
	}
	producer := map[string]any{"context": context}
	// The engine reads which LoadRunner function a source step runs, and in what
	// support mode, from its metadata when its params do not say.
	params, _ := node["params"].(map[string]any)
	if metadata, _ := node["metadata"].(map[string]any); metadata != nil {
		for field, param := range map[string]string{"source_function": "function", "support_mode": "support_mode"} {
			if value, ok := metadata[field]; ok && strings.TrimSpace(StringJSONValue(params[param])) == "" {
				producer["metadata."+field] = value
			}
		}
		// A producer's record headers or message properties are what the
		// consumers of its family read back.
		if importPeerFamilyIndex(node) >= 0 {
			sent := make(map[string]any)
			for key, value := range metadata {
				probe := dsl.Action{Type: StringJSONValue(node["type"]), Metadata: map[string]any{key: value}}
				if isSent, _ := varflow.MetadataReads(probe, key); isSent {
					sent[key] = value
				}
			}
			if len(sent) > 0 {
				producer["sent_metadata"] = sent
			}
		}
	}
	// The wire context leaves out control; a loop's block (its count,
	// condition and data-driven rows) is what it publishes.
	if block := importControlBlock(node); len(block) > 0 {
		producer["control"] = block
	}
	if s.accumulating(path) {
		// Where it runs and how often are part of what an accumulating step
		// supplies: its phase, and the loops around it.
		producer["phase"] = strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
		if loops := s.enclosingLoops(path); len(loops) > 0 {
			producer["loops"] = loops
		}
	}
	return s.digest(key, producer)
}

// enclosingLoopPaths returns the paths of the loops that contain the step at
// path, outermost first.
func (s *importReferenceScope) enclosingLoopPaths(path string) []string {
	var loops []string
	for rest := path; ; {
		cut := strings.LastIndex(rest, "/control/")
		if cut < 0 {
			break
		}
		rest = rest[:cut]
		if ancestor := s.nodes[rest]; ancestor != nil &&
			dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(StringJSONValue(ancestor["type"])))) == "control_loop" {
			loops = append([]string{rest}, loops...)
		}
	}
	return loops
}

// enclosingLoops returns the blocks of the loops that contain the step at
// path, outermost first.
func (s *importReferenceScope) enclosingLoops(path string) []any {
	var loops []any
	for _, loop := range s.enclosingLoopPaths(path) {
		loops = append(loops, importControlBlock(s.nodes[loop]))
	}
	return loops
}

// stepReads returns the graph keys of the variables the step at path reads:
// through its wire fields, and through the templates of its extractors (a
// default value is filled in).
func (s *importReferenceScope) stepReads(path string) map[string]struct{} {
	if cached, ok := s.reads[path]; ok {
		return cached
	}
	reads := make(map[string]struct{})
	exprs := append([]string(nil), s.expressions(path)...)
	for _, list := range []string{"extractors", "request"} {
		for _, extractor := range s.extractorList(path, list) {
			collectImportTemplates(extractor, &exprs)
		}
	}
	for _, expr := range exprs {
		if key := importReferenceKey(expr); key != "" {
			reads[key] = struct{}{}
		}
	}
	s.reads[path] = reads
	return reads
}

// importReadsVariable reports whether reads holds a read of the variable name,
// spelled {{var.name}} or {{data.name}}.
func importReadsVariable(reads map[string]struct{}, name string) bool {
	_, asVar := reads["var:"+name]
	_, asData := reads["bare:"+name]
	return asVar || asData
}

// readsLoopIndex reports whether the step at path reads the pass counter of its
// loops, which the loops' counts and conditions decide.
func (s *importReferenceScope) readsLoopIndex(path string) bool {
	return importReadsVariable(s.stepReads(path), "loop_index")
}

// accumulating reports whether the step at path yields a different value each
// time it runs: lr_param_increment adds to its variable, and a step that reads
// a variable it writes, directly, through a prefix, or round a cycle of steps,
// appends to it. How often such a step runs is part of what it supplies.
func (s *importReferenceScope) accumulating(path string) bool {
	if !s.accumulatesDone {
		s.findAccumulating()
	}
	return s.accumulates[path]
}

// findAccumulating marks the accumulating steps. The steps that compute a
// value in the script (those varflow lists outputs for) form a graph: a step
// has an edge to each such step that writes a variable it reads. A step on a
// cycle, or that reads its own output, accumulates. An extractor does not: its
// value comes from a response, not from the script, unless its default value
// reads the variable it fills.
func (s *importReferenceScope) findAccumulating() {
	s.accumulatesDone = true
	s.indexWriters()
	computes := make(map[string]struct{})
	for _, path := range s.order {
		step := s.step(path)
		if !step.ok {
			continue
		}
		if strings.EqualFold(importSourceFunction(step.action), "lr_param_increment") &&
			dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(step.action.Type))) == "source_parameter" {
			s.accumulates[path] = true
		}
		if outputs := varflow.StepOutputs(step.action); len(outputs.Writes)+len(outputs.Prefixes) > 0 {
			computes[path] = struct{}{}
		}
		for _, list := range []string{"extractors", "request"} {
			for _, raw := range s.extractorList(path, list) {
				extractor, _ := raw.(map[string]any)
				name := importCanonicalName(StringJSONValue(extractor["name"]))
				var templates []string
				collectImportTemplates(extractor, &templates)
				for _, expr := range templates {
					if key := importReferenceKey(expr); name != "" && (key == "var:"+name || key == "bare:"+name) {
						s.accumulates[path] = true
					}
				}
			}
		}
	}
	edges := make(map[string][]string, len(computes))
	for path := range computes {
		for key := range s.stepReads(path) {
			kind, name, _ := strings.Cut(key, ":")
			if kind != "var" && kind != "bare" {
				continue
			}
			for _, writer := range s.namedWriters(name) {
				if _, computed := computes[writer.path]; computed && writer.index < 0 {
					edges[path] = append(edges[path], writer.path)
				}
			}
		}
	}
	// Tarjan over the computing steps.
	index := make(map[string]int)
	low := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var connect func(string)
	connect = func(path string) {
		index[path], low[path] = len(index), len(index)
		stack = append(stack, path)
		onStack[path] = true
		for _, next := range edges[path] {
			if next == path {
				s.accumulates[path] = true
			}
			if _, seen := index[next]; !seen {
				connect(next)
				low[path] = min(low[path], low[next])
			} else if onStack[next] {
				low[path] = min(low[path], index[next])
			}
		}
		if low[path] != index[path] {
			return
		}
		var component []string
		for {
			member := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[member] = false
			component = append(component, member)
			if member == path {
				break
			}
		}
		if len(component) > 1 {
			for _, member := range component {
				s.accumulates[member] = true
			}
		}
	}
	for _, path := range s.order {
		if _, computed := computes[path]; computed {
			if _, seen := index[path]; !seen {
				connect(path)
			}
		}
	}
}
