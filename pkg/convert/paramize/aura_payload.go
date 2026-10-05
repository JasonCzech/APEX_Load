package paramize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

const maxAuraInspectionBytes = 64 << 20
const maxAuraCandidateDetails = 2048
const maxAuraCandidateBytes = 256 << 10

// AuraInspectionBudget shares the fixed inspection work ceiling across a
// multi-script import. It contains no source values and is never serialized.
type AuraInspectionBudget struct {
	mu        sync.Mutex
	remaining int
}

func NewAuraInspectionBudget() *AuraInspectionBudget {
	return &AuraInspectionBudget{remaining: maxAuraInspectionBytes}
}
func (budget *AuraInspectionBudget) UsedWorkBytes() int {
	if budget == nil {
		return 0
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return maxAuraInspectionBytes - budget.remaining
}

// AuraPayloadCoverage separates inspected field coverage from retained advice.
// Counts and fixed warnings are safe even when no selectable field is retained.
type AuraPayloadCoverage struct {
	skipBodies              map[string]bool
	Complete                bool `json:"complete"`
	RecognizedBodies        int  `json:"recognized_bodies"`
	InspectedBodies         int  `json:"inspected_bodies"`
	RejectedBodies          int  `json:"rejected_bodies"`
	OmittedBodies           int  `json:"omitted_bodies"`
	StringFields            int  `json:"string_fields"`
	UnsupportedNumberFields int  `json:"unsupported_number_fields"`
	UnsupportedOtherFields  int  `json:"unsupported_other_fields"`
	UnsafePathFields        int  `json:"unsafe_path_fields"`
	CandidateFields         int  `json:"candidate_fields"`
	RetainedCandidates      int  `json:"retained_candidates"`
	OmittedCandidates       int  `json:"omitted_candidates"`
}

func hasAuraLocation(l Location) bool {
	return len(l.AuraRoute) > 0 || l.ActionPath != "" || l.PayloadShape != "" || l.DecodedFieldPath != "" || len(l.EncodingLayers) > 0
}

// LooksAuraParameterName applies the shared business-name heuristic to a
// structured Aura leaf, excluding Aura framework and session fields.
func LooksAuraParameterName(key string) bool {
	switch strings.Join(parameterNameWords(key), "") {
	case "fwuid", "context", "applicationcontext", "sessionid":
		return false
	}
	return LooksParameterName(key)
}

// Recognition shares the pass budget and scans forms incrementally. Concrete
// Salesforce API routes take precedence over body-shaped lookalikes.
func probeAuraBody(target, body, encoding string, remaining *int) (candidate bool, code string) {
	if len(target) > *remaining {
		*remaining = 0
		return false, "work_limit"
	}
	*remaining -= len(target)
	parsed, err := url.Parse(target)
	if err != nil {
		return false, ""
	}
	path := strings.ToLower(parsed.Path)
	for _, prefix := range []string{"/services/data/", "/services/apexrest/", "/services/oauth2/", "/services/async/", "/services/soap/", "/services/tooling/", "/ui-api/"} {
		if strings.HasPrefix(path, prefix) {
			return false, ""
		}
	}
	for rest := path; rest != ""; {
		part, next, more := strings.Cut(rest, "/")
		rest = next
		if part == "aura" {
			return true, ""
		}
		if !more {
			break
		}
	}
	if body == "" {
		return false, ""
	}
	// Eight authored bytes conservatively cover decoding, field/key unescaping,
	// and envelope evidence searches before the full parser's actual accounting.
	if len(body) > *remaining/8 {
		*remaining = 0
		return false, "work_limit"
	}
	*remaining -= 8 * len(body)
	if len(body) > aurapayload.MaxBodyBytes {
		return false, "byte_limit"
	}
	wire := body
	if strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		decoded, _, err := dsl.DecodeBodyEncoding(body, encoding, aurapayload.MaxBodyBytes)
		if err != nil {
			if err == dsl.ErrBodyEncodingLimit {
				return false, "byte_limit"
			}
			return false, "invalid_encoding"
		}
		wire = string(decoded)
	} else {
		switch strings.ToLower(strings.TrimSpace(encoding)) {
		case "", "text", "utf8", "utf-8":
		default:
			return false, "unsupported_encoding"
		}
	}
	evidence := func(s string) bool { return strings.Contains(s, `"actions"`) && strings.Contains(s, `"descriptor"`) }
	if strings.HasPrefix(strings.TrimSpace(wire), "{") {
		return evidence(wire), ""
	}
	fields := 0
	for rest := wire; rest != ""; {
		fields++
		if fields > 4096 {
			return false, "field_limit"
		}
		field, next, more := strings.Cut(rest, "&")
		rest = next
		key, value, hasValue := strings.Cut(field, "=")
		if len(key) > 256 {
			return false, "field_limit"
		}
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			return false, "invalid_form"
		}
		if hasValue && decodedKey == "message" {
			decoded, err := url.QueryUnescape(value)
			if err != nil {
				return false, "invalid_form"
			}
			if evidence(decoded) {
				return true, ""
			}
		}
		if !more {
			break
		}
	}
	return false, ""
}

