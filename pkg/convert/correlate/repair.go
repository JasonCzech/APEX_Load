package correlate

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Repair locations cross an API boundary and are applied interactively. A
// pathological request can contain the same stale literal millions of times;
// retaining every occurrence is neither useful nor safe. The cap is per field
// and category, keeping suggestions actionable while bounding CPU and memory.
const (
	maxRepairLocationsPerField            = 256
	maxRepairSearchBytesPerAnalysis int64 = 64 << 20
	maxRepairLocationsPerAnalysis         = 8192
	maxRepairSuggestionsPerAnalysis       = 4096
)

type repairAnalysisLimits struct {
	searchBytes int64
	locations   int
	suggestions int
}

var defaultRepairAnalysisLimits = repairAnalysisLimits{
	searchBytes: maxRepairSearchBytesPerAnalysis,
	locations:   maxRepairLocationsPerAnalysis,
	suggestions: maxRepairSuggestionsPerAnalysis,
}

// RepairSeverity ranks how likely a finding is to break the replay.
type RepairSeverity string

const (
	// SeverityError means the replay almost certainly fails as-is (a stale,
	// rotated value is being replayed verbatim).
	SeverityError RepairSeverity = "error"
	// SeverityWarning means a correlation source the recording relied on was not
	// found on replay (boundary/shape drift) and should be reviewed.
	SeverityWarning RepairSeverity = "warning"
)

// SuggestedExtractor is the concrete correlation fix the engine proposes — the
// same shape the authoring/DSL layer needs to insert an extractor.
type SuggestedExtractor struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	ExtractType string `json:"extract_type"`
	Expression  string `json:"expression"`
	Convert     string `json:"convert,omitempty"`
	OnMiss      string `json:"on_miss,omitempty"`
}

// RepairLocation identifies a stale literal in the replay request without
// returning the literal itself. Offsets are UTF-16 code-unit indexes so browser
// clients can safely slice the corresponding replay field with String.slice.
type RepairLocation struct {
	Field                string `json:"field"`
	Start                int    `json:"start"`
	End                  int    `json:"end"`
	EditableSurfaceExact bool   `json:"editable_surface_exact,omitempty"`
}

// RepairSuggestion is a single ranked, explainable replay-correlation finding
// with a proposed fix. This is what turns APEX from "a converter" into a
// migration assistant: it tells the user *which* step will break, *why*, and the
// exact extractor to add.
type RepairSuggestion struct {
	RuleID          string              `json:"rule_id"`
	SourceFamily    ResponseFamily      `json:"source_family,omitempty"`
	Severity        RepairSeverity      `json:"severity"`
	Title           string              `json:"title"`
	Detail          string              `json:"detail"`
	SourceStepID    int                 `json:"source_step_id"`
	FailingStepID   int                 `json:"failing_step_id,omitempty"`
	UsedInIDs       []int               `json:"used_in_ids,omitempty"`
	UsageCount      int                 `json:"usage_count,omitempty"`
	AffectedFields  []string            `json:"affected_fields,omitempty"`
	RepairLocations []RepairLocation    `json:"repair_locations,omitempty"`
	RecordedValue   string              `json:"recorded_value,omitempty"`
	ReplayValue     string              `json:"replay_value,omitempty"`
	Suggestion      string              `json:"suggestion"`
	Extractor       *SuggestedExtractor `json:"extractor,omitempty"`
	Confidence      float64             `json:"confidence"`
	ConfidenceBand  string              `json:"confidence_band"`
	ReviewAction    string              `json:"review_action"`
}

// DiagnoseReplay compares a recording against a replay of the same flow (steps
// matched by Observation.ID) and reports correlation failures with repair
// suggestions. It reuses the rules engine: a value that was dynamic in the
// recording, rotated on replay, and is still being sent verbatim by a later step
// is a missing correlation; a correlation source that vanished on replay is a
// boundary drift.
func DiagnoseReplay(recorded, replay []Observation) []RepairSuggestion {
	suggestions, _ := DiagnoseReplayChecked(recorded, replay)
	return suggestions
}

// DiagnoseReplayChecked is the bounded/error-reporting form of DiagnoseReplay.
// API callers must use it so an exhausted analysis is surfaced as an error
// instead of being presented as a clean replay.
func DiagnoseReplayChecked(recorded, replay []Observation) ([]RepairSuggestion, error) {
	return diagnoseReplayCheckedWithLimits(recorded, replay, defaultRepairAnalysisLimits)
}

