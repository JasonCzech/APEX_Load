package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// PrepareResponseCredentials attaches converter and referenced-value descriptors
// before a host fingerprints drafts. The returned cleanup releases temporary
// source values after the response owner has finished its optional handoff.
func PrepareResponseCredentials(opts *ScriptImportResponseOptions) ([]SecretBindingRequirement, func()) {
	scripts := UniqueImportDraftScripts(ImportDraftScriptPointers(opts.Script, opts.Scripts))
	requirements := MergeImportCredentialRequirementSets(opts.ProtectedRequirements, opts.CredentialRequirements)
	toClear := [][]credentialpolicy.Requirement{opts.ProtectedRequirements, requirements}
	review := append([]SecretBindingRequirement(nil), opts.PreparedCredentialReview...)
	if !opts.CredentialReviewPrepared {
		requirements = MergeReferencedImportCredentialRequirements(scripts, requirements)
		toClear = append(toClear, requirements)
	}
	if !opts.CredentialReviewPrepared && len(requirements) > 0 {
		if len(scripts) == 0 {
			opts.Warnings = append(opts.Warnings, "Protected credential bindings could not be prepared; the generated secret references remain available for manual configuration.")
		} else {
			var err error
			review, err = AttachImportCredentialReview(scripts, requirements)
			if err != nil {
				opts.Warnings = append(opts.Warnings, "Protected credential bindings could not be prepared; the generated secret references remain available for manual configuration.")
			}
		}
	}
	return review, func() {
		for _, items := range toClear {
			ClearCredentialRequirementValues(items)
		}
	}
}
func ClearCredentialRequirementValues(requirements []credentialpolicy.Requirement) {
	for i := range requirements {
		requirements[i].Value = ""
	}
}
func UniqueImportDraftScripts(scripts []*dsl.Script) []*dsl.Script {
	result := make([]*dsl.Script, 0, len(scripts))
	seen := make(map[*dsl.Script]struct{}, len(scripts))
	for _, script := range scripts {
		if script == nil {
			continue
		}
		if _, ok := seen[script]; ok {
			continue
		}
		seen[script] = struct{}{}
		result = append(result, script)
	}
	return result
}
