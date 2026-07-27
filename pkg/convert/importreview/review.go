// Package importreview defines shared import findings, durable action gates, and
// source-neutral draft/replay/promotion readiness. It depends on the DSL but not
// on API response types.
package importreview

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// GateScope describes what a finding prevents.
type GateScope string

const (
	GateNone      GateScope = ""
	GatePromotion GateScope = "promotion"
	GateExecution GateScope = "execution"
)

// Finding is one deterministic conversion or readiness decision.
type Finding struct {
	Code                    string        `json:"code"`
	Severity                diag.Severity `json:"severity"`
	Element                 string        `json:"element,omitempty"`
	Detail                  string        `json:"detail"`
	Location                string        `json:"location,omitempty"`
	ActionPath              string        `json:"action_path,omitempty"`
	Source                  string        `json:"source,omitempty"`
	ReasonCode              string        `json:"reason_code,omitempty"`
	SourceRef               string        `json:"source_ref,omitempty"`
	Resolution              string        `json:"resolution,omitempty"`
	Gate                    GateScope     `json:"gate,omitempty"`
	AcknowledgementRequired bool          `json:"acknowledgement_required,omitempty"`
}

// State is the aggregate readiness state.
type State string

const (
	Ready          State = "ready"
	ReviewRequired State = "review_required"
	Blocked        State = "blocked"
)

// Readiness reports independent draft, replay, and promotion outcomes.
type Readiness struct {
	State              State    `json:"state"`
	DraftValid         bool     `json:"draft_valid"`
	ReplayReady        bool     `json:"replay_ready"`
	PromotionReady     bool     `json:"promotion_ready"`
	ValidationIssues   []string `json:"validation_issues,omitempty"`
	BlockingCodes      []string `json:"blocking_codes,omitempty"`
	ReviewCodes        []string `json:"review_codes,omitempty"`
	UnresolvedBindings int      `json:"unresolved_bindings,omitempty"`
}

// EvaluationInput contains the evidence used to compute Readiness.
type EvaluationInput struct {
	ValidationIssues   []string
	Findings           []Finding
	UnresolvedBindings int
}

// Evaluate computes readiness without mutating a script or its findings.
func Evaluate(input EvaluationInput) Readiness {
	validationIssues := uniqueSorted(input.ValidationIssues)
	findings := NormalizeFindings(input.Findings)
	blockingCodes := make([]string, 0)
	reviewCodes := make([]string, 0)
	for _, finding := range findings {
		code := firstNonEmpty(finding.Code, "import_finding")
		switch {
		case finding.Gate == GateExecution || finding.Severity == diag.Blocker:
			blockingCodes = append(blockingCodes, code)
		case finding.Gate == GatePromotion || finding.AcknowledgementRequired:
			reviewCodes = append(reviewCodes, code)
		}
	}
	if input.UnresolvedBindings > 0 {
		blockingCodes = append(blockingCodes, "credential_bindings_required")
	}
	blockingCodes = uniqueSorted(blockingCodes)
	reviewCodes = uniqueSorted(reviewCodes)

	readiness := Readiness{
		State:              Ready,
		DraftValid:         len(validationIssues) == 0,
		ReplayReady:        len(validationIssues) == 0 && len(blockingCodes) == 0,
		PromotionReady:     len(validationIssues) == 0 && len(blockingCodes) == 0 && len(reviewCodes) == 0,
		ValidationIssues:   validationIssues,
		BlockingCodes:      blockingCodes,
		ReviewCodes:        reviewCodes,
		UnresolvedBindings: max(input.UnresolvedBindings, 0),
	}
	switch {
	case !readiness.DraftValid || len(blockingCodes) > 0:
		readiness.State = Blocked
	case len(reviewCodes) > 0:
		readiness.State = ReviewRequired
	}
	return readiness
}

// EvaluateScript includes durable gates already attached to script actions.
func EvaluateScript(script *dsl.Script, input EvaluationInput) (Readiness, error) {
	scriptFindings, err := FindingsFromScript(script)
	if err != nil {
		return Readiness{}, err
	}
	input.Findings = append(append([]Finding(nil), input.Findings...), scriptFindings...)
	return Evaluate(input), nil
}

// Diagnostics projects rich findings into the existing converter diagnostic
// contract without exposing gate implementation details.
func Diagnostics(findings []Finding) diag.Diagnostics {
	findings = NormalizeFindings(findings)
	if len(findings) == 0 {
		return nil
	}
	out := make(diag.Diagnostics, 0, len(findings))
	for _, finding := range findings {
		out = append(out, diag.Diagnostic{
			Element:  firstNonEmpty(finding.Element, finding.Code),
			Detail:   finding.Detail,
			Severity: finding.Severity,
			Location: finding.Location,
		})
	}
	return out
}

