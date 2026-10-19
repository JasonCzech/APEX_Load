package sample

// correlate_redact.go — split from converter.go (same package, behavior-preserving).

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/httpcontract"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func detectCorrelations(exchanges []exchange) []Correlation {
	correlations, _ := detectCorrelationsChecked(exchanges)
	return correlations
}

func sampleCorrelationObservations(exchanges []exchange) []correlate.Observation {
	observations := make([]correlate.Observation, 0, len(exchanges))
	for index, ex := range exchanges {
		observation := correlate.Observation{
			ID:                     index + 1,
			RequestURL:             ex.URL,
			RequestBody:            ex.Body,
			RequestBodyTransformed: ex.BodyEncoding != "",
			RequestHeaders:         correlationObservationHeaders(ex.Headers, ex.HeaderList),
		}
		if ex.Response != nil {
			observation.ResponseBody = ex.Response.Body
			observation.ResponseHeaders = requestimport.ResponseContentBaselineHeaders(correlationObservationHeaders(ex.Response.Headers, ex.Response.HeaderList), ex.Response.ContentCapture)
		}
		observations = append(observations, observation)
	}
	return observations
}

func detectCorrelationsChecked(exchanges []exchange) ([]Correlation, error) {
	shared, err := correlate.DetectChecked(sampleCorrelationObservations(exchanges))
	if err != nil {
		return nil, err
	}
	return sampleCorrelations(shared), nil
}

// HAR review can retain bounded discoveries from incomplete analysis. These
// candidates must never be applied automatically unless analysis completed.
func detectHARCorrelationsForReview(exchanges []exchange) ([]Correlation, []string, error) {
	correlations, warnings, _, err := detectHARCorrelationsWithSettings(exchanges, nil)
	return correlations, warnings, err
}

// detectHARCorrelationsWithSettings detects correlations and applies a
// recording profile's correlation settings, when given. Static-asset bodies
// that do not fit the evidence budget are left out of the analysis; the
// returned omission says how many, so the import can note it.
func detectHARCorrelationsWithSettings(exchanges []exchange, settings *recordingprofile.CorrelationSettings) ([]Correlation, []string, correlate.StaticEvidenceOmission, error) {
	observations, omission := correlate.FitStaticAssetEvidence(sampleCorrelationObservations(exchanges))
	shared, warnings, err := correlate.DetectCheckedWithWarnings(observations)
	if err != nil {
		return nil, nil, omission, err
	}
	if settings != nil {
		shared = settings.ApplyCorrelation(observations, shared)
	}
	return sampleCorrelations(shared), warnings, omission, nil
}

func sampleCorrelations(shared []correlate.Correlation) []Correlation {
	correlations := make([]Correlation, 0, len(shared))
	for _, corr := range shared {
		if corr.Value == scrub.Redacted || corr.SampleValue == scrub.Redacted {
			continue
		}
		correlations = append(correlations, Correlation{
			Name:        corr.Name,
			Source:      corr.Source,
			Expression:  corr.Expression,
			ExtractType: corr.ExtractType,
			Convert:     corr.Convert,
			Value:       corr.Value,
			SampleValue: corr.SampleValue,
			FoundInStep: corr.FoundInID,
			UsedInSteps: corr.UsedInIDs,
			RuleID:      corr.RuleID,
			Confidence:  corr.Confidence,
			Reasons:     corr.Reasons,
		})
	}
	return correlations
}

