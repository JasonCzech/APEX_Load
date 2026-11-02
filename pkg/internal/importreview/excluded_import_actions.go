package importreview

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// RefreshProtectedImportCredentialLocations re-derives the JSON-pointer
// locations of every protected credential descriptor on script after an edit
// that moved or removed actions. Locations are positional ("/actions/9/..."),
// so a removal ahead of a credential shifts it; each descriptor is matched
// again by its replacement reference, the way AttachImportCredentialReview
// found it at import. A descriptor whose reference no longer occurs (its only
// request was removed) is dropped. AffectedPaths keeps its source and request
// entries and swaps the old locations for the new ones.
func RefreshProtectedImportCredentialLocations(script *dsl.Script) error {
	descriptors, err := LoadProtectedImportCredentialDescriptors(script)
	if err != nil || len(descriptors) == 0 {
		return err
	}
	raw, err := ScriptAsJSONValue(script)
	if err != nil {
		return err
	}
	kept := make([]ProtectedImportCredentialDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		locations := FindProtectedImportCredentialLocations(raw, strings.TrimSpace(descriptor.Replacement))
		if len(locations) == 0 {
			continue
		}
		stale := make(map[string]struct{}, len(descriptor.Locations))
		for _, location := range descriptor.Locations {
			stale[strings.TrimSpace(location.Path)] = struct{}{}
		}
		paths := make([]string, 0, len(descriptor.AffectedPaths))
		for _, path := range descriptor.AffectedPaths {
			if _, positional := stale[strings.TrimSpace(path)]; !positional {
				paths = append(paths, path)
			}
		}
		for _, location := range locations {
			paths = append(paths, location.Path)
		}
		descriptor.Locations = locations
		descriptor.AffectedPaths = UniqueSortedStrings(paths)
		kept = append(kept, descriptor)
	}
	SetProtectedImportCredentialDescriptors(script, kept)
	return nil
}

// EditedImportDraftSecretBindingRequirements is the secret_binding_requirements
// list an import review shows for script after a server-side draft edit. It
// discovers requirements the way the import response does: from the masked
// disclosure projection, since discovery only recognizes masked sentinels and
// a credential-preserving draft holds its retained credentials as literals.
// For such a draft the requirements also report whether the exact draft still
// holds each source value; any other draft is passed already masked. The draft's protected credential descriptors are
// merged in, so the list replaces the review's previous one whole.
func EditedImportDraftSecretBindingRequirements(script *dsl.Script, credentialPreserving bool) []SecretBindingRequirement {
	if script == nil {
		return []SecretBindingRequirement{}
	}
	var requirements []SecretBindingRequirement
	if credentialPreserving {
		exact := ImportResponseRawScriptValue(script)
		projected := CredentialPreservingImportResponseValueFromExact(script, exact, nil)
		requirements = SecretBindingRequirementsForImportScripts([]any{projected})
		requirements = AnnotateSecretBindingDestinationValues(requirements, []any{exact}, []any{projected})
	} else {
		// Any other draft is already the masked projection.
		requirements = DiscoverSecretBindingRequirements(script, 0)
	}
	if descriptors, err := LoadProtectedImportCredentialDescriptors(script); err == nil && len(descriptors) > 0 {
		response := map[string]any{"secret_binding_requirements": requirements}
		MergeProtectedImportCredentialReview(response, PublicProtectedImportCredentialDescriptors(script, descriptors, false))
		requirements, _ = response["secret_binding_requirements"].([]SecretBindingRequirement)
	}
	if requirements == nil {
		requirements = []SecretBindingRequirement{}
	}
	return requirements
}
