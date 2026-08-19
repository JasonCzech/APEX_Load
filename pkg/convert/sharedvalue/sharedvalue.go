// Package sharedvalue provides a cross-script parameterization pass for
// collection imports. Where pkg/convert/paramize analyzes one script at a time
// and proposes per-VUser test data, this pass answers a different question:
// which single value does the operator need to supply once for the *whole*
// collection?
//
// A Bruno collection generates one script per request, a JMeter plan one per
// thread group, and an AsyncAPI document one per channel. A base URL, tenant
// id, or API version shared by every request is therefore duplicated across
// every generated script, and an unresolved source variable blocks all of them
// at once. Detect groups those occurrences into one Candidate; Apply writes the
// operator's value into every affected script.
//
// Detect never mutates the scripts. Apply is a separate, human-approved step.
package sharedvalue

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type Kind string

const (
	// KindExistingParameter is a single-value list parameter a converter
	// already minted (Postman/RAML collection variables, Bruno native
	// per-request variables). The operator edits its value rather than
	// creating anything new.
	KindExistingParameter Kind = "existing_parameter"
	// KindSourceVariable is a {{var.X}} reference the source collection never
	// supplied a value for. Today it becomes an execution blocker; supplying a
	// value here clears it.
	KindSourceVariable Kind = "source_variable"
	// KindRepeatedLiteral is a literal hardcoded in two or more requests across
	// the import (the same host, tenant, or API version in every URL).
	KindRepeatedLiteral Kind = "repeated_literal"
)

// Field names used by Location. Kept as constants because Apply dispatches on
// them and a typo would silently skip a rewrite.
const (
	FieldTarget  = "target"
	FieldBody    = "body"
	FieldValue   = "value"
	FieldBaseURL = "config.base_url"
	// FieldHeaderPrefix is followed by the header name, e.g. "headers.X-Tenant".
	FieldHeaderPrefix = "headers."
	// FieldParamPrefix is followed by the action param key, e.g. "params.key".
	FieldParamPrefix = "params."
)

// Location pins one occurrence of a shared value. Path is the deterministic
// dslwalk surface path ("actions[3]", "actions[1].control.steps[0]", ...), which
// is why nested control-flow and http_sequence children are addressable here
// even though paramize.Location's section/index pair cannot reach them.
//
// ScriptIndex, not the script name, identifies the script: names are not unique
// (two collection folders can hold a request of the same name), and matching on
// one would apply a location found in one script to an unrelated namesake.
type Location struct {
	ScriptIndex int    `json:"script_index"`
	Path        string `json:"path"`
	Field       string `json:"field"`
	// SearchText carries enough surrounding context to make the replacement
	// unambiguous within the field (e.g. `tenant=acme&`). Empty for
	// FieldBaseURL and for whole-field replacements.
	SearchText  string `json:"search_text,omitempty"`
	ReplaceText string `json:"replace_text,omitempty"`
}

// Candidate is one value the operator can supply once for the whole import.
type Candidate struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// Name is the DSL parameter name the value will be bound to. For
	// KindExistingParameter it is the parameter that already exists.
	Name string `json:"name"`
	// SourceVariable is the reference as it appeared in the source collection,
	// before name normalization. Empty for KindRepeatedLiteral.
	SourceVariable string `json:"source_variable,omitempty"`
	// CurrentValue is the value the operator will edit: the constant a converter
	// already produced, or the literal a repeated occurrence is using today. It
	// is the only value this type carries — a separate truncated "sample" would
	// be a second copy of the same literal on the wire for no added meaning, and
	// import responses are asserted to carry no sample values at all.
	// Always empty for a credential-shaped candidate.
	CurrentValue string `json:"current_value,omitempty"`
	// LiteralValue is the exact text Apply searches for. Never serialized to a
	// client: it is server-derived state, and echoing it back would let a
	// caller smuggle a replacement instruction.
	LiteralValue string `json:"-"`
	// OccurrenceCount counts locations. ScriptIndexes identifies the affected
	// scripts by position in the slice Detect was given — the authoritative
	// target list Apply walks. ScriptNames is its display parallel, one entry
	// per index, so two same-named scripts still read as two.
	OccurrenceCount int      `json:"occurrence_count"`
	ScriptIndexes   []int    `json:"-"`
	ScriptNames     []string `json:"script_names,omitempty"`
	// CredentialShaped marks a candidate that must not become a parameter.
	// See the package-level rationale in Apply.
	CredentialShaped bool   `json:"credential_shaped,omitempty"`
	Rationale        string `json:"rationale,omitempty"`
	// Locations is server-side apply state and is never serialized. The browser
	// sends back only a candidate ID and the operator's value; the server
	// re-derives what that ID means from the scripts it holds. Putting the
	// locations on the wire would add nothing the review UI uses (it shows
	// occurrence_count and script_names) while carrying literal request text
	// out of the server.
	Locations []Location `json:"-"`
	// TargetsBaseURL is true when any location writes config.base_url, which
	// takes a literal rather than a template. See Apply.
	TargetsBaseURL bool `json:"targets_base_url,omitempty"`
}

