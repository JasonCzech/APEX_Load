package sample

// correlate_redact.go — split from converter.go (same package, behavior-preserving).

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
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
			observation.ResponseHeaders = correlationObservationHeaders(ex.Response.Headers, ex.Response.HeaderList)
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
	shared, warnings, err := correlate.DetectCheckedWithWarnings(sampleCorrelationObservations(exchanges))
	if err != nil {
		return nil, nil, err
	}
	return sampleCorrelations(shared), warnings, nil
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

type correlationValueUse struct {
	raw                bool
	encoded            bool
	unsupportedEncoded bool
}

const manualCorrelationValueReplacement = "manual_value_replacement_required"

type correlationApplicationPlan struct {
	index            int
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
	return prepareCorrelationApplicationWithExtractionLimit(exchanges, correlations, protocol.MaxExtractionBytes)
}

func prepareCorrelationApplicationWithExtractionLimit(exchanges []exchange, correlations []Correlation, extractionLimit int) (map[string]string, map[string]int, map[int][]dsl.Extractor) {
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

	// Base extractors are materialized by converter.go after this planning pass,
	// while encoded siblings are returned in extraExtractors. Track both here so
	// every source action stays within the same ceiling enforced by DSL validation.
	sourceExtractorCounts := make(map[int]int)
	sourceExtractionBytes := make(map[int]int)
	for planIndex := range plans {
		plan := &plans[planIndex]
		corr := &correlations[plan.index]
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
			extraExtractors[corr.FoundInStep] = append(extraExtractors[corr.FoundInStep], dsl.Extractor{
				Name:       plan.encodedName,
				Source:     corr.Source,
				Type:       extractorTypeForCorrelation(*corr),
				Expression: corr.Expression,
				Convert:    plan.encodedConvert,
				OnMiss:     "fail",
			})
		case plan.encodedMapped:
			corr.Convert = plan.encodedConvert
			corr.PreApplied = true
		case plan.rawMapped:
			corr.PreApplied = true
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
		if plan.usage.raw {
			add(plan.value)
		}
		if plan.usage.encoded {
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
	requestSurfaces, surfacesComplete := sampleRequestReplacementSurfaces(exchanges, surfaceLimit)
	if !surfacesComplete {
		// Collision analysis is safety-critical. A bounded prefix of editable
		// surfaces could miss an overlap and authorize a corrupting rewrite.
		markAllClaimsAmbiguous()
		return ambiguousLiterals, ambiguousPlans
	}
	comparisons := 0
	for leftIndex, left := range claims {
		for _, right := range claims[leftIndex+1:] {
			if left.planIndex == right.planIndex {
				continue
			}
			comparisons++
			if comparisons > comparisonLimit {
				// Collision analysis is safety-critical: when the bounded comparison
				// budget is exhausted, suppress every automatic claim rather than
				// returning a partially checked replacement map.
				markAllClaimsAmbiguous()
				return ambiguousLiterals, ambiguousPlans
			}
			overlaps := overlapBudget.Contains(left.literal, right.literal) || overlapBudget.Contains(right.literal, left.literal)
			if !overlaps {
				for _, surface := range requestSurfaces {
					overlaps = overlapBudget.LiteralsPartiallyOverlapInSurface(surface, left.literal, right.literal)
					if overlapBudget.Exhausted() || overlaps {
						break
					}
				}
			}
			if overlapBudget.Exhausted() {
				markAllClaimsAmbiguous()
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
				use.raw = true
			}
			if queryEncoded != "" && strings.Contains(surface, queryEncoded) {
				use.encoded = true
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

func exchangeSurfaces(ex exchange) []string {
	surfaces := []string{ex.URL}
	if ex.BodyEncoding == "" {
		surfaces = append(surfaces, ex.Body)
	}
	for _, value := range ex.Headers {
		surfaces = append(surfaces, value)
	}
	for _, header := range ex.HeaderList {
		surfaces = append(surfaces, header.Value)
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
		headers[index].Value = correlate.ApplyVars(headers[index].Value, vars)
	}
	return headers
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