func surfaceBody(ref dslwalk.SurfaceRef) (target, body, encoding string) {
	if ref.Action != nil {
		return ref.Action.Target, ref.Action.Body, ref.Action.BodyEncoding
	}
	target, _ = ref.Request["target"].(string)
	body, _ = ref.Request["body"].(string)
	encoding, _ = ref.Request["body_encoding"].(string)
	return
}
func surfaceHTTP(ref dslwalk.SurfaceRef) bool {
	if ref.Kind == dslwalk.HTTPSequenceRequestSurface {
		return true
	}
	if ref.Action == nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(ref.Action.Type)) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "REQUEST", "HTTP_REQUEST", "HTTP_GET", "HTTP_POST", "HTTP_PUT", "HTTP_PATCH", "HTTP_DELETE", "HTTP_HEAD", "HTTP_OPTIONS":
		return true
	}
	return false
}
func topIndex(path string) int {
	start := strings.IndexByte(path, '[')
	if start < 0 {
		return -1
	}
	end := strings.IndexByte(path[start:], ']')
	if end < 0 {
		return -1
	}
	index, _ := strconv.Atoi(path[start+1 : start+end])
	return index
}

// Only structural parent names are disclosed. Unknown object-map keys can be
// captured data themselves; they are counted as unsafe instead of echoed.
var auraStructuralKeys = map[string]bool{
	"actions": true, "params": true, "input": true, "inputs": true, "data": true, "records": true, "record": true, "recordInput": true, "recordInputs": true,
	"fields": true, "attributes": true, "items": true, "item": true, "values": true, "details": true, "request": true, "requests": true, "payload": true, "body": true,
	"entities": true, "entity": true, "accounts": true, "account": true, "contacts": true, "contact": true, "options": true, "criteria": true, "filters": true, "filter": true,
	"parameters": true, "arguments": true, "args": true, "value": true, "rows": true, "row": true, "properties": true, "object": true, "objects": true,
}

func safeAuraKey(key string) bool {
	if len(key) == 0 || len(key) > 80 {
		return false
	}
	for i, c := range key {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return auraStructuralKeys[key]
}

// SafeAuraParameterRoute reports whether a structural route can cross the
// disclosure boundary. It does not admit source-valued object-map keys.
func SafeAuraParameterRoute(route []aurapayload.Segment) bool {
	if len(route) < 4 || route[0].Kind != aurapayload.KeySegment || route[0].Key != "actions" || route[1].Kind != aurapayload.IndexSegment || route[2].Kind != aurapayload.KeySegment || route[2].Key != "params" {
		return false
	}
	lastKey := -1
	for i, segment := range route {
		if segment.Kind == aurapayload.KeySegment {
			lastKey = i
		}
	}
	for i, segment := range route {
		if segment.Kind == aurapayload.IndexSegment {
			if segment.Index < 0 || segment.Key != "" {
				return false
			}
			continue
		}
		if segment.Kind == aurapayload.JSONStringSegment {
			if segment.Key != "" || segment.Index != 0 {
				return false
			}
			continue
		}
		if segment.Kind != aurapayload.KeySegment || segment.Index != 0 {
			return false
		}
		lower := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(segment.Key))
		if credential.SensitiveName(segment.Key) || strings.Contains(lower, "context") || strings.Contains(lower, "fwuid") || strings.Contains(lower, "nonce") || strings.Contains(lower, "session") || (i != lastKey && !safeAuraKey(segment.Key)) || (i == lastKey && !safeAuraBusinessLeaf(segment.Key)) {
			return false
		}
	}
	return true
}

