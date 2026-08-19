package api

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
func buildImportSharedValues(scripts []*dsl.Script) *sharedvalue.Result {
	result := sharedvalue.Detect(scripts)
	return &result
}

// trustedSharedValueCandidates re-derives the candidates a caller is allowed to
// act on. Only IDs and operator-entered values cross the trust boundary; the
// candidate bodies a browser sends are descriptors, never mutation
// instructions. Credential-shaped candidates are dropped entirely here — they
// are handled by the guided credential-binding workflow.
func trustedSharedValueCandidates(scripts []*dsl.Script) []sharedvalue.Candidate {
	result := sharedvalue.Detect(scripts)
	trusted := make([]sharedvalue.Candidate, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		if candidate.CredentialShaped {
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
