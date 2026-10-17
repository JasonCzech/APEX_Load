package importreview

import (
	"errors"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Script is the read-only projection computed during fingerprinting.
func (p ImportDraftProjection) Script() *dsl.Script { return p.script }
func (e *StaleSecretBindingRequirementsError) Requirements() []SecretBindingRequirement {
	return e.requirements
}
func (e *SecretBindingRebindUnavailableError) Requirements() []SecretBindingRequirement {
	return e.requirements
}
func (e *SecretBindingRebindUnavailableError) Requirement() SecretBindingRequirement {
	return e.requirement
}
func (e *ScriptReplayBlockedError) Issues() []string { return e.issues }

func ReplayBlockedIssues(err error) []string {
	var blocked *ScriptReplayBlockedError
	if errors.As(err, &blocked) {
		return append([]string(nil), blocked.issues...)
	}
	return nil
}

// ScriptProjectionContextFor selects the existing contextual projection policy.
func ScriptProjectionContextFor(key string, metadata, opaqueMap bool) ScriptProjectionContext {
	return ScriptProjectionContext{key: key, metadata: metadata, opaqueMap: opaqueMap}
}

func NewImportDraftProjection(script *dsl.Script) ImportDraftProjection {
	return ImportDraftProjection{script: script}
}