// correlationObservationHeaders projects the authoritative ordered HeaderList
// into the newline-delimited repeated-value contract used by the correlation
// engine and HTTP extractor runtime. Import summary maps may comma-fold HAR
// duplicates or retain only one raw-HTTP occurrence, neither of which can
// identify a replay-side ordinal faithfully.
func correlationObservationHeaders(summary map[string]string, headers dsl.HeaderList) map[string]string {
	if len(headers) == 0 {
		return summary
	}
	result := cloneHeaders(summary)
	if result == nil {
		result = make(map[string]string)
	}
	type headerGroup struct {
		name   string
		values []string
	}
	groups := make(map[string]*headerGroup, len(headers))
	order := make([]string, 0, len(headers))
	for _, header := range headers {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		normalized := strings.ToLower(name)
		group := groups[normalized]
		if group == nil {
			group = &headerGroup{name: name}
			groups[normalized] = group
			order = append(order, normalized)
		}
		group.values = append(group.values, header.Value)
	}
	for _, normalized := range order {
		group := groups[normalized]
		for existing := range result {
			if strings.EqualFold(existing, group.name) {
				delete(result, existing)
			}
		}
		result[group.name] = strings.Join(group.values, "\n")
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// correlationValueUse is how a correlation's consumers use its value. raw and
// encoded are occurrences a rewrite may replace (see
// correlate.OccurrenceReplaceable); the contained flags count every
// occurrence, including ones inside a longer identifier or a URL's host,
// which collision analysis must still see.
type correlationValueUse struct {
	raw                bool
	encoded            bool
	rawContained       bool
	encodedContained   bool
	unsupportedEncoded bool
}

const manualCorrelationValueReplacement = "manual_value_replacement_required"

type correlationApplicationPlan struct {
	index int
	// reviewOnly mirrors the correlation's flag: the plan takes part in the
	// collision check but is never rewritten automatically.
	reviewOnly       bool
	value            string
	queryEncoded     string
	usage            correlationValueUse
	encodedConvert   string
	encodedSupported bool
	rawMapped        bool
	encodedMapped    bool
	encodedName      string
}

// prepareCorrelationApplication separates raw reuse from the exact QueryEscape
// spelling produced by url_encode. When the same response value is reused both
// ways, a second url_encode extractor/variable is required: one variable cannot
// simultaneously hold both spellings. Other encodings remain unchanged.
func prepareCorrelationApplication(exchanges []exchange, correlations []Correlation) (map[string]string, map[string]int, map[int][]dsl.Extractor) {
	return prepareCorrelationApplicationWithExtractionLimit(exchanges, correlations, protocol.MaxExtractionBytes, false)
}

// prepareIncompleteCorrelationApplication applies the correlations of an
// incomplete analysis. A value it skipped may contain an applied one, and
// collision analysis only compares applied values, so a spelling that occurs
// inside a longer identifier in a consumer request is held for review rather
// than rewritten in place.
func prepareIncompleteCorrelationApplication(exchanges []exchange, correlations []Correlation) (map[string]string, map[string]int, map[int][]dsl.Extractor) {
	return prepareCorrelationApplicationWithExtractionLimit(exchanges, correlations, protocol.MaxExtractionBytes, true)
}

func prepareCorrelationApplicationWithExtractionLimit(exchanges []exchange, correlations []Correlation, extractionLimit int, holdEmbedded bool) (map[string]string, map[string]int, map[int][]dsl.Extractor) {
	vars := make(map[string]string, len(correlations))
	sourceSteps := make(map[string]int, len(correlations))
	extraExtractors := make(map[int][]dsl.Extractor)
	usedNames := make(map[string]bool, len(correlations))
	plans := make([]correlationApplicationPlan, 0, len(correlations))
	sourceValidator := correlate.NewExactQueryEncodingValidator()

	for index := range correlations {
		corr := &correlations[index]
		usedNames[corr.Name] = true
		value := correlationConcreteValue(*corr)
		if value == "" {
			continue
		}
		usage := correlationValueUsage(exchanges, *corr, value)
		queryEncoded := correlate.QueryEncodedFormOf(value)
		encodedConvert, encodedConvertSupported := correlate.EncodedReuseExtractorConvert(corr.Convert)
		encodedConvertSupported = encodedConvertSupported &&
			correlate.CorrelationExtractionValueFits(corr.Name+"_encoded", queryEncoded)
		if encodedConvertSupported && strings.EqualFold(strings.TrimSpace(corr.Convert), "url_decode") {
			encodedConvertSupported = correlationSourceUsesExactQueryEncoding(sourceValidator, exchanges, *corr, queryEncoded)
		}
		plans = append(plans, correlationApplicationPlan{
			index:            index,
			reviewOnly:       corr.reviewOnly,
			value:            value,
			queryEncoded:     queryEncoded,
			usage:            usage,
			encodedConvert:   encodedConvert,
			encodedSupported: encodedConvertSupported,
		})
	}

	// A plan flagged here keeps ReviewAction even when a non-colliding spelling
	// of its value is still rewritten below and sets PreApplied=true: PreApplied
	// means "an extractor was attached and at least one supported consumer was
	// rewritten" (see Correlation in converter.go), while ReviewAction records
	// that another observed spelling still needs a manual replacement. The two
	// flags are complementary, not contradictory.
	ambiguousLiterals, ambiguousPlans := sampleCorrelationReplacementCollisions(exchanges, plans)
	for planIndex := range ambiguousPlans {
		correlations[plans[planIndex].index].ReviewAction = manualCorrelationValueReplacement
	}
	if holdEmbedded {
		for _, plan := range plans {
			for _, literal := range []string{plan.value, plan.queryEncoded} {
				if literal != "" && literalEmbeddedInConsumers(exchanges, correlations[plan.index].UsedInSteps, literal) {
					ambiguousLiterals[literal] = true
					correlations[plan.index].ReviewAction = manualCorrelationValueReplacement
				}
			}
		}
	}

	// Base extractors are materialized by converter.go after this planning pass,
	// while encoded siblings are returned in extraExtractors. Track both here so
	// every source action stays within the same ceiling enforced by DSL validation.
	sourceExtractorCounts := make(map[int]int)
	sourceExtractionBytes := make(map[int]int)
	for planIndex := range plans {
		plan := &plans[planIndex]
		corr := &correlations[plan.index]
		if corr.reviewOnly {
			// Below the automatic-rewrite confidence: the candidate stays
			// for review, after it took part in the collision check above.
			plan.rawMapped, plan.encodedMapped = false, false
			corr.PreApplied = false
			corr.ReviewAction = "manual_review_required"
			continue
		}
		if plan.usage.unsupportedEncoded || (plan.usage.encoded && !plan.encodedSupported) {
			corr.ReviewAction = manualCorrelationValueReplacement
		}

		// Ambiguous literals are omitted entirely. Overlapping claims can otherwise
		// rewrite a larger correlation with a smaller variable before its own source
		// extractor is available.
		plan.rawMapped = plan.usage.raw && !ambiguousLiterals[plan.value]
		plan.encodedMapped = plan.usage.encoded && plan.encodedSupported && plan.queryEncoded != "" && !ambiguousLiterals[plan.queryEncoded]
		requiredExtractors := 0
		if plan.rawMapped || plan.encodedMapped {
			requiredExtractors = 1
		}
		if plan.rawMapped && plan.encodedMapped {
			requiredExtractors++
		}
		if plan.encodedMapped {
			plan.encodedName = corr.Name
			if plan.rawMapped {
				plan.encodedName = claimCorrelationName(corr.Name+"_encoded", usedNames)
			}
		}
		if sourceExtractorCounts[corr.FoundInStep]+requiredExtractors > dsl.MaxActionExtractors {
			plan.rawMapped = false
			plan.encodedMapped = false
			corr.PreApplied = false
			corr.ReviewAction = manualCorrelationValueReplacement
			continue
		}
		if requiredExtractors == 0 {
			corr.PreApplied = false
			if corr.ReviewAction == "" {
				corr.ReviewAction = manualCorrelationValueReplacement
			}
			continue
		}
		requiredBytes := 0
		baseValue := plan.value
		if !plan.rawMapped {
			baseValue = plan.queryEncoded
		}
		baseBytes, baseFits := correlate.CorrelationExtractionEntryBytes(corr.Name, baseValue)
		if !baseFits {
			plan.rawMapped = false
			plan.encodedMapped = false
			corr.PreApplied = false
			corr.ReviewAction = manualCorrelationValueReplacement
			continue
		}
		requiredBytes += baseBytes
		if plan.rawMapped && plan.encodedMapped {
			encodedBytes, encodedFits := correlate.CorrelationExtractionEntryBytes(plan.encodedName, plan.queryEncoded)
			if !encodedFits {
				plan.rawMapped = false
				plan.encodedMapped = false
				corr.PreApplied = false
				corr.ReviewAction = manualCorrelationValueReplacement
				continue
			}
			requiredBytes += encodedBytes
		}
		if requiredBytes > extractionLimit || sourceExtractionBytes[corr.FoundInStep] > extractionLimit-requiredBytes {
			plan.rawMapped = false
			plan.encodedMapped = false
			corr.PreApplied = false
			corr.ReviewAction = manualCorrelationValueReplacement
			continue
		}
		sourceExtractorCounts[corr.FoundInStep] += requiredExtractors
		sourceExtractionBytes[corr.FoundInStep] += requiredBytes

		if plan.rawMapped {
			vars[plan.value] = "{{var." + corr.Name + "}}"
			sourceSteps[plan.value] = corr.FoundInStep
		}
		if plan.encodedMapped {
			vars[plan.queryEncoded] = "{{var." + plan.encodedName + "}}"
			sourceSteps[plan.queryEncoded] = corr.FoundInStep
			plan.encodedMapped = true
		}
		switch {
		case plan.encodedMapped && plan.rawMapped:
			corr.PreApplied = true
			corr.missDefault = plan.value
			onMiss, defaultValue := correlationMissPolicy(*corr, plan.queryEncoded)
			extraExtractors[corr.FoundInStep] = append(extraExtractors[corr.FoundInStep], dsl.Extractor{
				Name:         plan.encodedName,
				Source:       corr.Source,
				Type:         extractorTypeForCorrelation(*corr),
				Expression:   corr.Expression,
				Convert:      plan.encodedConvert,
				OnMiss:       onMiss,
				DefaultValue: defaultValue,
			})
		case plan.encodedMapped:
			corr.Convert = plan.encodedConvert
			corr.PreApplied = true
			corr.missDefault = plan.queryEncoded
		case plan.rawMapped:
			corr.PreApplied = true
			corr.missDefault = plan.value
		}
	}
	return vars, sourceSteps, extraExtractors
}

const maxSampleCorrelationReplacementSurfaces = 100_000

func sampleCorrelationReplacementCollisions(exchanges []exchange, plans []correlationApplicationPlan) (map[string]bool, map[int]bool) {
	return sampleCorrelationReplacementCollisionsWithSurfaceLimit(exchanges, plans, maxSampleCorrelationReplacementSurfaces)
}

func sampleCorrelationReplacementCollisionsWithSurfaceLimit(exchanges []exchange, plans []correlationApplicationPlan, surfaceLimit int) (map[string]bool, map[int]bool) {
	const comparisonLimit = 4_000_000

	type literalClaim struct {
		planIndex int
		literal   string
	}
	claims := make([]literalClaim, 0, len(plans)*2)
	for index, plan := range plans {
		seen := make(map[string]bool)
		add := func(literal string) {
			if literal == "" || seen[literal] {
				return
			}
			seen[literal] = true
			claims = append(claims, literalClaim{planIndex: index, literal: literal})
		}
		if plan.usage.raw || plan.usage.rawContained {
			add(plan.value)
		}
		if plan.usage.encoded || plan.usage.encodedContained {
			add(plan.queryEncoded)
		}
		if plan.usage.unsupportedEncoded {
			for _, spelling := range unsupportedCorrelationEncodings(plan.value, plan.queryEncoded) {
				add(spelling)
			}
		}
	}

	overlapBudget := paramize.NewLiteralOverlapSearchBudget()
	ambiguousLiterals := make(map[string]bool)
	ambiguousPlans := make(map[int]bool)
	markAllClaimsAmbiguous := func() {
		for _, claim := range claims {
			ambiguousLiterals[claim.literal] = true
			ambiguousPlans[claim.planIndex] = true
		}
	}
	// When a bound is exhausted at claims[firstUnchecked], every pair involving
	// an earlier claim has been compared, so those claims keep their outcome.
	// Plans owning any unchecked claim are suppressed whole.
	markUncheckedClaimsAmbiguous := func(firstUnchecked int) {
		unchecked := make(map[int]bool)
		for _, claim := range claims[firstUnchecked:] {
			unchecked[claim.planIndex] = true
		}
		for _, claim := range claims {
			if unchecked[claim.planIndex] {
				ambiguousLiterals[claim.literal] = true
				ambiguousPlans[claim.planIndex] = true
			}
		}
	}
	requestSurfaces, surfacesComplete := sampleRequestReplacementSurfaces(exchanges, surfaceLimit)
	if !surfacesComplete {
		// Collision analysis is safety-critical. A bounded prefix of editable
		// surfaces could miss an overlap and authorize a corrupting rewrite.
		markAllClaimsAmbiguous()
		return ambiguousLiterals, ambiguousPlans
	}
	occurrences := overlapBudget.NewLiteralOccurrenceIndexForSurfaces(requestSurfaces)
	comparisons := 0
	for leftIndex, left := range claims {
		for _, right := range claims[leftIndex+1:] {
			if left.planIndex == right.planIndex {
				continue
			}
			comparisons++
			if comparisons > comparisonLimit {
				markUncheckedClaimsAmbiguous(leftIndex)
				return ambiguousLiterals, ambiguousPlans
			}
			overlaps := overlapBudget.Contains(left.literal, right.literal) || overlapBudget.Contains(right.literal, left.literal)
			if overlaps && !overlapBudget.Exhausted() && containmentWithReviewOnly(plans[left.planIndex], left.literal, plans[right.planIndex], right.literal) {
				overlaps = false
			}
			if !overlaps && !overlapBudget.Exhausted() {
				overlaps = occurrences.PartiallyOverlap(left.literal, right.literal)
			}
			if overlapBudget.Exhausted() {
				markUncheckedClaimsAmbiguous(leftIndex)
				return ambiguousLiterals, ambiguousPlans
			}
			if !overlaps {
				continue
			}
			ambiguousLiterals[left.literal] = true
			ambiguousLiterals[right.literal] = true
			ambiguousPlans[left.planIndex] = true
			ambiguousPlans[right.planIndex] = true
		}
	}
	return ambiguousLiterals, ambiguousPlans
}

// containmentWithReviewOnly reports whether, of two different literals already
// known to contain one another, exactly one belongs to a review-only plan. A
// review-only plan is never rewritten automatically, so the containment is no
// collision either way:
//   - the review-only literal inside the applied one (a Salesforce org ID
//     inside an access token or session id) disappears when the longer literal
//     is replaced;
//   - the applied literal inside the review-only one (a Salesforce cshc inside
//     a verification redirect URL) is rewritten where it occurs, in that
//     longer literal too. The longer literal changes whenever the dynamic
//     value inside it does, so leaving it whole would be no less stale, and
//     holding the applied value would also leave its other consumers stale.
//
// One spelling both plans own stays a collision: the review-only owner keeps
// its literal, as in the native Salesforce builder. Partial overlaps are still
// checked by the caller.
func containmentWithReviewOnly(left correlationApplicationPlan, leftLiteral string, right correlationApplicationPlan, rightLiteral string) bool {
	return left.reviewOnly != right.reviewOnly && leftLiteral != rightLiteral
}

// collapseSameSourceDuplicateCorrelations keeps one correlation per value and
// source step. Two rules can recognize the same value in the same response: a
// Salesforce frontdoor sid is also a generic Location query parameter, one rule
// keeping the wire spelling and the other decoding it. Kept apart, one literal
// equals the other or its encoded spelling, so the collision check holds both
// for manual review and the value is never rewritten. The higher-confidence
// detection survives (the earlier one on a tie) and takes over the other's
// consumers, but only when it can rewrite every one of them. Otherwise both
// stay, as before, rather than leave a consumer silently stale.
func collapseSameSourceDuplicateCorrelations(exchanges []exchange, correlations []Correlation) []Correlation {
	if len(correlations) < 2 {
		return correlations
	}
	byStep := make(map[int][]int, len(correlations))
	out := make([]Correlation, 0, len(correlations))
	for _, corr := range correlations {
		merged := false
		for _, index := range byStep[corr.FoundInStep] {
			if !sameSourceDuplicateCorrelation(out[index], corr) {
				continue
			}
			winner, loser := out[index], corr
			if corr.Confidence > winner.Confidence {
				winner, loser = corr, out[index]
			}
			if !correlationServesSteps(exchanges, winner, loser.UsedInSteps) {
				continue
			}
			winner.UsedInSteps = mergeSortedUniqueInts(winner.UsedInSteps, loser.UsedInSteps)
			winner.UsedInEntries = mergeSortedUniqueInts(winner.UsedInEntries, loser.UsedInEntries)
			out[index] = winner
			merged = true
			break
		}
		if !merged {
			byStep[corr.FoundInStep] = append(byStep[corr.FoundInStep], len(out))
			out = append(out, corr)
		}
	}
	return out
}

// sameSourceDuplicateCorrelation reports whether two correlations extract the
// same value from the same response: the identical literal, or a decoded value
// and its exact query-encoded wire spelling.
func sameSourceDuplicateCorrelation(left, right Correlation) bool {
	if left.FoundInStep <= 0 || left.FoundInStep != right.FoundInStep {
		return false
	}
	leftValue, rightValue := correlationConcreteValue(left), correlationConcreteValue(right)
	if leftValue == "" || rightValue == "" {
		return false
	}
	return leftValue == rightValue ||
		decodedWireSpellingPair(left, leftValue, rightValue) ||
		decodedWireSpellingPair(right, rightValue, leftValue)
}

// decodedWireSpellingPair reports whether other is the exact query encoding of
// a url_decode correlation's decoded value.
func decodedWireSpellingPair(decoding Correlation, decoded, other string) bool {
	if !strings.EqualFold(strings.TrimSpace(decoding.Convert), "url_decode") {
		return false
	}
	encoded := correlate.QueryEncodedFormOf(decoded)
	return encoded != "" && encoded == other
}

// correlationServesSteps reports whether every step outside the correlation's
// own consumers carries its value where the planner rewrites it: a replaceable
// occurrence of the value itself or of its exact query encoding.
func correlationServesSteps(exchanges []exchange, corr Correlation, steps []int) bool {
	value := correlationConcreteValue(corr)
	encoded := correlate.QueryEncodedFormOf(value)
	for _, step := range steps {
		if slices.Contains(corr.UsedInSteps, step) {
			continue
		}
		if step <= 0 || step > len(exchanges) {
			return false
		}
		served := false
		for _, surface := range exchangeSurfaces(exchanges[step-1]) {
			if correlate.ContainsReplaceableOccurrence(surface, value) || (encoded != "" && correlate.ContainsReplaceableOccurrence(surface, encoded)) {
				served = true
				break
			}
		}
		if !served {
			return false
		}
	}
	return true
}

func mergeSortedUniqueInts(left, right []int) []int {
	if len(right) == 0 {
		return left
	}
	merged := make([]int, 0, len(left)+len(right))
	merged = append(merged, left...)
	merged = append(merged, right...)
	slices.Sort(merged)
	return slices.Compact(merged)
}

// sampleRequestReplacementSurfaces returns references to every field that
// sample conversion can actually rewrite. Keeping them separate avoids both a
// recording-sized aggregate copy and false overlap matches across field/action
// boundaries.
func sampleRequestReplacementSurfaces(exchanges []exchange, limit int) ([]string, bool) {
	if limit <= 0 || len(exchanges) > limit {
		return nil, false
	}
	surfaces := make([]string, 0, min(len(exchanges)*3, limit))
	seen := make(map[string]struct{}, cap(surfaces))
	add := func(surface string) bool {
		if surface == "" {
			return true
		}
		if _, exists := seen[surface]; exists {
			return true
		}
		if len(surfaces) >= limit {
			return false
		}
		seen[surface] = struct{}{}
		surfaces = append(surfaces, surface)
		return true
	}
	for _, ex := range exchanges {
		for _, surface := range exchangeSurfaces(ex) {
			if !add(surface) {
				return nil, false
			}
		}
	}
	return surfaces, true
}

// correlationVarsAvailableAtStep returns only variables whose extractor has
// already executed. Detection is intentionally based on response-to-later-
// request reuse, but a globally applied replacement map can still rewrite an
// incidental equal literal in an earlier request, producing a reference to a
// variable that does not exist yet.
func correlationVarsAvailableAtStep(vars map[string]string, sourceSteps map[string]int, step int) map[string]string {
	if len(vars) == 0 {
		return nil
	}
	available := make(map[string]string, len(vars))
	for literal, replacement := range vars {
		if sourceStep, ok := sourceSteps[literal]; ok && sourceStep < step {
			available[literal] = replacement
		}
	}
	return available
}

func correlationConcreteValue(corr Correlation) string {
	if corr.Value != "" {
		return corr.Value
	}
	return corr.SampleValue
}

func correlationValueUsage(exchanges []exchange, corr Correlation, value string) correlationValueUse {
	use := correlationValueUse{}
	queryEncoded := correlate.QueryEncodedFormOf(value)
	unsupportedEncoded := unsupportedCorrelationEncodings(value, queryEncoded)
	for _, step := range corr.UsedInSteps {
		if step <= 0 || step > len(exchanges) {
			continue
		}
		exchange := exchanges[step-1]
		if exchange.BodyEncoding != "" {
			if strings.Contains(exchange.Body, value) ||
				(queryEncoded != "" && strings.Contains(exchange.Body, queryEncoded)) {
				use.unsupportedEncoded = true
			}
			for _, representation := range unsupportedEncoded {
				if strings.Contains(exchange.Body, representation) {
					use.unsupportedEncoded = true
					break
				}
			}
		}
		for _, surface := range exchangeSurfaces(exchange) {
			if strings.Contains(surface, value) {
				use.rawContained = true
				use.raw = use.raw || correlate.ContainsReplaceableOccurrence(surface, value)
			}
			if queryEncoded != "" && strings.Contains(surface, queryEncoded) {
				use.encodedContained = true
				use.encoded = use.encoded || correlate.ContainsReplaceableOccurrence(surface, queryEncoded)
			}
			for _, representation := range unsupportedEncoded {
				if strings.Contains(surface, representation) {
					use.unsupportedEncoded = true
					break
				}
			}
		}
	}
	return use
}

func unsupportedCorrelationEncodings(value, queryEncoded string) []string {
	seen := map[string]bool{value: true, queryEncoded: true}
	forms := make([]string, 0, 8)
	appendForm := func(form string) {
		if form == "" || seen[form] {
			return
		}
		seen[form] = true
		forms = append(forms, form)
	}
	for _, form := range correlate.EncodedFormsOf(value) {
		appendForm(form)
		appendForm(lowerPercentEscapes(form))
	}
	appendForm(lowerPercentEscapes(queryEncoded))
	return forms
}

func lowerPercentEscapes(value string) string {
	encoded := []byte(value)
	for index := 0; index+2 < len(encoded); index++ {
		if encoded[index] != '%' {
			continue
		}
		for offset := 1; offset <= 2; offset++ {
			if encoded[index+offset] >= 'A' && encoded[index+offset] <= 'F' {
				encoded[index+offset] += 'a' - 'A'
			}
		}
		index += 2
	}
	return string(encoded)
}

// A url_decode extractor can be reused for a canonical query/form consumer by
// dropping its conversion only when the source already carries that exact wire
// spelling. For example, a source using %20 cannot safely feed a consumer using
// QueryEscape's '+' spelling even though both decode to the same value.
func correlationSourceUsesExactQueryEncoding(validator *correlate.ExactQueryEncodingValidator, exchanges []exchange, corr Correlation, queryEncoded string) bool {
	if queryEncoded == "" || corr.FoundInStep <= 0 || corr.FoundInStep > len(exchanges) {
		return false
	}
	source := exchanges[corr.FoundInStep-1].Response
	if source == nil {
		return false
	}
	return validator.Matches(corr.FoundInStep, source.Body, correlationObservationHeaders(source.Headers, source.HeaderList), correlate.Correlation{
		Source:      corr.Source,
		ExtractType: extractorTypeForCorrelation(corr),
		Expression:  corr.Expression,
	}, queryEncoded)
}

func claimCorrelationName(base string, used map[string]bool) string {
	if !used[base] {
		used[base] = true
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := base + "_" + strconv.Itoa(suffix)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func literalEmbeddedInConsumers(exchanges []exchange, steps []int, literal string) bool {
	for _, step := range steps {
		if step <= 0 || step > len(exchanges) {
			continue
		}
		for _, surface := range exchangeSurfaces(exchanges[step-1]) {
			if literalEmbedded(surface, literal) {
				return true
			}
		}
	}
	return false
}

// literalEmbedded reports whether literal occurs in text inside a longer
// identifier (see correlate.OccurrenceStandalone).
func literalEmbedded(text, literal string) bool {
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], literal)
		if index < 0 {
			return false
		}
		start := offset + index
		if !correlate.OccurrenceStandalone(text, start, start+len(literal)) {
			return true
		}
		offset = start + 1
	}
	return false
}

func exchangeSurfaces(ex exchange) []string {
	surfaces := []string{ex.URL}
	if ex.BodyEncoding == "" {
		surfaces = append(surfaces, ex.Body)
	}
	for name, value := range ex.Headers {
		if correlate.HeaderCorrelatable(name) {
			surfaces = append(surfaces, value)
		}
	}
	for _, header := range ex.HeaderList {
		if correlate.HeaderCorrelatable(header.Name) {
			surfaces = append(surfaces, header.Value)
		}
	}
	return surfaces
}

func cloneHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneHeaderList(input dsl.HeaderList) dsl.HeaderList {
	if len(input) == 0 {
		return nil
	}
	cloned := make(dsl.HeaderList, len(input))
	copy(cloned, input)
	return cloned
}

func cloneMetadata(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

// redactSourceReviewSecrets scrubs credentials from the preserved source text
// shown in the Source Comparison panel. This runs regardless of the
// sanitize_sensitive option: that option controls whether the generated
// actions keep raw values, not whether secrets persist in script metadata.
// Returns the set of file names whose content was redacted.
func redactSourceReviewSecrets(files map[string]string, exchanges []exchange) map[string]bool {
	redacted := make(map[string]bool, len(files))
	for name, content := range files {
		next, changed := redactExchangeSecrets(content, exchanges)
		if strings.HasSuffix(strings.ToLower(name), ".har") {
			if scrubbed, err := scrub.HAR([]byte(next), scrub.Policy{}); err == nil {
				next = string(scrubbed)
				changed = true
			}
		}
		if changed {
			files[name] = next
			redacted[name] = true
		}
	}
	return redacted
}

func redactExchangeSecrets(content string, exchanges []exchange) (string, bool) {
	original := content
	redactions := collectExchangeSecretRedactions(exchanges)
	replacements := make(map[string]string, 2*(len(redactions.headerValues)+len(redactions.bodyReplacements)))
	for _, replacement := range redactions.bodyReplacements {
		addRawAndJSONEscapedReplacement(replacements, replacement.body, replacement.sanitized)
	}
	// Header redaction wins if the same literal also appears in a sanitized body.
	for _, value := range redactions.headerValues {
		addRawAndJSONEscapedReplacement(replacements, value, "[REDACTED]")
	}
	// Apply every literal in one strings.Replacer-backed pass. Sequential
	// ReplaceAll calls can rescan "[REDACTED]" and expand it repeatedly when a
	// later short secret (for example "D") appears inside the marker.
	if len(replacements) > 0 {
		// Source review retains only MaxFileChars. Bound expanding substitutions
		// one byte beyond that display ceiling so even a one-byte credential
		// repeated throughout a large import cannot create a many-times-larger
		// transient string before sourceview applies its truncation marker.
		content, _ = correlate.ApplyVarsWithOutputLimit(content, replacements, sourceview.MaxFileChars+1)
	}
	// Catch-all for secret fields that did not round-trip through a parsed
	// exchange verbatim (re-serialized bodies, query strings, ...).
	content = paramize.SanitizeBodySecrets(content)
	return content, content != original
}

type exchangeSecretRedactions struct {
	headerValues     []string
	seenHeaderValues map[string]struct{}
	bodyReplacements []bodySecretReplacement
	seenBodies       map[string]struct{}
}

type bodySecretReplacement struct {
	body      string
	sanitized string
}

func collectExchangeSecretRedactions(exchanges []exchange) exchangeSecretRedactions {
	redactions := exchangeSecretRedactions{
		seenHeaderValues: make(map[string]struct{}),
		seenBodies:       make(map[string]struct{}),
	}
	for _, ex := range exchanges {
		redactions.addSensitiveHeaderValues(ex.Headers)
		redactions.addSensitiveHeaderListValues(ex.HeaderList)
		if ex.BodyEncoding == "" {
			redactions.addBody(ex.Body)
		}
		for _, value := range ex.SourceSecrets {
			if value == "" {
				continue
			}
			if _, exists := redactions.seenHeaderValues[value]; exists {
				continue
			}
			redactions.seenHeaderValues[value] = struct{}{}
			redactions.headerValues = append(redactions.headerValues, value)
		}
		redactions.addURL(ex.URL)
		if ex.Response != nil {
			redactions.addSensitiveHeaderValues(ex.Response.Headers)
			redactions.addSensitiveHeaderListValues(ex.Response.HeaderList)
			redactions.addBody(ex.Response.Body)
		}
	}
	return redactions
}

func (redactions *exchangeSecretRedactions) addURL(raw string) {
	if raw == "" {
		return
	}
	sanitized := scrub.ValueURL(raw, scrub.Policy{})
	if sanitized == raw {
		return
	}
	redactions.addBodyReplacement(raw, sanitized)
}

func (redactions *exchangeSecretRedactions) addSensitiveHeaderListValues(headers dsl.HeaderList) {
	for _, header := range headers {
		redactions.addSensitiveHeaderValue(header.Name, header.Value)
	}
}

func (redactions *exchangeSecretRedactions) addSensitiveHeaderValues(headers map[string]string) {
	for key, value := range headers {
		redactions.addSensitiveHeaderValue(key, value)
	}
}

func (redactions *exchangeSecretRedactions) addSensitiveHeaderValue(name, value string) {
	if !paramize.SensitiveHeaderKey(name) || strings.TrimSpace(value) == "" {
		return
	}
	if _, exists := redactions.seenHeaderValues[value]; exists {
		return
	}
	redactions.seenHeaderValues[value] = struct{}{}
	redactions.headerValues = append(redactions.headerValues, value)
}

func (redactions *exchangeSecretRedactions) addBody(body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	if _, exists := redactions.seenBodies[body]; exists {
		return
	}
	sanitized := paramize.SanitizeBodySecrets(body)
	if sanitized == body {
		return
	}
	redactions.addBodyReplacement(body, sanitized)
}

func (redactions *exchangeSecretRedactions) addBodyReplacement(body, sanitized string) {
	if body == "" || body == sanitized {
		return
	}
	if _, exists := redactions.seenBodies[body]; exists {
		return
	}
	redactions.seenBodies[body] = struct{}{}
	redactions.bodyReplacements = append(redactions.bodyReplacements, bodySecretReplacement{
		body:      body,
		sanitized: sanitized,
	})
}

func redactSensitiveHeaderValues(content string, headers map[string]string) string {
	for key, value := range headers {
		if !paramize.SensitiveHeaderKey(key) || strings.TrimSpace(value) == "" {
			continue
		}
		content = replaceRawAndJSONEscaped(content, value, "[REDACTED]")
	}
	return content
}

func redactBodySecrets(content string, body string) string {
	if strings.TrimSpace(body) == "" {
		return content
	}
	sanitized := paramize.SanitizeBodySecrets(body)
	if sanitized == body {
		return content
	}
	return replaceRawAndJSONEscaped(content, body, sanitized)
}

// replaceRawAndJSONEscaped replaces both the literal value and its
// JSON-string-escaped form, so redaction reaches secrets embedded inside JSON
// containers such as HAR header entries and postData text.
func replaceRawAndJSONEscaped(content string, value string, replacement string) string {
	content = strings.ReplaceAll(content, value, replacement)
	escapedValue := jsonEscapeString(value)
	if escapedValue != "" && escapedValue != value {
		content = strings.ReplaceAll(content, escapedValue, jsonEscapeString(replacement))
	}
	return content
}

func addRawAndJSONEscapedReplacement(replacements map[string]string, value, replacement string) {
	if value == "" {
		return
	}
	replacements[value] = replacement
	escapedValue := jsonEscapeString(value)
	if escapedValue != "" && escapedValue != value {
		replacements[escapedValue] = jsonEscapeString(replacement)
	}
}

// jsonEscapeString returns the JSON string encoding of value without the
// surrounding quotes and without HTML escaping, matching how HAR files embed
// string values.
func jsonEscapeString(value string) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return ""
	}
	encoded := strings.TrimRight(buf.String(), "\n")
	if len(encoded) < 2 {
		return ""
	}
	return encoded[1 : len(encoded)-1]
}

