package api

import (
	"net/http"

	"github.com/jasonczech/apexload/pkg/convert/convertresult"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// importDraft is the converter-agnostic view of a freshly converted import.
//
// Every ingest handler builds one and calls augment instead of hand-attaching
// the cross-cutting concerns an import response is expected to carry. Before
// this existed each handler attached them itself, and the set it remembered
// drifted per format: source review reached 13 of 17 importers, converter
// diagnostics 9, and the MuleSoft/Agentforce detectors 6 — with two of those six
// discarding the sections they produced. Routing every format through one pass
// makes the coverage a property of the code rather than of what each handler
// remembered.
type importDraft struct {
	// Source is the import provenance key recorded on every produced script
	// ("openapi", "jmeter", ...). It also namespaces the preserved evidence.
	Source string
	// SourceScriptName names the artifact the draft was converted from.
	SourceScriptName string
	// ProjectID is normalized and applied to every produced script.
	ProjectID string
	// Script is the primary draft — the one whose preflight the response
	// reports. Scripts is the whole batch for formats that produce more than
	// one; a single-script format leaves it empty.
	Script  *dsl.Script
	Scripts []*dsl.Script
	// SourceReview is the converter's residual source-mapping review. It is
	// typed as any because each converter package declares its own review shape;
	// a nil or typed-nil value attaches nothing.
	SourceReview any
	// Unsupported carries converter diagnostics, rendered as review sections.
	Unsupported diag.Diagnostics
	// Sections are the format's own review sections. Shared sections append to
	// them, so a format's own findings stay first in the response.
	Sections []scriptImportReviewSection
	// Warnings are the converter's warnings. Validation issues are appended to
	// the augmented copy, never to this slice.
	Warnings []string
	// SuppressedMuleSoftPolicies drops named Anypoint policies from the MuleSoft
	// section. RAML sets it for client_id_enforcement because its own review
	// section already reports that policy from the converter's summary.
	SuppressedMuleSoftPolicies []string
	// Accounting is the converter's completeness report, in the terms every
	// importer shares. Converters that track no detected count leave it zero and
	// the response omits it rather than claiming nothing was found.
	Accounting convertresult.Accounting
}

// importAugmentation is what the shared pass produces for the response envelope.
type importAugmentation struct {
	// Sections is the format's own review sections plus every shared one.
	Sections []scriptImportReviewSection
	// Preflight is the primary draft's replay preflight. Each produced script
	// also carries its own, preserved for a later save.
	Preflight *ReplayPreflight
	// Warnings is the converter's warnings plus any validation issues.
	Warnings []string
	// Accounting is the draft's completeness report, passed through for the
	// response envelope.
	Accounting convertresult.Accounting
}

// augment applies every cross-cutting concern an import response carries and
// returns what the envelope needs. It mutates the draft's scripts in place:
// project scope, import provenance, preserved review evidence, and preflight all
// live on the script so a later save keeps them.
func (d *importDraft) augment(r *http.Request) importAugmentation {
	return d.augmentWithAgentforceTransform(agentforceTransformRequested(r))
}

// augmentWithAgentforceTransform is augment for callers that resolved the
// Agentforce transform opt-in earlier — Postman decides it before conversion,
// because the same flag also drives its native collection handling.
func (d *importDraft) augmentWithAgentforceTransform(agentforceTransform bool) importAugmentation {
	scripts := d.allScripts()
	projectID := normalizeProjectID(d.ProjectID)
	for _, script := range scripts {
		script.ProjectID = projectID
		ensureImportProvenance(script, d.Source, d.SourceScriptName)
	}
	attachSourceReviewToScripts(scripts, d.Source, d.SourceScriptName, d.SourceReview)

	sections := appendImportDiagnosticReviewSections(d.Sections, d.Unsupported)

	// The protocol detectors report on the converted traffic as a whole, so they
	// run against the primary draft; a batch's scripts share one source artifact.
	primary := d.primary()
	sections = attachMuleSoftReview(primary, d.Source, sections, d.SuppressedMuleSoftPolicies...)
	// Agentforce transformation rewrites executable actions and can introduce
	// governed secret references. It must run before validation and preflight so
	// those describe the draft the operator will actually save, not the
	// pre-transform one.
	nativeSalesforceReview := trustedNativeSalesforceReview(primary)
	sections = attachAgentforceReview(primary, d.Source, sections, agentforceTransform)
	finalizeSalesforceNativeReferences(nativeSalesforceReview, primary, d.SourceReview)

	augmented := importAugmentation{Sections: sections, Warnings: d.Warnings, Accounting: d.Accounting}

	// Each script is validated on its own. Attaching one batch-wide issue list
	// to every script would mark a clean script unreplayable because a sibling
	// was not, which is what the shared multi-script helper used to do.
	//
	// The preflight is built from the converter's warnings alone. Validation
	// issues travel in the preflight's own Issues field, so folding them into
	// its warnings as well would double-count them in the validation check the
	// review panel renders.
	var allIssues []string
	for _, script := range scripts {
		issues := validateScript(script)
		allIssues = appendUniqueStrings(allIssues, issues...)
		preflight := buildReplayPreflight(script, nil, issues, d.Warnings)
		attachImportReplayPreflight(script, d.Source, preflight)
		if script == primary {
			augmented.Preflight = preflight
		}
	}
	augmented.Warnings = appendUniqueStrings(append([]string(nil), d.Warnings...), allIssues...)
	return augmented
}

// allScripts is the batch this draft produced, primary first and deduplicated.
// A single-script format supplies only Script; a batch format supplies Scripts
// and usually repeats its primary there.
func (d *importDraft) allScripts() []*dsl.Script {
	scripts := make([]*dsl.Script, 0, len(d.Scripts)+1)
	seen := make(map[*dsl.Script]struct{}, len(d.Scripts)+1)
	add := func(script *dsl.Script) {
		if script == nil {
			return
		}
		if _, done := seen[script]; done {
			return
		}
		seen[script] = struct{}{}
		scripts = append(scripts, script)
	}
	add(d.Script)
	for _, script := range d.Scripts {
		add(script)
	}
	return scripts
}

// primary is the draft whose preflight the response reports.
func (d *importDraft) primary() *dsl.Script {
	if d.Script != nil {
		return d.Script
	}
	for _, script := range d.Scripts {
		if script != nil {
			return script
		}
	}
	return nil
}

// ensureImportProvenance records where a draft came from. attachSourceReview
// only writes provenance alongside a review, so a converter that produces no
// source review would otherwise leave the draft unattributable at save time.
func ensureImportProvenance(script *dsl.Script, source string, sourceScriptName string) {
	if script == nil {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Source == "" {
		script.Metadata.Import.Source = source
	}
	if script.Metadata.Import.SourceScriptName == "" {
		script.Metadata.Import.SourceScriptName = sourceScriptName
	}
}
