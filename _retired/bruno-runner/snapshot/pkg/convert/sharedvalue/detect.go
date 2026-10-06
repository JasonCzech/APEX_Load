package sharedvalue

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Work limits. An import is operator-facing: a collection large enough to blow
// through these is better served by an honest warning than by a partial list
// the operator would mistake for a complete one.
const (
	maxScannedFields   = 200_000
	maxTrackedLiterals = 4_096
	maxCandidates      = 200
	// A shared value is something an operator can type. Anything longer is a
	// payload, not a setting.
	maxSharedValueBytes = 512
)

// minSharedOccurrences is the reach a repeated literal must have before it is
// worth the operator's attention. One occurrence is not a collection-wide value.
const minSharedOccurrences = 2

// Detect groups values that the whole import shares into candidates the
// operator supplies once. It never mutates the scripts.
//
// Pre-filled values come only from the scripts themselves — the constant
// parameters the converters already minted from the source environment, and
// config.base_url. Detect deliberately has no channel for raw source-environment
// values: Bruno keeps those sealed inside the import artifact, and surfacing
// them here would build a disclosure path around that.
func Detect(scripts []*dsl.Script) Result {
	result := Result{Candidates: []Candidate{}}
	if len(scripts) == 0 {
		result.Summary = buildSummary(result.Candidates)
		return result
	}

	builder := newCandidateBuilder()
	builder.collectExistingParameters(scripts)
	builder.collectSourceVariables(scripts)
	if err := builder.collectRepeatedLiterals(scripts); err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("Repeated-value analysis was not completed and no repeated-value candidates were produced: %v", err))
	}
	result.Warnings = append(result.Warnings, builder.warnings...)

	candidates := builder.candidates()
	if len(candidates) > maxCandidates {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"Only the %d most widely shared values are listed; this import produced %d.", maxCandidates, len(candidates)))
		candidates = candidates[:maxCandidates]
	}
	result.Candidates = candidates
	result.Summary = buildSummary(candidates)
	return result
}

type candidateBuilder struct {
	// byName keys on the lowercased parameter name so a value already bound in
	// one script and merely hardcoded in another lands on one row.
	byName   map[string]*Candidate
	order    []string
	warnings []string
}

func newCandidateBuilder() *candidateBuilder {
	return &candidateBuilder{byName: map[string]*Candidate{}}
}

func (b *candidateBuilder) get(name string) *Candidate {
	key := strings.ToLower(name)
	if existing, ok := b.byName[key]; ok {
		return existing
	}
	candidate := &Candidate{Name: name, CredentialShaped: CredentialShaped(name)}
	b.byName[key] = candidate
	b.order = append(b.order, key)
	return candidate
}

func (b *candidateBuilder) has(name string) bool {
	_, ok := b.byName[strings.ToLower(name)]
	return ok
}

// collectExistingParameters is the cheapest and highest-value pass: every
// request-collection converter already emits a single-value list parameter per
// source variable, so the values are sitting in the scripts waiting to be
// edited. Bruno native duplicates them across one script per request, which is
// exactly the fan-out this feature exists to collapse.
func (b *candidateBuilder) collectExistingParameters(scripts []*dsl.Script) {
	for scriptIndex, script := range scripts {
		if script == nil {
			continue
		}
		for _, param := range script.Parameters {
			name := strings.TrimSpace(param.Name)
			if name == "" || !ValidName(name) {
				continue
			}
			binding, ok := constantParameterBinding(script, param)
			if !ok {
				continue
			}
			value := binding.value
			if len(value) > maxSharedValueBytes || isRedacted(value) {
				continue
			}
			candidate := b.get(name)
			candidate.Kind = KindExistingParameter
			candidate.SourceVariable = name
			candidate.Rationale = "Defined once per script by the importer; edit it here to change every script at once."
			if candidate.CurrentValue == "" {
				candidate.CurrentValue = value
			} else if candidate.CurrentValue != value {
				// Distinct values behind one name are not a shared value. Say so
				// rather than silently flattening the operator's data.
				candidate.Rationale = "Scripts currently disagree on this value; entering one here overwrites all of them."
			}
			candidate.addScript(scriptIndex, scriptName(script))
			candidate.OccurrenceCount++
		}
	}
}

// collectSourceVariables surfaces references the source collection never
// resolved. Today these only become an execution blocker; supplying a value
// here is what clears it.
func (b *candidateBuilder) collectSourceVariables(scripts []*dsl.Script) {
	for scriptIndex, script := range scripts {
		if script == nil {
			continue
		}
		for _, missing := range requestimport.MissingVariables(script, nil) {
			name := NormalizeName(missing)
			if !ValidName(name) {
				continue
			}
			candidate := b.get(name)
			// An existing parameter already carries a value; do not demote it.
			if candidate.Kind == KindExistingParameter {
				candidate.addScript(scriptIndex, scriptName(script))
				continue
			}
			candidate.Kind = KindSourceVariable
			candidate.SourceVariable = missing
			candidate.Rationale = "Referenced by the collection but never supplied a value; requests cannot run until it is set."
			candidate.addScript(scriptIndex, scriptName(script))
			candidate.OccurrenceCount++
		}
	}
}