// DiagnoseReplayReviewChecked returns the same checked repair analysis plus
// bounded, value-free relationship coverage. Detection and replay candidate
// indexing each run once; an exhausted analysis never returns partial advice.
func DiagnoseReplayReviewChecked(recorded, replay []Observation) (ReplayDiagnosis, error) {
	return diagnoseReplayReviewCheckedWithLimits(recorded, replay, defaultRepairAnalysisLimits)
}

func diagnoseReplayCheckedWithLimits(recorded, replay []Observation, limits repairAnalysisLimits) ([]RepairSuggestion, error) {
	diagnosis, err := diagnoseReplayReviewCheckedWithLimits(recorded, replay, limits)
	return diagnosis.Suggestions, err
}

func diagnoseReplayReviewCheckedWithLimits(recorded, replay []Observation, limits repairAnalysisLimits) (ReplayDiagnosis, error) {
	correlations, err := DetectChecked(recorded)
	if err != nil {
		return ReplayDiagnosis{}, err
	}
	replayCandidatesByID, replayIndexTruncated, err := candidateIndexChecked(replay)
	if err != nil {
		return ReplayDiagnosis{}, err
	}
	if replayIndexTruncated {
		// Diagnose stays fail-closed: a partially indexed replay would let
		// missing suggestions read as a clean replay.
		return ReplayDiagnosis{}, fmt.Errorf("%w: aggregate candidate count exceeds %d", ErrCorrelationWorkLimit, maxCorrelationCandidatesPerAnalysis)
	}
	replayByID := indexObservations(replay)
	coverage := newRelationshipCoverage(recorded, replayByID)
	if len(correlations) == 0 {
		coverage.Complete = false
	}
	missingConsumerWarnings := make(map[int]bool)
	suggestions := make([]RepairSuggestion, 0)
	var repairSearchBytes int64
	repairLocations := 0
	appendSuggestion := func(suggestion RepairSuggestion) error {
		if len(suggestions) >= limits.suggestions {
			return fmt.Errorf("%w: repair suggestion count exceeds %d", ErrCorrelationWorkLimit, limits.suggestions)
		}
		if len(suggestion.RepairLocations) > limits.locations-repairLocations {
			return fmt.Errorf("%w: repair location count exceeds %d", ErrCorrelationWorkLimit, limits.locations)
		}
		repairLocations += len(suggestion.RepairLocations)
		suggestions = append(suggestions, suggestion)
		return nil
	}
	claimedNames := make(map[string]bool, len(correlations))
	for _, correlation := range correlations {
		if correlation.Name != "" {
			claimedNames[correlation.Name] = true
		}
	}

	for _, c := range correlations {
		coverage.ProducerCount++
		_, producerObserved := replayByID[c.FoundInID]
		replayValue, extractorAvailable := lookupCandidateValue(replayCandidatesByID[c.FoundInID], c)
		extractorAvailable = producerObserved && extractorAvailable && replayValue != ""
		for _, usedID := range c.UsedInIDs {
			if _, exists := replayByID[usedID]; exists {
				continue
			}
			coverage.record(c, usedID, RelationshipConsumerMissing, producerObserved, extractorAvailable, false)
			if !missingConsumerWarnings[usedID] {
				if err := appendSuggestion(missingCorrelationConsumerSuggestion(c.FoundInID, usedID)); err != nil {
					return ReplayDiagnosis{}, err
				}
				missingConsumerWarnings[usedID] = true
			}
		}
		recordObservedConsumers := func(state RelationshipState) {
			for _, usedID := range c.UsedInIDs {
				if _, exists := replayByID[usedID]; exists {
					coverage.record(c, usedID, state, producerObserved, extractorAvailable, true)
				}
			}
		}
		if !producerObserved {
			coverage.MissingProducers++
			coverage.Complete = false
			recordObservedConsumers(RelationshipProducerMissing)
			if err := appendSuggestion(missingCorrelationSourceSuggestion(c,
				fmt.Sprintf("Step %d was not observed in the replay, so the recorded extractor for %q could not be verified.", c.FoundInID, c.Name))); err != nil {
				return ReplayDiagnosis{}, err
			}
			continue
		}
		if !extractorAvailable {
			coverage.UnavailableExtractors++
			coverage.Complete = false
			recordObservedConsumers(RelationshipExtractorUnavailable)
			// The recording extracted this value here, but the replay response no
			// longer exposes it — the boundary or JSON path likely drifted.
			if err := appendSuggestion(missingCorrelationSourceSuggestion(c,
				fmt.Sprintf("Step %d no longer exposes %q at the recorded extractor location; the response boundary may have changed or become ambiguous.", c.FoundInID, c.Name))); err != nil {
				return ReplayDiagnosis{}, err
			}
			continue
		}

		if replayValue == c.Value {
			recordObservedConsumers(RelationshipProducerUnchanged)
			// Value did not rotate between record and replay; treating it as a hard
			// dynamic value would be a false positive, so skip.
			continue
		}

		// Decide variable naming across every consumer of this correlation. Raw
		// and query-encoded uses may occur in different steps; they still require
		// two extractor variables with stable names across all suggestions.
		usageByID := make(map[int]staleRepairUsage, len(c.UsedInIDs))
		hasRawUsage := false
		hasEncodedUsage := false
		for _, usedID := range c.UsedInIDs {
			if used, ok := replayByID[usedID]; ok {
				cost := correlationRequestEvidenceBytes(used)
				if cost > limits.searchBytes-repairSearchBytes {
					return ReplayDiagnosis{}, fmt.Errorf("%w: repair request-surface search exceeds %d bytes", ErrCorrelationWorkLimit, limits.searchBytes)
				}
				repairSearchBytes += cost
				usage, overflow := staleRepairUsageForObservation(used, c.Value, replayValue, limits.locations-repairLocations)
				if overflow {
					return ReplayDiagnosis{}, fmt.Errorf("%w: repair location count exceeds %d", ErrCorrelationWorkLimit, limits.locations)
				}
				usageByID[usedID] = usage
				state := RelationshipNoStaleLiteral
				switch {
				case len(usage.nonRepairable) > 0:
					state = RelationshipManualSurface
				case len(usage.unsupportedEncoded) > 0:
					state = RelationshipManualEncoding
				case len(usage.raw) > 0 || len(usage.queryEncoded) > 0:
					state = RelationshipStaleLiteral
				}
				coverage.record(c, usedID, state, true, true, true)
				hasRawUsage = hasRawUsage || len(usage.raw) > 0
				hasEncodedUsage = hasEncodedUsage || len(usage.queryEncoded) > 0
			}
		}
		encodedExtractorName := c.Name
		if hasRawUsage && hasEncodedUsage {
			encodedExtractorName = claimRepairName(c.Name+"_encoded", claimedNames)
		}

		// The value rotated on replay. Any later step still sending the *recorded*
		// literal (and not the fresh replay value) will be rejected by the server.
		for _, usedID := range c.UsedInIDs {
			usage, ok := usageByID[usedID]
			if !ok {
				continue
			}
			if len(usage.raw) > 0 {
				if err := appendSuggestion(staleValueSuggestion(c, usedID, replayValue, usage.raw, extractorFor(c), "")); err != nil {
					return ReplayDiagnosis{}, err
				}
			}
			if len(usage.queryEncoded) > 0 {
				extractor := extractorFor(c)
				convert, supported := EncodedReuseExtractorConvert(c.Convert)
				if supported {
					extractor.Convert = convert
					extractor.Name = encodedExtractorName
					if err := appendSuggestion(staleValueSuggestion(c, usedID, replayValue, usage.queryEncoded, extractor, "The stale request value is query/form encoded.")); err != nil {
						return ReplayDiagnosis{}, err
					}
				} else {
					if err := appendSuggestion(manualEncodedRepairSuggestion(c, usedID, replayValue, usage.queryEncoded,
						"The source conversion cannot be combined safely with url_encode; update this encoded request value manually.")); err != nil {
						return ReplayDiagnosis{}, err
					}
				}
			}
			if len(usage.unsupportedEncoded) > 0 {
				if err := appendSuggestion(manualEncodedRepairSuggestion(c, usedID, replayValue, usage.unsupportedEncoded,
					"The stale request uses a path or non-canonical form encoding that the DSL's url_encode conversion cannot reproduce exactly; update it manually.")); err != nil {
					return ReplayDiagnosis{}, err
				}
			}
			if len(usage.nonRepairable) > 0 {
				if err := appendSuggestion(manualNonRepairableSuggestion(c, usedID, replayValue, usage.nonRepairable)); err != nil {
					return ReplayDiagnosis{}, err
				}
			}
		}
	}

	// A replay step can be rejected (HTTP >=400) without any dynamic value
	// visibly rotating — e.g. a stale CSRF/nonce the server echoes back unchanged,
	// or a token the rule catalog does not recognize. The value-diff loop above
	// cannot see those; this pass flags rejected steps that no suggestion already
	// explains so they are not silently missed.
	for _, suggestion := range diagnoseUnexplainedRejections(replay, suggestions) {
		if err := appendSuggestion(suggestion); err != nil {
			return ReplayDiagnosis{}, err
		}
	}

	sort.SliceStable(suggestions, func(i, j int) bool {
		if suggestions[i].Severity != suggestions[j].Severity {
			// Errors before warnings.
			return suggestions[i].Severity == SeverityError
		}
		if suggestions[i].Confidence != suggestions[j].Confidence {
			return suggestions[i].Confidence > suggestions[j].Confidence
		}
		return suggestions[i].FailingStepID < suggestions[j].FailingStepID
	})
	return ReplayDiagnosis{Suggestions: suggestions, RelationshipCoverage: coverage}, nil
}