func applyCorrelationVars(value string, vars map[string]string) string {
	return correlate.ApplyVars(value, vars)
}

func applyCorrelationVarsToHeaders(headers map[string]string, vars map[string]string) map[string]string {
	return correlate.ApplyVarsToHeaders(headers, vars)
}

func applyCorrelationVarsToHeaderList(headers dsl.HeaderList, vars map[string]string) dsl.HeaderList {
	if len(headers) == 0 {
		return headers
	}
	for index := range headers {
		if correlate.HeaderCorrelatable(headers[index].Name) {
			headers[index].Value = correlate.ApplyVars(headers[index].Value, vars)
		}
	}
	return headers
}

// correlationMissPolicy is how a correlation's extractor treats a miss. A
// high-confidence dynamic value fails the step, so a broken correlation
// surfaces at once. A less certain one may be static content that the page
// no longer carries, so a miss keeps the recorded value its variable
// replaced: replay then sends what the recording did.
func correlationMissPolicy(corr Correlation, recorded string) (string, *string) {
	if correlate.HighConfidence(correlate.Correlation{Confidence: corr.Confidence}) || recorded == "" || len(recorded) > httpprotocol.MaxExtractorExpressionBytes {
		return "fail", nil
	}
	return "default", &recorded
}

func extractorTypeForCorrelation(corr Correlation) string {
	if corr.ExtractType != "" {
		return corr.ExtractType
	}
	return extractorTypeForSource(corr.Source)
}

func extractorTypeForSource(source string) string {
	if source == "header" {
		return "header"
	}
	return "jsonpath"
}

func sanitizeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "value"
	}
	replacer := regexp.MustCompile(`[^A-Za-z0-9_]+`)
	value = replacer.ReplaceAllString(strings.ToLower(value), "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "value"
	}
	return value
}
