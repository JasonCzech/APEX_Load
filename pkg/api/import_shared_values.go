package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"github.com/jasonczech/apexload/pkg/convert/sharedvalue"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// buildImportSharedValues is the shared hook that gives every importer the
// collection-wide value panel, in the same way buildImportParameterization
// gives every importer the parameterization panel. It is deliberately derived
// from whatever scripts an import produced rather than from converter-specific
// state, so a new importer needs no extra wiring.
//
// It is always fed the disclosure-safe projection of the scripts. That makes the
// candidate IDs the browser receives identical to the ones the apply endpoint
// re-derives, and guarantees no masked credential can be echoed back as a
// pre-filled value.
var buildImportSharedValues = importreview.BuildImportSharedValues

// trustedSharedValueCandidates re-derives the candidates a caller is allowed to
// act on. Only IDs and operator-entered values cross the trust boundary; the
// candidate bodies a browser sends are descriptors, never mutation
// instructions. Credential-shaped candidates are dropped here — the guided
// credential-binding workflow handles them — except an input the source left
// unset, which a binding supplies from a project secret or data pool column
// (Binding.ValidateFor) and which Apply refuses a value for.
func trustedSharedValueCandidates(scripts []*dsl.Script) []sharedvalue.Candidate {
	result := sharedvalue.Detect(scripts)
	trusted := make([]sharedvalue.Candidate, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		// A credential-shaped input the source left unset stays, for a data
		// pool column or a project secret; Apply refuses a value for it.
		if candidate.CredentialShaped && candidate.Kind != sharedvalue.KindSourceVariable {
			continue
		}
		trusted = append(trusted, candidate)
	}
	return trusted
}

// sanitizeSharedValueDiffs blanks the before/after text before it leaves the
// server, mirroring sanitizeParameterizationDiffs. The review UI needs to know
// which locations changed, not what they contained.
func sanitizeSharedValueDiffs(diffs []sharedvalue.Diff) []sharedvalue.Diff {
	sanitized := make([]sharedvalue.Diff, 0, len(diffs))
	for _, diff := range diffs {
		diff.Before = ""
		diff.After = ""
		sanitized = append(sanitized, diff)
	}
	return sanitized
}

// rebaseBrunoSharedValues preserves inspection identities while restricting
// all mutations to the selected script positions. Neither locations nor index
// mappings come from client-supplied descriptors. Valid edits for deselected
// scripts remain no-ops, so returning to the selection does not lose work.
func rebaseBrunoSharedValues(candidates []sharedvalue.Candidate, selected map[int]int) []sharedvalue.Candidate {
	result := make([]sharedvalue.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.ScriptIndexes = append([]int(nil), candidate.ScriptIndexes...)
		candidate.Locations = append([]sharedvalue.Location(nil), candidate.Locations...)
		indexes := candidate.ScriptIndexes[:0]
		for _, index := range candidate.ScriptIndexes {
			if target, ok := selected[index]; ok {
				indexes = append(indexes, target)
			}
		}
		candidate.ScriptIndexes = indexes
		locations := candidate.Locations[:0]
		for _, location := range candidate.Locations {
			if target, ok := selected[location.ScriptIndex]; ok {
				location.ScriptIndex = target
				locations = append(locations, location)
			}
		}
		candidate.Locations = locations
		result = append(result, candidate)
	}
	return result
}