type staleRepairUsage struct {
	raw                []RepairLocation
	queryEncoded       []RepairLocation
	unsupportedEncoded []RepairLocation
	nonRepairable      []string
}

func staleValueSuggestion(c Correlation, usedID int, replayValue string, locations []RepairLocation, extractor *SuggestedExtractor, detailSuffix string) RepairSuggestion {
	detail := fmt.Sprintf("Step %d replays the recorded value for %s, but step %d now returns a different value — the server will reject the stale value.", usedID, c.Name, c.FoundInID)
	if detailSuffix != "" {
		detail += " " + detailSuffix
	}
	reviewAction := reviewActionFor(SeverityError, c.Confidence)
	if len(locations) > 0 && !allRepairLocationsEditable(locations) {
		// Generic/import observations carry wire evidence without trusted proof
		// that its offsets match an editable DSL field. Keep the extractor useful,
		// but never advertise automatic application to API clients in that case.
		reviewAction = "manual_value_replacement_required"
	}
	return RepairSuggestion{
		RuleID:          c.RuleID,
		SourceFamily:    FamilyForRule(c.RuleID),
		Severity:        SeverityError,
		Title:           "Stale value replayed (missing correlation)",
		Detail:          detail,
		SourceStepID:    c.FoundInID,
		FailingStepID:   usedID,
		UsedInIDs:       append([]int(nil), c.UsedInIDs...),
		UsageCount:      len(c.UsedInIDs),
		AffectedFields:  fieldsForRepairLocations(locations),
		RepairLocations: append([]RepairLocation(nil), locations...),
		RecordedValue:   c.Value,
		ReplayValue:     replayValue,
		Suggestion:      fmt.Sprintf("Capture %s from step %d and substitute {{var.%s}} in step %d.", extractor.Name, c.FoundInID, extractor.Name, usedID),
		Extractor:       extractor,
		Confidence:      c.Confidence,
		ConfidenceBand:  confidenceBand(c.Confidence),
		ReviewAction:    reviewAction,
	}
}

