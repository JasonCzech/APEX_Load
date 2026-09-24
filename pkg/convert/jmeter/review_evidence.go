package jmeter

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// ReviewSummary counts source behaviors, independently of sampler/action totals.
// UnsupportedBehaviors is a subset, not an additional issue count.
type ReviewSummary struct {
	BlockingBehaviors    int `json:"blocking_behaviors"`
	ReviewBehaviors      int `json:"review_behaviors"`
	UnsupportedBehaviors int `json:"unsupported_behaviors"`
	Warnings             int `json:"warnings"`
	Informational        int `json:"informational"`
}

type ReviewOutcome struct {
	importreview.Finding
	Unsupported bool
}

func canonicalReviewCode(code string) string {
	switch code {
	case "missing_variable":
		return "import_missing_variables"
	}
	return code
}

// NormalizeReviewEvidence joins typed source diagnostics with durable gate reasons.
// Display strings are output only; they are never parsed to infer readiness.
func (r *ConvertResult) NormalizeReviewEvidence() {
	var outcomes []ReviewOutcome
	mappings := []sourceview.Mapping{}
	if r.SourceReview != nil {
		mappings = r.SourceReview.Mappings
	}
	source := r.Summary.FileName
	if source == "" && len(mappings) > 0 {
		source = mappings[0].SourceFile
	}
	locate := func(location, element string) (string, string) {
		for _, m := range mappings {
			if m.TargetKind == "disabled" {
				continue
			}
			if m.TargetName == location || (m.TargetPath == location && (m.TargetKind == "action" || m.TargetKind == "controller")) {
				return fmt.Sprintf("%s:%d#%s", m.SourceFile, m.StartLine, m.ID), m.TargetPath
			}
		}
		return source + "#" + location + "#" + element, location
	}
	unsupportedPaths := map[string]bool{}
	controllerGaps := map[string]sourceview.Mapping{}
	for _, m := range mappings {
		if m.TargetKind == "controller" && m.Status == "review_only" {
			controllerGaps[m.TargetPath] = m
		}
		if m.TargetKind == "disabled" || m.Status == "ignored" {
			continue
		}
		if m.Status == "unsupported" || m.TargetKind == "unsupported" || (m.TargetKind == "postprocessor" && m.Status == "review_only") {
			code := "unsupported_jmeter_behavior"
			if m.TargetKind == "postprocessor" {
				code = "unsupported_jmeter_processor"
			}
			outcomes = append(outcomes, ReviewOutcome{Finding: importreview.Finding{Code: code, Severity: diag.Blocker, Element: m.TargetName, Detail: m.Notes, Location: m.TargetPath, ActionPath: m.TargetPath, Source: source, SourceRef: fmt.Sprintf("%s:%d#%s", m.SourceFile, m.StartLine, m.ID), Gate: importreview.GateExecution}, Unsupported: true})
			unsupportedPaths[m.TargetPath+"\x00"+code] = true
		}
	}
	gatedLocations := map[string]bool{}
	for si, script := range r.Scripts {
		walkErr := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
			meta := ref.Metadata()
			review, _ := meta["import_review"].(map[string]any)
			reasons := dslwalk.MetadataStrings(review, "reason_codes")
			codes := append([]string{}, dslwalk.MetadataStrings(meta, "execution_blockers")...)
			if c := dslwalk.MetadataString(meta, "execution_blocker"); c != "" {
				codes = append(codes, c)
			}
			if len(codes) > 0 {
				codes = append(codes, reasons...)
			}
			path := ref.Path
			if len(r.Scripts) > 1 {
				path = fmt.Sprintf("scripts[%d].%s", si, path)
			}
			src, _ := locate(path, ref.Name())
			seenCodes := map[string]bool{}
			for _, code := range codes {
				code = canonicalReviewCode(code)
				if seenCodes[code] {
					continue
				}
				seenCodes[code] = true
				if code == "import_review_required" && len(reasons) > 0 {
					continue
				}
				if unsupportedPaths[path+"\x00"+code] {
					continue
				}
				detail := "Resolve imported behavior before replay: " + code
				switch code {
				case "import_missing_variables":
					detail = "Declare or explicitly bind unresolved variables: " + strings.Join(dslwalk.MetadataStrings(meta, "missing_variables"), ", ")
				case "jmeter_timestamp_timezone_required":
					detail = "Select a timestamp policy: UTC normalization or a source timezone and acknowledge the literal Z suffix."
				case "jmeter_masked_source_value":
					detail = "Replace redaction placeholders with real values or explicit secret bindings, then regenerate."
				}
				element := ref.Name()
				unsupported := strings.HasPrefix(code, "unsupported_")
				if m, ok := controllerGaps[path]; ok {
					unsupported = true
					element = m.Function + ": " + m.TargetName
					detail = m.Notes
				}
				outcomes = append(outcomes, ReviewOutcome{Finding: importreview.Finding{Code: code, Severity: diag.Blocker, Element: element, Detail: detail, Location: path, ActionPath: path, Source: source, SourceRef: src, Gate: importreview.GateExecution}, Unsupported: unsupported})
			}
			if len(codes) > 0 {
				gatedLocations[ref.Path] = true
				gatedLocations[path] = true
				gatedLocations[ref.Name()] = true
			}
			// Promotion-only gates remain distinct from ordinary warnings.
			if dslwalk.MetadataBool(meta, "review_required") || dslwalk.MetadataBool(meta, "promotion_blocker") {
				reviewCodes := dslwalk.MetadataStrings(meta, "review_reasons")
				if c := dslwalk.MetadataString(meta, "review_gate"); c != "" {
					reviewCodes = append(reviewCodes, c)
				}
				if len(reviewCodes) == 0 {
					reviewCodes = []string{"import_review_required"}
				}
				for _, code := range reviewCodes {
					outcomes = append(outcomes, ReviewOutcome{Finding: importreview.Finding{Code: canonicalReviewCode(code), Severity: diag.Warning, Element: ref.Name(), Detail: "Review imported behavior: " + code, Location: path, ActionPath: path, Source: source, SourceRef: src, Gate: importreview.GatePromotion}})
				}
			}
			return nil
		})
		if walkErr != nil {
			outcomes = append(outcomes, ReviewOutcome{Finding: importreview.Finding{Code: "jmeter_review_traversal_failed", Severity: diag.Blocker, Element: "JMeter script", Detail: "Review traversal failed; regenerate and validate the draft before replay.", Source: source, SourceRef: fmt.Sprintf("%s#scripts[%d]", source, si), Gate: importreview.GateExecution}})
		}
	}
	// Prefer authored findings for actionable details and canonical identities.
	for _, f := range r.Findings {
		f.Code = canonicalReviewCode(f.Code)
		represented := false
		for i := range outcomes {
			o := &outcomes[i]
			sameSource := (f.ActionPath != "" && o.ActionPath == f.ActionPath) || (f.Location != "" && o.Location == f.Location) || (o.Element != "" && strings.HasPrefix(f.Location, o.Element+"/"))
			if f.SourceRef != "" && f.SourceRef != f.Location {
				sameSource = o.SourceRef == f.SourceRef
			}
			if o.Code == f.Code && sameSource {
				if f.Detail != "" && f.Code != "import_missing_variables" {
					o.Detail = f.Detail
					o.Element = f.Element
					o.Location = f.Location
					if f.SourceRef != "" && f.SourceRef != f.Location {
						o.SourceRef = f.SourceRef
					}
				}
				// Retain the finding's gate/severity as additional evidence;
				// enriching display details must not discard stronger readiness.
				f.Source, f.SourceRef, f.Detail = o.Source, o.SourceRef, o.Detail
				outcomes = append(outcomes, ReviewOutcome{Finding: f})
				represented = true
				break
			}
		}
		if represented {
			continue
		}
		if f.Source == "" || f.Source == "jmeter" {
			f.Source = source
		}
		if f.SourceRef == "" || f.SourceRef == f.Location {
			f.SourceRef, _ = locate(f.Location, f.Element)
		}
		outcomes = append(outcomes, ReviewOutcome{Finding: f})
	}
	// Retain typed diagnostics not already represented by a durable source decision.
	for _, d := range r.Unsupported {
		src, path := locate(d.Location, d.Element)
		// A coded diagnostic has its own reason identity. Only legacy uncoded
		// diagnostics need the location-based compatibility matching below.
		if d.Code != "" {
			if d.SourceRef != "" {
				src = d.SourceRef
			}
			gate := importreview.GateNone
			if d.Severity == diag.Blocker {
				gate = importreview.GateExecution
			}
			outcomes = append(outcomes, ReviewOutcome{Finding: importreview.Finding{Code: canonicalReviewCode(d.Code), Severity: d.Severity, Element: d.Element, Detail: d.Detail, Location: path, Source: source, SourceRef: src, Gate: gate}})
			continue
		}
		represented := false
		for _, o := range outcomes {
			if (o.Unsupported && o.SourceRef == src) || (o.Detail == d.Detail && o.Severity == d.Severity && (o.SourceRef == src || o.Location == d.Location)) || (d.Severity == diag.Blocker && (o.SourceRef == src || o.Location == d.Location || (o.Element != "" && strings.HasPrefix(d.Location, o.Element+"/")))) {
				represented = true
				break
			}
		}
		if d.Severity == diag.Blocker && gatedLocations[d.Location] {
			represented = true
		}
		if represented {
			continue
		}
		isUnsupported := false
		for _, name := range r.UnsupportedElements {
			if d.Element == name || d.Element == "JMeter "+name {
				isUnsupported = true
				break
			}
		}
		gate := importreview.GateNone
		if isUnsupported {
			gate = importreview.GatePromotion
		}
		outcomes = append(outcomes, ReviewOutcome{Unsupported: isUnsupported, Finding: importreview.Finding{Gate: gate, Code: "jmeter_diagnostic:" + d.Element, Severity: d.Severity, Element: d.Element, Detail: d.Detail, Location: path, Source: source, SourceRef: src}})
	}
	// Some source warnings (for example listener overhead) have no diagnostic yet.
	for _, w := range r.Warnings {
		found := false
		for _, o := range outcomes {
			if o.Detail == w {
				found = true
				break
			}
		}
		if !found {
			outcomes = append(outcomes, ReviewOutcome{Finding: importreview.Finding{Code: "jmeter_warning:" + w, Severity: diag.Warning, Element: "JMeter source", Detail: w, Source: source, SourceRef: source}})
		}
	}
	r.SetReviewEvidence(outcomes)
}