// NormalizeFindings trims, defaults, deduplicates, and deterministically sorts
// findings. Codes are never derived from detail text.
func NormalizeFindings(input []Finding) []Finding {
	if len(input) == 0 {
		return nil
	}
	out := make([]Finding, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, finding := range input {
		finding.Code = strings.TrimSpace(finding.Code)
		finding.Element = strings.TrimSpace(finding.Element)
		finding.Detail = strings.TrimSpace(finding.Detail)
		finding.Location = strings.TrimSpace(finding.Location)
		finding.ActionPath = strings.TrimSpace(finding.ActionPath)
		finding.Source = strings.TrimSpace(finding.Source)
		finding.ReasonCode = strings.TrimSpace(finding.ReasonCode)
		finding.SourceRef = strings.TrimSpace(finding.SourceRef)
		finding.Resolution = strings.TrimSpace(finding.Resolution)
		if finding.Severity == "" {
			finding.Severity = diag.Warning
		}
		if finding.Code == "" && finding.Detail == "" {
			continue
		}
		key := findingIdentity(finding)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, finding)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return findingIdentity(out[i]) < findingIdentity(out[j])
	})
	return out
}

func findingIdentity(finding Finding) string {
	return strings.Join([]string{
		finding.ActionPath,
		finding.Location,
		finding.Code,
		finding.Source,
		finding.ReasonCode,
		finding.SourceRef,
		finding.Resolution,
		string(finding.Severity),
		string(finding.Gate),
		fmt.Sprint(finding.AcknowledgementRequired),
		finding.Element,
		finding.Detail,
	}, "\x00")
}

// ApplyToAction writes compatibility gate metadata understood by the current
// replay and launch boundaries. Existing unrelated metadata is preserved.
func ApplyToAction(action *dsl.Action, findings []Finding) {
	if action == nil {
		return
	}
	findings = NormalizeFindings(findings)
	if len(findings) == 0 {
		return
	}
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	executionCodes := metadataStringSlice(action.Metadata, "execution_blockers")
	existingExecution := metadataString(action.Metadata, "execution_blocker")
	if existingExecution != "" {
		executionCodes = append(executionCodes, existingExecution)
	}
	reviewCodes := metadataStringSlice(action.Metadata, "review_reasons")
	existingReviewGate := metadataString(action.Metadata, "review_gate")
	if existingReviewGate != "" {
		reviewCodes = append(reviewCodes, existingReviewGate)
	}
	for _, finding := range findings {
		code := firstNonEmpty(finding.Code, "import_finding")
		switch {
		case finding.Gate == GateExecution || finding.Severity == diag.Blocker:
			executionCodes = append(executionCodes, code)
		case finding.Gate == GatePromotion || finding.AcknowledgementRequired:
			reviewCodes = append(reviewCodes, code)
		}
	}
	executionCodes = uniqueSorted(executionCodes)
	reviewCodes = uniqueSorted(reviewCodes)
	if len(executionCodes) > 0 {
		action.Metadata["execution_blocker"] = firstNonEmpty(existingExecution, executionCodes[0])
		action.Metadata["execution_blockers"] = executionCodes
	}
	if len(reviewCodes) > 0 {
		action.Metadata["review_required"] = true
		action.Metadata["promotion_blocker"] = true
		action.Metadata["review_gate"] = firstNonEmpty(existingReviewGate, reviewCodes[0])
		action.Metadata["review_reasons"] = reviewCodes
	}
	if len(executionCodes) > 0 {
		applyDurableImportReview(action, findings, executionCodes)
	}
}

func applyDurableImportReview(action *dsl.Action, findings []Finding, executionCodes []string) {
	if action == nil {
		return
	}
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	if review == nil {
		review = make(map[string]any)
	}

	reasonCodes := metadataStringSlice(review, "reason_codes")
	sourceRefs := metadataStringSlice(review, "source_refs")
	source := metadataString(review, "source")
	resolution := metadataString(review, "resolution")
	for _, finding := range findings {
		if finding.Gate != GateExecution && finding.Severity != diag.Blocker {
			continue
		}
		reason := finding.ReasonCode
		if reason == "" {
			reason = finding.Code
		}
		reasonCodes = append(reasonCodes, reason)
		if finding.SourceRef != "" {
			sourceRefs = append(sourceRefs, finding.SourceRef)
		} else {
			sourceRefs = append(sourceRefs, firstNonEmpty(finding.Location, finding.ActionPath))
		}
		source = firstNonEmpty(source, finding.Source)
		resolution = firstNonEmpty(resolution, finding.Resolution)
	}
	if len(reasonCodes) == 0 {
		reasonCodes = append(reasonCodes, executionCodes...)
	}
	source = firstNonEmpty(
		source,
		metadataString(action.Metadata, "source_profile"),
		metadataString(action.Metadata, "source_format"),
		metadataString(action.Metadata, "import_source"),
		"import",
	)
	resolution = firstNonEmpty(resolution, "replace_behavior_and_remove_gate")

	review["version"] = 1
	review["source"] = source
	review["reason_codes"] = uniqueSorted(reasonCodes)
	review["source_refs"] = uniqueSorted(sourceRefs)
	review["resolution"] = resolution
	action.Metadata["import_review"] = review
}