func allRepairLocationsEditable(locations []RepairLocation) bool {
	for _, location := range locations {
		if !location.EditableSurfaceExact {
			return false
		}
	}
	return true
}

func manualEncodedRepairSuggestion(c Correlation, usedID int, replayValue string, locations []RepairLocation, reason string) RepairSuggestion {
	suggestion := staleValueSuggestion(c, usedID, replayValue, locations, extractorFor(c), reason)
	suggestion.Extractor = nil
	suggestion.Suggestion = "Add a compatible extractor and replace the identified stale request field manually without changing its encoding semantics."
	suggestion.ReviewAction = "manual_value_replacement_required"
	return suggestion
}

func manualNonRepairableSuggestion(c Correlation, usedID int, replayValue string, fields []string) RepairSuggestion {
	suggestion := staleValueSuggestion(c, usedID, replayValue, nil, extractorFor(c),
		"The stale value is present on a request surface that cannot be represented as a complete set of safe editable offsets (for example, a transformed body or too many repeated occurrences).")
	suggestion.Extractor = nil
	suggestion.AffectedFields = append([]string(nil), fields...)
	suggestion.Suggestion = "Add a compatible extractor and replace every stale occurrence in the identified request field manually."
	suggestion.ReviewAction = "manual_value_replacement_required"
	return suggestion
}