func safeAuraBusinessLeaf(key string) bool {
	if len(key) == 0 || len(key) > 80 {
		return false
	}
	for i, c := range key {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	for _, subject := range []string{"", "record", "external", "account", "customer", "user", "order", "product", "item", "contact", "transaction", "invoice", "subscription", "lead", "opportunity"} {
		for _, suffix := range []string{"id", "email", "sku", "zip", "number", "code"} {
			if normalized == subject+suffix {
				return true
			}
		}
	}
	return false
}

func eligibleAuraLeaf(leaf aurapayload.Leaf) (business, safe bool) {
	route := leaf.Route
	if len(route) < 4 || route[0].Kind != aurapayload.KeySegment || route[0].Key != "actions" || route[1].Kind != aurapayload.IndexSegment || route[2].Kind != aurapayload.KeySegment || route[2].Key != "params" {
		return false, false
	}
	if !LooksAuraParameterName(leaf.Key) {
		return false, false
	}
	for _, segment := range route {
		if segment.Kind != aurapayload.KeySegment {
			continue
		}
		lower := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(segment.Key))
		if credential.SensitiveName(segment.Key) || strings.Contains(lower, "context") || strings.Contains(lower, "fwuid") || strings.Contains(lower, "nonce") || strings.Contains(lower, "session") {
			return false, false
		}
	}
	return true, SafeAuraParameterRoute(route)
}