// SetReviewEvidence also combines already source-qualified evidence from ZIP entries.
func (r *ConvertResult) SetReviewEvidence(input []ReviewOutcome) {
	byID := map[string]ReviewOutcome{}
	for _, o := range input {
		o.Code = canonicalReviewCode(o.Code)
		key := o.Source + "\x00" + o.SourceRef + "\x00" + o.Code
		// Execution gates always need a blocker diagnostic, regardless of the
		// severity attached by an earlier conversion stage.
		if o.Gate == importreview.GateExecution {
			o.Severity = diag.Blocker
		}
		if old, ok := byID[key]; ok {
			severityRank := map[diag.Severity]int{diag.Info: 0, diag.Warning: 1, diag.Blocker: 2}
			merged := old
			if severityRank[o.Severity] > severityRank[old.Severity] {
				merged = o
			}
			merged.Unsupported = old.Unsupported || o.Unsupported
			merged.AcknowledgementRequired = old.AcknowledgementRequired || o.AcknowledgementRequired
			if old.Gate == importreview.GateExecution || o.Gate == importreview.GateExecution {
				merged.Gate = importreview.GateExecution
				merged.Severity = diag.Blocker
			} else if old.Gate == importreview.GatePromotion || o.Gate == importreview.GatePromotion {
				merged.Gate = importreview.GatePromotion
			}
			if merged.Detail == "" {
				if old.Detail != "" {
					merged.Detail = old.Detail
				} else {
					merged.Detail = o.Detail
				}
			}
			o = merged
		}
		byID[key] = o
	}
	keys := make([]string, 0, len(byID))
	for key := range byID {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	r.ReviewOutcomes = make([]ReviewOutcome, 0, len(keys))
	r.Diagnostics = make(diag.Diagnostics, 0, len(keys))
	r.UnsupportedElements = []string{}
	r.ReviewSummary = &ReviewSummary{}
	for _, key := range keys {
		o := byID[key]
		r.ReviewOutcomes = append(r.ReviewOutcomes, o)
		d := diag.Diagnostic{Element: o.Element, Detail: o.Detail, Severity: o.Severity, Location: o.Location, Code: o.Code, SourceRef: o.SourceRef}
		r.Diagnostics = append(r.Diagnostics, d)
		switch {
		case o.Gate == importreview.GateExecution || o.Severity == diag.Blocker:
			r.ReviewSummary.BlockingBehaviors++
		case o.Gate == importreview.GatePromotion || o.AcknowledgementRequired:
			r.ReviewSummary.ReviewBehaviors++
		case o.Severity == diag.Warning:
			r.ReviewSummary.Warnings++
		default:
			r.ReviewSummary.Informational++
		}
		if o.Unsupported {
			r.ReviewSummary.UnsupportedBehaviors++
			r.UnsupportedElements = append(r.UnsupportedElements, d.String())
		}
	}
	r.preserveReviewEvidence()
}

// Preserve the resolved evidence with single-script drafts without modifying any gate.
func (r *ConvertResult) preserveReviewEvidence() {
	if len(r.Scripts) != 1 || r.Scripts[0].Metadata == nil || r.Scripts[0].Metadata.Import == nil {
		return
	}
	p := r.Scripts[0].Metadata.Import.Preserved
	if p == nil {
		p = map[string]any{}
		r.Scripts[0].Metadata.Import.Preserved = p
	}
	p["jmeter_review_summary"] = r.ReviewSummary
	p["jmeter_review_diagnostics"] = r.Diagnostics
}