func claimRepairName(base string, used map[string]bool) string {
	if !used[base] {
		used[base] = true
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func fieldsForRepairLocations(locations []RepairLocation) []string {
	seen := make(map[string]bool, len(locations))
	fields := make([]string, 0, len(locations))
	for _, location := range locations {
		if location.Field == "" || seen[location.Field] {
			continue
		}
		seen[location.Field] = true
		fields = append(fields, location.Field)
	}
	return fields
}

func correlationRequestEvidenceBytes(observation Observation) int64 {
	total := int64(len(observation.RequestURL) + len(observation.RequestBody))
	for key, value := range observation.RequestHeaders {
		total += int64(len(key) + len(value))
	}
	return total
}

func staleRepairUsageForObservation(observation Observation, recordedValue, replayValue string, locationLimit int) (staleRepairUsage, bool) {
	usage := staleRepairUsage{}
	locationCount := func() int {
		return len(usage.raw) + len(usage.queryEncoded) + len(usage.unsupportedEncoded)
	}
	appendField := func(field, content string, editableSurfaceExact bool) bool {
		raw, queryEncoded, unsupported, overflow := staleRepairLocations(field, content, recordedValue, replayValue, editableSurfaceExact)
		if overflow {
			if !containsString(usage.nonRepairable, field) {
				usage.nonRepairable = append(usage.nonRepairable, field)
			}
			return true
		}
		if len(raw)+len(queryEncoded)+len(unsupported) > locationLimit-locationCount() {
			return false
		}
		usage.raw = append(usage.raw, raw...)
		usage.queryEncoded = append(usage.queryEncoded, queryEncoded...)
		usage.unsupportedEncoded = append(usage.unsupportedEncoded, unsupported...)
		return true
	}
	if locationLimit < 0 || !appendField("request_url", observation.RequestURL, observation.EditableRequestURL) {
		return staleRepairUsage{}, true
	}
	if observation.RequestBodyTransformed {
		if requestFieldContainsValue(observation.RequestBody, recordedValue, replayValue, true) {
			usage.nonRepairable = append(usage.nonRepairable, "request_body")
		}
	} else {
		if !appendField("request_body", observation.RequestBody, observation.EditableRequestBody) {
			return staleRepairUsage{}, true
		}
	}
	keys := make([]string, 0, len(observation.RequestHeaders))
	for key := range observation.RequestHeaders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !appendField("request_headers."+key, observation.RequestHeaders[key], editableHeaderSurface(observation.EditableRequestHeaders, key)) {
			return staleRepairUsage{}, true
		}
	}
	return usage, false
}

type byteRange struct {
	start int
	end   int
}

func staleRepairLocations(field, content, recordedValue, replayValue string, editableSurfaceExact bool) (raw, queryEncoded, unsupported []RepairLocation, overflow bool) {
	if content == "" || recordedValue == "" {
		return nil, nil, nil, false
	}
	protected, protectedOverflow := literalRangesLimited(content, correlationValueRepresentations(replayValue), maxRepairLocationsPerField)
	if protectedOverflow {
		// If fresh-value ranges cannot be represented within the bound, emitting a
		// later stale offset could target text that is actually part of a fresh
		// value. Fail closed for this field.
		return nil, nil, nil, true
	}
	queryForm := QueryEncodedFormOf(recordedValue)
	type repairRepresentation struct {
		value    string
		category string
	}
	representations := []repairRepresentation{{value: recordedValue, category: "raw"}}
	if queryForm != "" && queryForm != recordedValue {
		representations = append(representations, repairRepresentation{value: queryForm, category: "query"})
	}
	for _, spelling := range correlationValueRepresentations(recordedValue) {
		if spelling == recordedValue || spelling == queryForm {
			continue
		}
		representations = append(representations, repairRepresentation{value: spelling, category: "unsupported"})
	}

	// A raw value can itself be a prefix of its URL-encoded spelling (for
	// example, "token%" is a prefix of "token%25"). Classifying every spelling
	// independently would then emit two overlapping automatic repairs, and the
	// raw repair would leave the encoded suffix behind. Gather all concrete
	// matches first and give each byte range one longest-spelling owner. Equal-
	// length overlaps are resolved left-to-right, matching a single-pass literal
	// replacement and keeping the emitted offsets mutually non-overlapping.
	type ownedRange struct {
		byteRange
		category string
	}
	candidates := make([]ownedRange, 0)
	for _, representation := range representations {
		matches, matchesOverflow := literalRangesLimited(content, []string{representation.value}, maxRepairLocationsPerField)
		if matchesOverflow {
			return nil, nil, nil, true
		}
		for _, match := range matches {
			if !overlapsAny(match, protected) {
				candidates = append(candidates, ownedRange{byteRange: match, category: representation.category})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].start != candidates[j].start {
			return candidates[i].start < candidates[j].start
		}
		leftLength := candidates[i].end - candidates[i].start
		rightLength := candidates[j].end - candidates[j].start
		if leftLength != rightLength {
			return leftLength > rightLength
		}
		return candidates[i].category < candidates[j].category
	})
	owned := make([]ownedRange, 0, len(candidates))
	for _, candidate := range candidates {
		if len(owned) == 0 || candidate.start >= owned[len(owned)-1].end {
			owned = append(owned, candidate)
			continue
		}
		last := owned[len(owned)-1]
		candidateLength := candidate.end - candidate.start
		lastLength := last.end - last.start
		if candidateLength > lastLength {
			// Selected ranges are non-overlapping and ordered. Because candidate.start
			// is at or after last.start, replacing last cannot overlap an earlier
			// selected range.
			owned[len(owned)-1] = candidate
		}
	}

	rawRanges := make([]byteRange, 0)
	queryRanges := make([]byteRange, 0)
	unsupportedRanges := make([]byteRange, 0)
	for _, item := range owned {
		switch item.category {
		case "raw":
			rawRanges = append(rawRanges, item.byteRange)
		case "query":
			queryRanges = append(queryRanges, item.byteRange)
		default:
			unsupportedRanges = append(unsupportedRanges, item.byteRange)
		}
	}
	if len(rawRanges) > maxRepairLocationsPerField || len(queryRanges) > maxRepairLocationsPerField || len(unsupportedRanges) > maxRepairLocationsPerField {
		return nil, nil, nil, true
	}
	return repairLocationsForRanges(field, content, rawRanges, editableSurfaceExact),
		repairLocationsForRanges(field, content, queryRanges, editableSurfaceExact),
		repairLocationsForRanges(field, content, unsupportedRanges, editableSurfaceExact), false
}

func literalRangesLimited(content string, representations []string, limit int) ([]byteRange, bool) {
	if limit <= 0 {
		return nil, len(content) > 0 && len(representations) > 0
	}
	ranges := make([]byteRange, 0, min(limit, 16))
	seen := make(map[byteRange]bool)
	for _, representation := range representations {
		if representation == "" {
			continue
		}
		for offset := 0; offset <= len(content)-len(representation); {
			index := strings.Index(content[offset:], representation)
			if index < 0 {
				break
			}
			match := byteRange{start: offset + index, end: offset + index + len(representation)}
			if !seen[match] {
				if len(ranges) >= limit {
					return ranges, true
				}
				seen[match] = true
				ranges = append(ranges, match)
			}
			offset = match.start + 1
		}
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start != ranges[j].start {
			return ranges[i].start < ranges[j].start
		}
		return ranges[i].end < ranges[j].end
	})
	return ranges, false
}