// The public digest excludes unknown/private object-map keys as well as all
// values. The codec's complete structural digest remains server-side only.
func publicAuraPayloadShape(inspection aurapayload.Inspection) string {
	hash := sha256.New()
	write := func(s string) { _, _ = hash.Write([]byte(strconv.Itoa(len(s)) + ":")); _, _ = hash.Write([]byte(s)) }
	write("aura_business_strings_v1")
	write(inspection.Codec)
	// Shared codec emits canonical object-member and array order.
	for _, leaf := range inspection.Leaves {
		business, safe := eligibleAuraLeaf(leaf)
		if !business || !safe {
			continue
		}
		write("leaf")
		write(leaf.ValueType)
		for _, segment := range leaf.Route {
			write(segment.Kind)
			write(segment.Key)
			write(strconv.Itoa(segment.Index))
		}
		for _, layer := range leaf.EncodingLayers {
			write(layer)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func publicAuraShapeWork(inspection aurapayload.Inspection) int {
	work := len(inspection.Codec) + 64
	for _, leaf := range inspection.Leaves {
		business, safe := eligibleAuraLeaf(leaf)
		if !business || !safe {
			continue
		}
		work += len(leaf.ValueType) + 16
		for _, segment := range leaf.Route {
			work += len(segment.Kind) + len(segment.Key) + 32
		}
		for _, layer := range leaf.EncodingLayers {
			work += len(layer) + 16
		}
	}
	return work
}

func auraPayloadSuggestions(script *dsl.Script, names *nameRegistry, claimed map[string]bool, budgets ...*AuraInspectionBudget) ([]Suggestion, *AuraPayloadCoverage, []string) {
	coverage := &AuraPayloadCoverage{Complete: true, skipBodies: map[string]bool{}}
	suggestions := []Suggestion{}
	warnings := []string{}
	warned := map[string]bool{}
	warn := func(text string) {
		if !warned[text] {
			warnings = append(warnings, text)
			warned[text] = true
		}
	}
	budget := NewAuraInspectionBudget()
	if len(budgets) > 0 && budgets[0] != nil {
		budget = budgets[0]
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	inspectionBytes, detailBytes := maxAuraInspectionBytes-budget.remaining, 0
	defer func() { budget.remaining = maxAuraInspectionBytes - inspectionBytes }()
	detailsFull := false
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if !surfaceHTTP(ref) {
			return nil
		}
		target, body, encoding := surfaceBody(ref)
		if body == "" {
			return nil
		}
		remaining := maxAuraInspectionBytes - inspectionBytes
		candidate, probeCode := probeAuraBody(target, body, encoding, &remaining)
		inspectionBytes = maxAuraInspectionBytes - remaining
		if probeCode != "" {
			coverage.skipBodies[ref.Path] = true
			coverage.OmittedBodies++
			coverage.Complete = false
			warn("Aura payload detection could not inspect a request body: " + probeCode)
			return nil
		}
		if !candidate {
			return nil
		}
		coverage.skipBodies[ref.Path] = true
		coverage.RecognizedBodies++
		if len(body) > maxAuraInspectionBytes-inspectionBytes {
			coverage.OmittedBodies++
			coverage.Complete = false
			warn("Aura payload inspection reached its aggregate work limit; remaining bodies were omitted.")
			return nil
		}
		inspection, err := aurapayload.InspectWithWorkLimit(body, encoding, maxAuraInspectionBytes-inspectionBytes)
		if err != nil {
			if typed, ok := err.(*aurapayload.Error); ok {
				inspectionBytes += typed.WorkBytes
			}
			coverage.RejectedBodies++
			coverage.Complete = false
			warn("Aura payload inspection rejected a body: " + err.Error())
			return nil
		}
		inspectionBytes += inspection.WorkBytes
		coverage.InspectedBodies++
		shapeWork := publicAuraShapeWork(inspection)
		if shapeWork > maxAuraInspectionBytes-inspectionBytes {
			coverage.OmittedBodies++
			coverage.Complete = false
			warn("Aura payload inspection reached its aggregate safe-shape work limit.")
			return nil
		}
		inspectionBytes += shapeWork
		publicShape := publicAuraPayloadShape(inspection)
		sort.SliceStable(inspection.Leaves, func(i, j int) bool { return inspection.Leaves[i].DisplayPath < inspection.Leaves[j].DisplayPath })
		for _, leaf := range inspection.Leaves {
			business, safe := eligibleAuraLeaf(leaf)
			if !business {
				continue
			}
			if !safe {
				coverage.UnsafePathFields++
				coverage.Complete = false
				continue
			}
			switch leaf.ValueType {
			case "number":
				coverage.UnsupportedNumberFields++
				continue
			case "string":
				coverage.StringFields++
			default:
				coverage.UnsupportedOtherFields++
				continue
			}
			if leaf.Value == "" || isTemplated(leaf.Value) || claimed[leaf.Value] {
				continue
			}
			coverage.CandidateFields++
			if detailsFull || len(suggestions) >= maxAuraCandidateDetails || len(leaf.Value) > maxAuraCandidateBytes-detailBytes {
				coverage.OmittedCandidates++
				coverage.Complete = false
				detailsFull = true
				continue
			}
			varName := names.claim(leaf.Key)
			template := "{{var." + varName + "}}"
			candidate := Suggestion{Kind: KindDataParam, VarName: varName, Replacement: template, LiteralValue: leaf.Value, SampleValue: truncateValue(leaf.Value),
				Locations: []Location{{Section: string(ref.Phase), ActionIndex: topIndex(ref.Path), Field: "body", SearchText: leaf.Value, ReplaceText: template, ActionPath: ref.Path,
					AuraRoute: append([]aurapayload.Segment(nil), leaf.Route...), DecodedFieldPath: leaf.DisplayPath, EncodingLayers: append([]string(nil), leaf.EncodingLayers...), PayloadShape: publicShape, auraExpectedShape: inspection.Shape}},
				Confidence: dataParamConfidence, RuleID: "data_param.aura_business_leaf", Rationale: "Confirm this decoded Aura business field is per-user test data before binding it to a data source.",
				Parameter: &dsl.Parameter{Name: varName, Type: "file", DataSource: consolidatedDataSourceName, Column: varName}}
			candidate.ID = suggestionID(candidate)
			detailJSON, marshalErr := json.Marshal(candidate)
			if marshalErr != nil || len(detailJSON) > maxAuraCandidateBytes-detailBytes {
				coverage.OmittedCandidates++
				coverage.Complete = false
				detailsFull = true
				continue
			}
			detailBytes += len(detailJSON)
			suggestions = append(suggestions, candidate)
		}
		return nil
	})
	if err != nil {
		coverage.Complete = false
		warn("Aura payload inspection could not finish bounded action traversal.")
	}
	coverage.RetainedCandidates = len(suggestions)
	if coverage.UnsupportedNumberFields+coverage.UnsupportedOtherFields > 0 {
		warn("Aura payload parameterization supports string business fields; nonstring business fields remain unsupported.")
	}
	if coverage.UnsafePathFields > 0 {
		warn("Aura payload inspection omitted fields with undisclosed object-map keys.")
	}
	if coverage.OmittedCandidates > 0 {
		warn("Aura payload inspection reached its retained candidate detail limit; candidate counts include omitted advice.")
	}
	if coverage.RecognizedBodies == 0 && coverage.OmittedBodies == 0 && err == nil {
		return suggestions, nil, warnings
	}
	return suggestions, coverage, warnings
}

type auraBodyPlan struct {
	Body     string
	Diffs    []Diff
	Contract *aurapayload.TemplateContract
}

// planAuraApplication validates all edits against original bodies. The returned
// plan is applied only to the cloned script after every selected edit passes.
func planAuraApplication(script *dsl.Script, suggestions []Suggestion, selected map[string]bool) (map[string]auraBodyPlan, error) {
	groups := map[string][]aurapayload.Edit{}
	diffs := map[string][]Diff{}
	locations := map[string][]Location{}
	existingNames := newNameRegistry(script)
	selectedNames := map[string]string{}
	for _, s := range suggestions {
		if !selected[s.ID] || s.PreApplied {
			continue
		}
		for _, l := range s.Locations {
			if !hasAuraLocation(l) {
				continue
			}
			if len(s.Locations) != 1 || s.VarName == "" || correlate.Slug(s.VarName) != s.VarName || existingNames.taken[s.VarName] || (selectedNames[s.VarName] != "" && selectedNames[s.VarName] != s.ID) || s.Kind != KindDataParam || s.Parameter == nil || s.Parameter.Name != s.VarName || s.Parameter.Column != s.VarName || s.Replacement != "{{var."+s.VarName+"}}" || l.ReplaceText != s.Replacement || l.Field != "body" || l.ActionPath == "" || len(l.AuraRoute) == 0 || l.SearchText != s.LiteralValue || len(l.AuraRoute) > aurapayload.MaxRouteSegments {
				return nil, fmt.Errorf("selected Aura parameterization has an invalid descriptor")
			}
			routeBytes, routeErr := json.Marshal(l.AuraRoute)
			if routeErr != nil || len(routeBytes) > aurapayload.MaxRouteBytes {
				return nil, fmt.Errorf("selected Aura parameterization has an invalid route")
			}
			selectedNames[s.VarName] = s.ID
			groups[l.ActionPath] = append(groups[l.ActionPath], aurapayload.Edit{Route: l.AuraRoute, Shape: l.PayloadShape, Expected: s.LiteralValue, Replacement: l.ReplaceText})
			locations[l.ActionPath] = append(locations[l.ActionPath], l)
			diffs[l.ActionPath] = append(diffs[l.ActionPath], Diff{SuggestionID: s.ID, Kind: "value_replaced", Path: l.ActionPath + ":" + l.DecodedFieldPath})
		}
	}
	plans := map[string]auraBodyPlan{}
	if len(groups) == 0 {
		return plans, nil
	}
	// Mixed raw-body and structured edits could consume one another's source.
	for _, s := range suggestions {
		if !selected[s.ID] || s.PreApplied {
			continue
		}
		for _, l := range s.Locations {
			if hasAuraLocation(l) || l.Field != "body" {
				continue
			}
			path := fmt.Sprintf("%s[%d]", l.Section, l.ActionIndex)
			if _, exists := groups[path]; exists {
				return nil, fmt.Errorf("selected Aura and literal body parameterizations overlap")
			}
		}
	}
	work := 0
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		edits, ok := groups[ref.Path]
		if !ok {
			return nil
		}
		if !surfaceHTTP(ref) {
			return fmt.Errorf("selected Aura parameterization references an unavailable HTTP surface")
		}
		_, body, encoding := surfaceBody(ref)
		if len(body) > maxAuraInspectionBytes-work {
			return fmt.Errorf("selected Aura parameterization exceeds its aggregate work limit")
		}
		inspection, err := aurapayload.InspectWithWorkLimit(body, encoding, maxAuraInspectionBytes-work)
		if err != nil {
			return err
		}
		work += inspection.WorkBytes
		shapeWork := publicAuraShapeWork(inspection)
		if shapeWork > maxAuraInspectionBytes-work {
			return fmt.Errorf("selected Aura parameterization exceeds its aggregate safe-shape work limit")
		}
		work += shapeWork
		publicShape := publicAuraPayloadShape(inspection)

		leavesByPath := make(map[string]int, len(inspection.Leaves))
		for i, leaf := range inspection.Leaves {
			leavesByPath[leaf.DisplayPath] = i
		}
		for index, l := range locations[ref.Path] {
			if l.PayloadShape != publicShape {
				return fmt.Errorf("selected Aura parameterization has a stale public shape")
			}
			expectedShape := inspection.Shape
			if l.auraExpectedShape != "" {
				expectedShape = l.auraExpectedShape
			}
			edits[index].Shape = expectedShape
			if l.Section != string(ref.Phase) || l.ActionIndex != topIndex(ref.Path) {
				return fmt.Errorf("selected Aura parameterization has an invalid action path")
			}
			leafIndex, exists := leavesByPath[l.DecodedFieldPath]
			if !exists {
				return fmt.Errorf("selected Aura parameterization has an unavailable decoded field")
			}
			leaf := inspection.Leaves[leafIndex]
			business, safe := eligibleAuraLeaf(leaf)
			if !business || !safe || leaf.ValueType != "string" || isTemplated(leaf.Value) || !reflect.DeepEqual(leaf.Route, l.AuraRoute) || !reflect.DeepEqual(leaf.EncodingLayers, l.EncodingLayers) {
				return fmt.Errorf("selected Aura parameterization has an invalid decoded field")
			}
		}

		metadata := ref.Metadata()
		policy, _ := metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey].(string)
		contract, used, err := aurapayload.PromoteTemplateContractWithWorkAccounting(body, encoding, strings.TrimSpace(policy), metadata[dsl.ImportAuraTemplateContractMetadataKey], maxAuraInspectionBytes-work)
		work += used
		if err != nil {
			return err
		}
		replaced, used, err := aurapayload.ReplaceWithWorkAccounting(body, encoding, edits, maxAuraInspectionBytes-work)
		work += used
		if err != nil {
			return err
		}
		// Edits cannot redirect a retained occurrence. Check the final body
		// before publishing any part of the atomic application plan.
		_, _, used, err = aurapayload.ResolveContextualWithWorkAccounting(replaced, encoding, contract, func(value string, _ aurapayload.TemplateContext) (string, string) { return value, value }, maxAuraInspectionBytes-work)
		work += used
		if err != nil {
			return err
		}
		plans[ref.Path] = auraBodyPlan{Body: replaced, Diffs: diffs[ref.Path], Contract: contract}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("selected Aura parameterization could not be applied: %w", err)
	}
	if len(plans) != len(groups) {
		return nil, fmt.Errorf("selected Aura parameterization references an unavailable action path")
	}
	return plans, nil
}
func applyAuraPlans(script *dsl.Script, plans map[string]auraBodyPlan) ([]Diff, error) {
	diffs := []Diff{}
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		plan, ok := plans[ref.Path]
		if !ok {
			return nil
		}
		if ref.Action != nil {
			ref.Action.Body = plan.Body
		} else {
			ref.Request["body"] = plan.Body
		}
		metadata := ref.Metadata()
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] = dsl.ImportAuraJSONTemplateValueEncoding
		metadata[dsl.ImportAuraTemplateContractMetadataKey] = plan.Contract
		ref.SetMetadata(metadata)
		diffs = append(diffs, plan.Diffs...)
		return nil
	})
	return diffs, err
}