type Summary struct {
	Total            int          `json:"total"`
	ByKind           map[Kind]int `json:"by_kind"`
	CredentialCount  int          `json:"credential_count"`
	MultiScriptCount int          `json:"multi_script_count"`
}

type Result struct {
	Candidates []Candidate `json:"candidates"`
	Summary    Summary     `json:"summary"`
	Warnings   []string    `json:"warnings,omitempty"`
}

// Diff records one applied change for the review UI. Before/After are blanked
// at the API boundary, matching paramize.Diff handling.
type Diff struct {
	CandidateID string `json:"candidate_id"`
	ScriptName  string `json:"script_name,omitempty"`
	Path        string `json:"path"`
	Before      string `json:"before,omitempty"`
	After       string `json:"after,omitempty"`
}

// buildSummary is the single place summary counts are derived so Detect and the
// API-side re-derivation cannot disagree.
func buildSummary(candidates []Candidate) Summary {
	summary := Summary{Total: len(candidates), ByKind: map[Kind]int{}}
	for _, candidate := range candidates {
		summary.ByKind[candidate.Kind]++
		if candidate.CredentialShaped {
			summary.CredentialCount++
		}
		if len(candidate.ScriptNames) > 1 {
			summary.MultiScriptCount++
		}
	}
	return summary
}

// candidateID is a deterministic descriptor over the candidate's *structure*
// only. Literal and sample values are deliberately excluded so an ID can never
// become a credential oracle, mirroring paramize.suggestionID.
func candidateID(candidate Candidate) string {
	var builder strings.Builder
	builder.WriteString(string(candidate.Kind))
	builder.WriteByte('|')
	builder.WriteString(candidate.Name)
	builder.WriteByte('|')
	builder.WriteString(candidate.SourceVariable)
	for _, location := range candidate.Locations {
		builder.WriteByte('|')
		builder.WriteString(strconv.Itoa(location.ScriptIndex))
		builder.WriteByte(':')
		builder.WriteString(location.Path)
		builder.WriteByte(':')
		builder.WriteString(location.Field)
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return fmt.Sprintf("sv_%x", sum[:8])
}

// sortCandidates gives a stable review order: the most widely shared values
// first, then by kind and name so equal-reach rows do not shuffle between runs.
func sortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if len(left.ScriptNames) != len(right.ScriptNames) {
			return len(left.ScriptNames) > len(right.ScriptNames)
		}
		if left.OccurrenceCount != right.OccurrenceCount {
			return left.OccurrenceCount > right.OccurrenceCount
		}
		if left.Kind != right.Kind {
			return kindRank(left.Kind) < kindRank(right.Kind)
		}
		return left.Name < right.Name
	})
}

func kindRank(kind Kind) int {
	switch kind {
	case KindExistingParameter:
		return 0
	case KindSourceVariable:
		return 1
	case KindRepeatedLiteral:
		return 2
	default:
		return 3
	}
}

func scriptName(script *dsl.Script) string {
	if script == nil {
		return ""
	}
	return script.Name
}

// scriptAt bounds-checks an index recorded at detection time against the slice
// handed to Apply. A caller that reorders or shortens the slice between the two
// calls gets no mutation rather than a mutation of the wrong script.
func scriptAt(scripts []*dsl.Script, index int) *dsl.Script {
	if index < 0 || index >= len(scripts) {
		return nil
	}
	return scripts[index]
}