// ApplyToScript applies findings carrying ActionPath to the matching action.
// Findings without ActionPath remain result-level diagnostics.
func ApplyToScript(script *dsl.Script, findings []Finding) error {
	if script == nil || len(findings) == 0 {
		return nil
	}
	byPath := make(map[string][]Finding)
	for _, finding := range findings {
		if path := strings.TrimSpace(finding.ActionPath); path != "" {
			byPath[path] = append(byPath[path], finding)
		}
	}
	if err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil {
			ApplyToAction(ref.Action, byPath[ref.Path])
		} else if findings := byPath[ref.Path]; len(findings) > 0 {
			action := dsl.Action{Name: ref.Name(), Metadata: ref.Metadata()}
			ApplyToAction(&action, findings)
			ref.SetMetadata(action.Metadata)
		}
		delete(byPath, ref.Path)
		return nil
	}); err != nil {
		return err
	}
	if len(byPath) == 0 {
		return nil
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return fmt.Errorf("import findings reference unknown action path(s): %s", strings.Join(paths, ", "))
}

// FindingsFromScript extracts compatibility review and execution gates from all
// top-level and nested actions.
func FindingsFromScript(script *dsl.Script) ([]Finding, error) {
	if script == nil {
		return nil, nil
	}
	var findings []Finding
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		metadata := ref.Metadata()
		element := firstNonEmpty(ref.Name(), "action")
		review, _ := metadata["import_review"].(map[string]any)
		reviewSource := metadataString(review, "source")
		reviewReasons := metadataStringSlice(review, "reason_codes")
		reviewRefs := metadataStringSlice(review, "source_refs")
		reviewResolution := metadataString(review, "resolution")
		executionCodes := metadataStringSlice(metadata, "execution_blockers")
		if code := metadataString(metadata, "execution_blocker"); code != "" {
			executionCodes = append(executionCodes, code)
		}
		for _, code := range uniqueSorted(executionCodes) {
			findings = append(findings, Finding{
				Code:       code,
				Severity:   diag.Blocker,
				Element:    element,
				Detail:     "imported behavior has an unresolved execution blocker",
				Location:   ref.Path,
				ActionPath: ref.Path,
				Source:     reviewSource,
				ReasonCode: firstString(reviewReasons),
				SourceRef:  firstString(reviewRefs),
				Resolution: reviewResolution,
				Gate:       GateExecution,
			})
		}
		reviewRequired := metadataBool(metadata, "review_required") ||
			metadataBool(metadata, "promotion_blocker")
		reviewCodes := metadataStringSlice(metadata, "review_reasons")
		if gate := metadataString(metadata, "review_gate"); gate != "" {
			reviewCodes = append(reviewCodes, gate)
		}
		reviewCodes = uniqueSorted(reviewCodes)
		if reviewRequired && len(reviewCodes) == 0 {
			reviewCodes = []string{"import_review_required"}
		}
		for _, code := range reviewCodes {
			findings = append(findings, Finding{
				Code:                    code,
				Severity:                diag.Warning,
				Element:                 element,
				Detail:                  "imported behavior requires review before replay or promotion",
				Location:                ref.Path,
				ActionPath:              ref.Path,
				Gate:                    GatePromotion,
				AcknowledgementRequired: true,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NormalizeFindings(findings), nil
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// metadataBool, metadataString and metadataStringSlice adapt the shared
// dslwalk extractors to this package's semantics: findings preserve the case an
// importer wrote, and lists are sorted so review output is stable.
func metadataBool(metadata map[string]any, key string) bool {
	return dslwalk.MetadataBool(metadata, key)
}

func metadataString(metadata map[string]any, key string) string {
	return dslwalk.MetadataString(metadata, key)
}

func metadataStringSlice(metadata map[string]any, key string) []string {
	return uniqueSorted(dslwalk.MetadataStrings(metadata, key))
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