func overlapsAny(candidate byteRange, protected []byteRange) bool {
	for _, item := range protected {
		if candidate.start < item.end && item.start < candidate.end {
			return true
		}
	}
	return false
}

// repairLocationsForRanges converts sorted byte ranges to JavaScript UTF-16
// offsets with one forward scan instead of rescanning content from byte zero for
// every occurrence.
func repairLocationsForRanges(field, content string, ranges []byteRange, editableSurfaceExact bool) []RepairLocation {
	if len(ranges) == 0 {
		return nil
	}
	targets := make([]int, 0, len(ranges)*2)
	for _, item := range ranges {
		targets = append(targets, item.start, item.end)
	}
	sort.Ints(targets)
	offsets := make(map[int]int, len(targets))
	byteOffset := 0
	utf16Offset := 0
	for _, target := range targets {
		if _, exists := offsets[target]; exists {
			continue
		}
		for byteOffset < target {
			r, size := utf8.DecodeRuneInString(content[byteOffset:])
			if size <= 0 {
				size = 1
			}
			if byteOffset+size > target {
				// Literal matches in valid Go strings align to rune boundaries. Keep
				// malformed input bounded and conservative if they do not.
				byteOffset = target
				break
			}
			units := utf16.RuneLen(r)
			if units < 0 {
				units = 1
			}
			utf16Offset += units
			byteOffset += size
		}
		offsets[target] = utf16Offset
	}
	locations := make([]RepairLocation, len(ranges))
	for index, item := range ranges {
		locations[index] = RepairLocation{
			Field:                field,
			Start:                offsets[item.start],
			End:                  offsets[item.end],
			EditableSurfaceExact: editableSurfaceExact,
		}
	}
	return locations
}

