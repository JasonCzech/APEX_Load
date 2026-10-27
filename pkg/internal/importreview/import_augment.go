package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/convertresult"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	"net/http"
)

type ImportDraft struct {
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
	Sections []ScriptImportReviewSection
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
	// BeforeFinalize runs Stage 3 value transformations (UUIDs, timestamps,
	// parameterization, secret bindings, effective_settings attachment) after
	// Stage 2 domain/protocol augmentation (Salesforce, MuleSoft, Agentforce)
	// and before Stage 4 reference finalization, validation, and preflight.
	BeforeFinalize func(primary *dsl.Script, warnings *[]string, unsupported *diag.Diagnostics) error
}

type ImportAugmentation struct {
	// Sections is the format's own review sections plus every shared one.
	Sections []ScriptImportReviewSection
	// Preflight is the primary draft's replay preflight. Each produced script
	// also carries its own, preserved for a later save.
	Preflight *ReplayPreflight
	// Warnings is the converter's warnings plus any validation issues.
	Warnings []string
	// Accounting is the draft's completeness report, passed through for the
	// response envelope.
	Accounting convertresult.Accounting
}

func (d *ImportDraft) Augment(r *http.Request) ImportAugmentation {
	return d.AugmentWithAgentforceTransform(AgentforceTransformRequested(r))
}

func (d *ImportDraft) AugmentWithAgentforceTransform(agentforceTransform bool) ImportAugmentation {
	augmented, _ := d.AugmentWithAgentforceTransformChecked(agentforceTransform)
	return augmented
}

func (d *ImportDraft) AugmentWithAgentforceTransformChecked(agentforceTransform bool) (ImportAugmentation, error) {
	scripts := d.AllScripts()
	projectID := NormalizeProjectID(d.ProjectID)
	for _, script := range scripts {
		script.ProjectID = projectID
		EnsureImportProvenance(script, d.Source, d.SourceScriptName)
	}
	if d.Source != "recorder" {
		StampFileImportSourceIdentity(d.SourceScriptName, scripts)
	}
	AttachSourceReviewToScripts(scripts, d.Source, d.SourceScriptName, d.SourceReview)

	// The protocol detectors report on the converted traffic as a whole, so they
	// run against the primary draft; a batch's scripts share one source artifact.
	Primary := d.Primary()
	var protocolSections []ScriptImportReviewSection
	protocolSections = AttachMuleSoftReview(Primary, d.Source, protocolSections, d.SuppressedMuleSoftPolicies...)
	// Agentforce transformation rewrites executable actions and can introduce
	// governed secret references. It must run before validation and preflight so
	// those describe the draft the operator will actually save, not the
	// pre-transform one.
	nativeSalesforceReview := TrustedNativeSalesforceReview(Primary)
	protocolSections = AttachAgentforceReview(Primary, d.Source, protocolSections, agentforceTransform)
	if d.BeforeFinalize != nil && Primary != nil {
		if err := d.BeforeFinalize(Primary, &d.Warnings, &d.Unsupported); err != nil {
			return ImportAugmentation{}, err
		}
		scripts = d.AllScripts()
		Primary = d.Primary()
	}
	sections := AppendImportDiagnosticReviewSections(d.Sections, d.Unsupported)
	sections = append(sections, protocolSections...)
	FinalizeSalesforceNativeReferences(nativeSalesforceReview, Primary, d.SourceReview)

	augmented := ImportAugmentation{Sections: sections, Warnings: d.Warnings, Accounting: d.Accounting}

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
		issues := ValidateScript(script)
		allIssues = AppendUniqueStrings(allIssues, issues...)
		preflight := BuildReplayPreflight(script, nil, issues, d.Warnings)
		AttachImportReplayPreflight(script, d.Source, preflight)
		if script == Primary {
			augmented.Preflight = preflight
		}
	}
	augmented.Warnings = AppendUniqueStrings(append([]string(nil), d.Warnings...), allIssues...)
	return augmented, nil
}

func (d *ImportDraft) AllScripts() []*dsl.Script {
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

func (d *ImportDraft) Primary() *dsl.Script {
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

func EnsureImportProvenance(script *dsl.Script, source string, SourceScriptName string) {
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
		script.Metadata.Import.SourceScriptName = SourceScriptName
	}
}