// collectRepeatedLiterals finds values hardcoded across the import. It is
// deliberately narrow — two classes with a low false-positive rate — because a
// noisy list is worse than a short one the operator can trust.
func (b *candidateBuilder) collectRepeatedLiterals(scripts []*dsl.Script) error {
	origins := newLiteralIndex()
	headers := newLiteralIndex()
	scanned := 0
	var walkErr error

	for scriptIndex, script := range scripts {
		if script == nil {
			continue
		}
		name := scriptName(script)
		err := walkFields(script, func(entry field) bool {
			scanned++
			if scanned > maxScannedFields {
				walkErr = fmt.Errorf("import exceeds the %d-field scan budget", maxScannedFields)
				return false
			}
			switch {
			case entry.name == FieldTarget || entry.name == FieldBaseURL:
				if origin := absoluteOrigin(entry.value); origin != "" {
					origins.add(origin, origin, scriptIndex, name, entry)
				}
			case strings.HasPrefix(entry.name, FieldHeaderPrefix):
				headerName := strings.TrimPrefix(entry.name, FieldHeaderPrefix)
				if sharedHeaderCandidate(headerName, entry.value) {
					// Key on the header name as well as the value. Two different
					// headers that happen to carry the same string are two
					// settings, not one: merging them would name the candidate
					// after whichever was walked last, let two unrelated
					// single-occurrence headers clear the shared-reach bar, and
					// rewrite one header to the other's parameter.
					key := strings.ToLower(headerName) + "\x00" + entry.value
					headers.add(key, entry.value, scriptIndex, name, entry)
				}
			}
			return true
		})
		if err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
	}
	if origins.dropped > 0 || headers.dropped > 0 {
		// The scan budget reports itself; this cap must too, or a partial list
		// reads as a complete one.
		b.warnings = append(b.warnings, fmt.Sprintf(
			"Repeated-value analysis stopped tracking new values after %d distinct ones; some shared values may be missing from this list.",
			maxTrackedLiterals))
	}

	b.promote(origins, "base_url", "The same host is hardcoded in every request; entering it once repoints the whole collection.")
	b.promoteByHeader(headers)
	return nil
}

func (b *candidateBuilder) promote(index *literalIndex, name, rationale string) {
	for _, key := range index.ranked() {
		entry := index.entries[key]
		// config.base_url is definitionally the collection's base address, so it
		// is worth offering even in a single-script import. Anything else has to
		// prove it is shared.
		if entry.occurrences < minSharedOccurrences && !entry.targetsBaseURL {
			continue
		}
		unique := b.uniqueName(name)
		candidate := b.get(unique)
		if candidate.Kind != "" {
			continue
		}
		candidate.Kind = KindRepeatedLiteral
		candidate.Rationale = rationale
		candidate.LiteralValue = entry.literal
		candidate.CurrentValue = entry.literal
		candidate.OccurrenceCount = entry.occurrences
		for _, affected := range entry.scriptOrder {
			candidate.addScript(affected.index, affected.name)
		}
		candidate.Locations = entry.locations
		candidate.TargetsBaseURL = entry.targetsBaseURL
		// One origin candidate is enough; a second host is a different service,
		// not a shared setting the operator entered once.
		return
	}
}

func (b *candidateBuilder) promoteByHeader(index *literalIndex) {
	for _, key := range index.ranked() {
		entry := index.entries[key]
		if entry.occurrences < minSharedOccurrences || entry.headerName == "" {
			continue
		}
		name := b.uniqueName(NormalizeName(entry.headerName))
		if !ValidName(name) {
			continue
		}
		candidate := b.get(name)
		if candidate.Kind != "" {
			continue
		}
		candidate.Kind = KindRepeatedLiteral
		candidate.Rationale = fmt.Sprintf("The %s header carries the same value in every request.", entry.headerName)
		candidate.LiteralValue = entry.literal
		candidate.CurrentValue = entry.literal
		candidate.OccurrenceCount = entry.occurrences
		for _, affected := range entry.scriptOrder {
			candidate.addScript(affected.index, affected.name)
		}
		candidate.Locations = entry.locations
	}
}

// uniqueName avoids colliding with a parameter that already exists under a
// different meaning. pkg/dsl rejects duplicate names case-insensitively, so a
// silent overwrite here would either fail validation or destroy unrelated data.
func (b *candidateBuilder) uniqueName(base string) string {
	if !b.has(base) {
		return base
	}
	for suffix := 2; suffix < 100; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if !b.has(candidate) {
			return candidate
		}
	}
	return base
}

func (b *candidateBuilder) candidates() []Candidate {
	out := make([]Candidate, 0, len(b.order))
	for _, key := range b.order {
		candidate := b.byName[key]
		if candidate.Kind == "" {
			continue
		}
		if candidate.CredentialShaped {
			// Never let a credential-shaped value leave this pass.
			candidate.CurrentValue = ""
			candidate.Rationale = "Credential-shaped; set this once in Secret Bindings rather than as a script parameter."
		}
		out = append(out, *candidate)
	}
	sortCandidates(out)
	for i := range out {
		out[i].ID = candidateID(out[i])
	}
	return out
}