func editableHeaderSurface(proofs map[string]bool, name string) bool {
	for candidate, exact := range proofs {
		if strings.EqualFold(candidate, name) {
			return exact
		}
	}
	return false
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func missingCorrelationSourceSuggestion(c Correlation, detail string) RepairSuggestion {
	return RepairSuggestion{
		RuleID:         c.RuleID,
		SourceFamily:   FamilyForRule(c.RuleID),
		Severity:       SeverityWarning,
		Title:          "Correlation source missing on replay",
		Detail:         detail,
		SourceStepID:   c.FoundInID,
		UsedInIDs:      append([]int(nil), c.UsedInIDs...),
		UsageCount:     len(c.UsedInIDs),
		Suggestion:     fmt.Sprintf("Re-check the extractor for %s (expression %q) against the current response.", c.Name, c.Expression),
		Extractor:      extractorFor(c),
		Confidence:     c.Confidence,
		ConfidenceBand: confidenceBand(c.Confidence),
		ReviewAction:   reviewActionFor(SeverityWarning, c.Confidence),
	}
}

// diagnoseUnexplainedRejections flags server rejection and assertion-aware
// failure with a received HTTP response when existing repairs do not explain
// the step. Unknown status remains a no-op; an asserted failure on HTTP 2xx/3xx
// is manual-only and does not infer an authentication or correlation cause.
func diagnoseUnexplainedRejections(replay []Observation, existing []RepairSuggestion) []RepairSuggestion {
	explained := make(map[int]struct{}, len(existing))
	for _, s := range existing {
		if s.FailingStepID != 0 {
			explained[s.FailingStepID] = struct{}{}
		}
	}
	out := make([]RepairSuggestion, 0)
	assertionsReported := make(map[int]bool)
	for _, o := range replay {
		if o.ResponseSuccess != nil && *o.ResponseSuccess {
			continue
		}
		if o.ResponseStatus > 0 && o.ResponseStatus < 400 && o.ResponseSuccess != nil && !*o.ResponseSuccess {
			if assertionsReported[o.ID] {
				continue
			}
			assertionsReported[o.ID] = true
			out = append(out, RepairSuggestion{
				RuleID: "replay_assertion_failed_unexplained", Severity: SeverityWarning,
				Title:         "Replay step failed without a detected cause",
				Detail:        fmt.Sprintf("Step %d received HTTP %d, but the replay engine reported failure. This comparison does not identify the cause.", o.ID, o.ResponseStatus),
				FailingStepID: o.ID,
				Suggestion:    "Inspect action diagnostics, failed assertions, and response expectations manually before choosing a repair.",
				Confidence:    0.4, ConfidenceBand: confidenceBand(0.4), ReviewAction: "manual_review_required",
			})
			continue
		}
		if o.ResponseStatus < 400 {
			continue
		}
		if _, ok := explained[o.ID]; ok {
			continue
		}
		out = append(out, RepairSuggestion{
			RuleID:         "replay_rejected_unexplained",
			Severity:       SeverityWarning,
			Title:          "Replay step rejected without a detected correlation cause",
			Detail:         fmt.Sprintf("Step %d failed on replay (HTTP %d) but no rotated dynamic value was detected — the rejection may stem from a token the rule engine does not recognize (e.g. a custom CSRF/anti-replay header re-sent verbatim).", o.ID, o.ResponseStatus),
			FailingStepID:  o.ID,
			Suggestion:     "Inspect this step's request for an auth/CSRF/nonce value that must be re-extracted from an earlier response, even if its value did not visibly change between record and replay.",
			Confidence:     0.4,
			ConfidenceBand: confidenceBand(0.4),
			ReviewAction:   "manual_review_required",
		})
	}
	return out
}

func ObservationUsageFields(observation Observation, value string) []string {
	return observationUsageFields(observation, value, "")
}

// observationStaleUsageFields reports fields that still carry the recorded
// value after occurrences of the fresh replay value (raw or URL/form encoded)
// are removed. Comparing per field prevents a correctly updated header from
// hiding a stale body or URL, while also avoiding a false stale match when the
// old value is merely a substring of the fresh value.
func observationStaleUsageFields(observation Observation, recordedValue, replayValue string) []string {
	return observationUsageFields(observation, recordedValue, replayValue)
}

func observationUsageFields(observation Observation, value, excludedValue string) []string {
	if value == "" {
		return nil
	}
	fields := make([]string, 0, 4)
	if requestFieldContainsValue(observation.RequestURL, value, excludedValue, false) {
		fields = append(fields, "request_url")
	}
	if requestFieldContainsValue(observation.RequestBody, value, excludedValue, true) {
		fields = append(fields, "request_body")
	}
	keys := make([]string, 0, len(observation.RequestHeaders))
	for key := range observation.RequestHeaders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if requestFieldContainsValue(observation.RequestHeaders[key], value, excludedValue, false) {
			fields = append(fields, "request_headers."+key)
		}
	}
	return fields
}

func requestFieldContainsValue(content, value, excludedValue string, includeFormValues bool) bool {
	if content == "" || value == "" {
		return false
	}
	search := observationSearch{}
	search.addSurface(content)
	if includeFormValues {
		search.addFormValues(content)
	}
	surfaces := make([]string, 0, len(search.rawSurfaces)+len(search.decodedSurfaces)+len(search.formValues))
	surfaces = append(surfaces, search.rawSurfaces...)
	surfaces = append(surfaces, search.decodedSurfaces...)
	surfaces = append(surfaces, search.formValues...)
	valueRepresentations := correlationValueRepresentations(value)
	excludedRepresentations := correlationValueRepresentations(excludedValue)
	for _, surface := range surfaces {
		for _, representation := range excludedRepresentations {
			surface = strings.ReplaceAll(surface, representation, "")
		}
		for _, representation := range valueRepresentations {
			if strings.Contains(surface, representation) {
				return true
			}
		}
	}
	return false
}