// addScript records one affected script. Dedupe is on the index, not the name:
// two scripts may legitimately share a name, and collapsing them would both
// understate the reach shown to the operator and hide one from Apply.
func (c *Candidate) addScript(index int, name string) {
	for _, existing := range c.ScriptIndexes {
		if existing == index {
			return
		}
	}
	c.ScriptIndexes = append(c.ScriptIndexes, index)
	c.ScriptNames = append(c.ScriptNames, name)
}

// affectedScript pairs a script's position with its name so a candidate can
// report a display name while Apply targets an exact slice position.
type affectedScript struct {
	index int
	name  string
}

type literalEntry struct {
	// literal is the value to search for; the map key may carry more (the
	// header name) to keep distinct settings apart.
	literal        string
	occurrences    int
	seen           map[string]bool
	scriptOrder    []affectedScript
	locations      []Location
	headerName     string
	targetsBaseURL bool
}

type literalIndex struct {
	entries map[string]*literalEntry
	order   []string
	// dropped counts literals refused once the tracking cap was reached, so the
	// caller can tell the operator the list is partial.
	dropped int
}

func newLiteralIndex() *literalIndex {
	return &literalIndex{entries: map[string]*literalEntry{}}
}

func (i *literalIndex) add(key, literal string, scriptIndex int, script string, entry field) {
	existing, ok := i.entries[key]
	if !ok {
		if len(i.entries) >= maxTrackedLiterals {
			i.dropped++
			return
		}
		existing = &literalEntry{literal: literal, seen: map[string]bool{}}
		i.entries[key] = existing
		i.order = append(i.order, key)
	}
	if strings.HasPrefix(entry.name, FieldHeaderPrefix) {
		existing.headerName = strings.TrimPrefix(entry.name, FieldHeaderPrefix)
	}
	if entry.name == FieldBaseURL {
		existing.targetsBaseURL = true
	}
	// A header reachable through both the legacy Headers map and HeaderList is
	// one occurrence the operator can see, not two. Deduplicating on
	// script+path+field keeps the reported reach honest; Apply still rewrites
	// every physical field behind that key, because it matches on the same key
	// while walking.
	locationKey := strconv.Itoa(scriptIndex) + "\x00" + entry.path + "\x00" + entry.name
	if existing.seen[locationKey] {
		return
	}
	existing.seen[locationKey] = true

	existing.occurrences++
	found := false
	for _, affected := range existing.scriptOrder {
		if affected.index == scriptIndex {
			found = true
			break
		}
	}
	if !found {
		existing.scriptOrder = append(existing.scriptOrder, affectedScript{index: scriptIndex, name: script})
	}
	existing.locations = append(existing.locations, Location{
		ScriptIndex: scriptIndex,
		Path:        entry.path,
		Field:       entry.name,
		SearchText:  literal,
		ReplaceText: literal,
	})
}

// ranked orders by reach then by first-seen so the result is stable across runs.
func (i *literalIndex) ranked() []string {
	ranked := append([]string(nil), i.order...)
	position := make(map[string]int, len(i.order))
	for index, key := range i.order {
		position[key] = index
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		left, right := ranked[a], ranked[b]
		leftEntry, rightEntry := i.entries[left], i.entries[right]
		if len(leftEntry.scriptOrder) != len(rightEntry.scriptOrder) {
			return len(leftEntry.scriptOrder) > len(rightEntry.scriptOrder)
		}
		if leftEntry.occurrences != rightEntry.occurrences {
			return leftEntry.occurrences > rightEntry.occurrences
		}
		return position[left] < position[right]
	})
	return ranked
}

// absoluteOrigin returns scheme://host[:port] for an absolute URL, or "" when
// the target is relative (already riding on config.base_url) or templated.
func absoluteOrigin(target string) string {
	if target == "" || strings.Contains(target, "{{") || len(target) > 8192 {
		return ""
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// structuralHeaders carry protocol mechanics rather than environment settings.
// Offering them as shared values would bury the ones that matter.
var structuralHeaders = map[string]bool{
	"content-type": true, "accept": true, "accept-encoding": true,
	"accept-language": true, "connection": true, "content-length": true,
	"host": true, "user-agent": true, "cache-control": true,
	"transfer-encoding": true, "expect": true, "te": true, "upgrade": true,
}

func sharedHeaderCandidate(name, value string) bool {
	if name == "" || value == "" || strings.Contains(value, "{{") {
		return false
	}
	if len(value) > maxSharedValueBytes || isRedacted(value) {
		return false
	}
	return !structuralHeaders[strings.ToLower(strings.TrimSpace(name))]
}

// isRedacted reports whether a value came from a disclosure projection rather
// than the script itself.
//
// Detection runs against the masked projection while the mutation is applied to
// the exact server-held draft. A literal the projection replaced with the
// redaction sentinel therefore does not exist in the apply target, so offering
// it would produce a candidate that silently rewrites nothing — the operator
// enters a value, the request succeeds, and applied_count is zero.
func isRedacted(value string) bool {
	return strings.Contains(value, credential.Redacted)
}