// correlationValueRepresentations returns the concrete spellings a request can
// carry for one extracted value. Templates replace all of these spellings with
// the same runtime variable; braces must remain unescaped for DSL resolution.
func correlationValueRepresentations(value string) []string {
	if value == "" {
		return nil
	}
	candidates := append([]string{value}, EncodedFormsOf(value)...)
	seen := make(map[string]struct{}, len(candidates)*2)
	representations := make([]string, 0, len(candidates)*2)
	appendUnique := func(candidate string) {
		if candidate == "" {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		representations = append(representations, candidate)
	}
	for _, candidate := range candidates {
		appendUnique(candidate)
		appendUnique(lowerPercentEscapes(candidate))
	}
	sort.SliceStable(representations, func(i, j int) bool {
		return len(representations[i]) > len(representations[j])
	})
	return representations
}

func lowerPercentEscapes(value string) string {
	bytes := []byte(value)
	for i := 0; i+2 < len(bytes); i++ {
		if bytes[i] != '%' {
			continue
		}
		for offset := 1; offset <= 2; offset++ {
			if bytes[i+offset] >= 'A' && bytes[i+offset] <= 'F' {
				bytes[i+offset] += 'a' - 'A'
			}
		}
		i += 2
	}
	return string(bytes)
}

// HasBlockingRepairs reports whether any suggestion is severe enough to fail a
// replay gate (used by the replay/authoring surfaces to block save/approve).
func HasBlockingRepairs(suggestions []RepairSuggestion) bool {
	for _, s := range suggestions {
		if s.Severity == SeverityError {
			return true
		}
	}
	return false
}

func indexObservations(observations []Observation) map[int]Observation {
	index := make(map[int]Observation, len(observations))
	for _, o := range observations {
		index[o.ID] = o
	}
	return index
}

// candidateValueForCorrelation re-runs the rule extractors over a replay
// response and returns the fresh value only when the recorded extractor
// identity maps to one unambiguous value. Display names are not identities: the
// same name commonly appears at multiple JSON paths or response surfaces.
func candidateValueForCorrelation(observation Observation, corr Correlation) string {
	value, _ := uniqueCandidateValue(Candidates(observation.ResponseBody, observation.ResponseHeaders), corr)
	return value
}

func uniqueCandidateValue(candidates []Candidate, corr Correlation) (string, bool) {
	value := ""
	found := false
	for _, candidate := range candidates {
		if !sameCandidateLocation(candidate, corr) {
			continue
		}
		if !found {
			value = candidate.Value
			found = true
			continue
		}
		if candidate.Value != value {
			return "", false
		}
	}
	return value, found
}

func sameCandidateLocation(candidate Candidate, corr Correlation) bool {
	candidateSource := strings.TrimSpace(candidate.Source)
	corrSource := strings.TrimSpace(corr.Source)
	if !strings.EqualFold(candidateSource, corrSource) ||
		normalizeExtractType(candidate.ExtractType) != normalizeExtractType(corr.ExtractType) ||
		!strings.EqualFold(strings.TrimSpace(candidate.Convert), strings.TrimSpace(corr.Convert)) {
		return false
	}
	candidateExpression := strings.TrimSpace(candidate.Expression)
	corrExpression := strings.TrimSpace(corr.Expression)
	if strings.EqualFold(candidateSource, "header") && normalizeExtractType(candidate.ExtractType) == "header" {
		return strings.EqualFold(candidateExpression, corrExpression)
	}
	return candidateExpression == corrExpression
}

func normalizeExtractType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized
}

func extractorFor(c Correlation) *SuggestedExtractor {
	return &SuggestedExtractor{
		Name:        c.Name,
		Source:      c.Source,
		ExtractType: c.ExtractType,
		Expression:  c.Expression,
		Convert:     c.Convert,
		OnMiss:      "fail",
	}
}

func confidenceBand(confidence float64) string {
	switch {
	case confidence >= 0.8:
		return "high"
	case confidence >= 0.6:
		return "medium"
	default:
		return "low"
	}
}

func reviewActionFor(severity RepairSeverity, confidence float64) string {
	if severity == SeverityError && confidence >= 0.8 {
		return "auto_apply_available"
	}
	if severity == SeverityError {
		return "manual_review_required"
	}
	return "review_extractor_boundary"
}
