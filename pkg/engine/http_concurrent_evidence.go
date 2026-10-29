package engine

import (
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	"reflect"
	"strings"
)

// Fixed source-ordinal allowances make completed-result retention independent
// of completion order. Rich body/snapshot data and private exchanges each have
// a 16 MiB group ceiling. The existing protocol-evidence (128 KiB), extraction
// (64 KiB), error (16 KiB) and metadata limits also apply to every child. Active
// requests still use the HTTP adapter's response ceiling, bounded by fanout.
const maxConcurrentRichEvidenceBytes = 16 << 20
const maxConcurrentCorrelationBytes = 16 << 20
const concurrentCorrelationOmitted = "raw correlation evidence omitted by concurrent group retention limit"

func boundConcurrentCompletedResult(result *protocol.Result, count int, config *RedactionConfig, observed ...[]string) (*RedactionConfig, error) {
	// Register only bounded, sensitively named outputs in a detached worker
	// matcher. Runtime variable publication remains owned by the parent after
	// the join. Exact outputs and private exchanges bypass public projection.
	if result != nil && (result.ExtractionsIncomplete || protocol.ValidateExtractions(result.Extractions) != nil) {
		// Once any outputs were discarded, no complete sensitive-value matcher
		// can be established. Do not classify only the surviving prefix/map.
		omitConcurrentPublicEvidence(result)
		result.Extractions = nil
		result.ExtractionsIncomplete = true
		result.Success = false
		result.ErrorMessage = "concurrent HTTP extraction retention failed"
		result.CorrelationEvidence = protocol.UnavailableCorrelationEvidence("concurrent HTTP credential projection unavailable")
		return nil, errors.New("concurrent HTTP publication redaction failed")
	}
	var values []string
	if result != nil {
		values = concurrentSensitiveExtractionValues(result.Extractions)
	}
	for _, history := range observed {
		values = append(values, history...)
	}
	matcher, err := WithSecretValueRedactionChecked(nil, values)
	if err == nil {
		config, err = attachConcurrentRuntimeMatcher(config, matcher)
	}
	if err != nil {
		if result == nil {
			return nil, errors.New("concurrent HTTP publication redaction failed")
		}
		omitConcurrentPublicEvidence(result)
		result.Success = false
		result.Extractions = nil
		result.ExtractionsIncomplete = true
		result.CorrelationEvidence = protocol.UnavailableCorrelationEvidence("concurrent HTTP credential projection unavailable")
		return nil, errors.New("concurrent HTTP publication redaction failed")
	}
	// A later transport error can discard the final result while an earlier
	// attempt still supplies classifications needed to project the error text.
	if result == nil {
		return config, nil
	}
	count = max(1, count)
	allowance := maxConcurrentRichEvidenceBytes / count
	bodyLimit, snapshotLimit := allowance/2, allowance/2
	preOmitted, preOmittedBodyBytes := false, 0
	preOmittedScreenshot := false
	// With no exact matcher or custom rule, an oversized graph cannot shrink
	// into the retained allowance. Drop it whole before the generic scrubber:
	// discarded bytes expose no prefixes and need no expensive projection.
	if !hasRuntimeSecretReplacers(config) && (config == nil || len(config.compiled) == 0) {
		if len(result.ResponseBody) > min(bodyLimit/2, maxRetainedResultEvidenceBytes/2) {
			preOmitted, preOmittedBodyBytes = true, len(result.ResponseBody)
			preOmittedScreenshot = result.ScreenshotBase64 != "" || result.ScreenshotOmitted
			clearConcurrentRichEvidence(result)
		}
		if result.Snapshot != nil && len(result.Snapshot.ResponseBody) > min(snapshotLimit/2, defaultErrorSnapshotMaxBytes/2) {
			preOmitted = true
			preOmittedScreenshot = preOmittedScreenshot || result.Snapshot.ScreenshotBase64 != "" || result.Snapshot.ScreenshotOmitted
			result.Snapshot = nil
		}
	}
	scrubConcurrentPublicResult(result, config)
	if preOmittedScreenshot && !config.HasRuntimeSecretMaterial() {
		result.ScreenshotOmitted = true
	}
	metadataPointers := concurrentPublicStringPointers(result)
	metadataBefore := make([]string, len(metadataPointers))
	for i, field := range metadataPointers {
		metadataBefore[i] = *field
	}
	originalBodyBytes := preOmittedBodyBytes + len(result.ResponseBody)
	originalRich := protocol.ErrorSnapshot{ResponseBody: result.ResponseBody, PageURL: result.PageURL, PageTitle: result.PageTitle, HTML: result.HTML, ScreenshotBase64: result.ScreenshotBase64, ConsoleEntries: result.ConsoleEntries, NetworkEntries: result.NetworkEntries, DOMSummary: result.DOMSummary}
	originalProtocolEvidence := result.ProtocolEvidence
	originalError := result.ErrorMessage
	rich, _ := boundedErrorSnapshot(&protocol.ErrorSnapshot{ResponseBody: result.ResponseBody, PageURL: result.PageURL, PageTitle: result.PageTitle, HTML: result.HTML, ScreenshotBase64: result.ScreenshotBase64, ConsoleEntries: result.ConsoleEntries, NetworkEntries: result.NetworkEntries, DOMSummary: result.DOMSummary}, bodyLimit)

	result.ResponseBody = ""
	result.PageURL = ""
	result.PageTitle = ""
	result.HTML = ""
	result.ScreenshotBase64 = ""
	result.ConsoleEntries = nil
	result.NetworkEntries = nil
	result.DOMSummary = nil
	if rich != nil {
		result.ResponseBody = rich.ResponseBody
		result.PageURL = rich.PageURL
		result.PageTitle = rich.PageTitle
		result.HTML = rich.HTML
		result.ScreenshotBase64 = rich.ScreenshotBase64
		result.ScreenshotOmitted = result.ScreenshotOmitted || rich.ScreenshotOmitted
		result.ConsoleEntries = rich.ConsoleEntries
		result.NetworkEntries = rich.NetworkEntries
		result.DOMSummary = rich.DOMSummary
	}
	originalSnapshot := result.Snapshot
	result.Snapshot, _ = boundedErrorSnapshot(originalSnapshot, snapshotLimit)

	correlationOmitted := false
	if evidence := result.CorrelationEvidence; evidence != nil {
		if evidence.UnavailableReason != "" {
			correlationOmitted = true
			result.CorrelationEvidence = protocol.UnavailableCorrelationEvidence(cloneBoundedResultString(evidence.UnavailableReason, maxResultErrorBytes))
		} else if !concurrentCorrelationFits(evidence, maxConcurrentCorrelationBytes/count) {
			result.CorrelationEvidence = protocol.UnavailableCorrelationEvidence(concurrentCorrelationOmitted)
			correlationOmitted = true
		} else {
			detached := *evidence
			detached.RequestURL = strings.Clone(evidence.RequestURL)
			detached.RequestBody = strings.Clone(evidence.RequestBody)
			detached.AuthoredRequestURL = strings.Clone(evidence.AuthoredRequestURL)
			detached.AuthoredRequestBody = strings.Clone(evidence.AuthoredRequestBody)
			detached.ResponseBody = strings.Clone(evidence.ResponseBody)
			detached.RequestHeaders = detachConcurrentStrings(evidence.RequestHeaders)
			detached.AuthoredRequestHeaders = detachConcurrentStrings(evidence.AuthoredRequestHeaders)
			detached.ResponseHeaders = detachConcurrentStrings(evidence.ResponseHeaders)
			if evidence.ResponseSuccess != nil {
				value := *evidence.ResponseSuccess
				detached.ResponseSuccess = &value
			}
			result.CorrelationEvidence = &detached
		}
	}
	// Validate before copying: runtime extraction values must remain exact, while
	// a tiny extracted substring must not retain its original large body backing.
	if protocol.ValidateExtractions(result.Extractions) != nil {
		result.Extractions = nil
		result.ExtractionsIncomplete = true
		result.Success = false
		result.ErrorMessage = "concurrent HTTP extraction retention failed"
	} else {
		result.Extractions = detachConcurrentStrings(result.Extractions)
	}
	boundResultEvidence(result)
	metadataComplete := true
	for i, field := range metadataPointers {
		if *field != metadataBefore[i] {
			// A later sibling may introduce a new sensitive value. Never retain
			// a prefix that its exact matcher can no longer recognize.
			*field = ""
			metadataComplete = false
		}
	}
	if result.ErrorMessage == "" && len(originalError) > maxResultErrorBytes {
		result.ErrorMessage = "concurrent HTTP error evidence omitted by retention limit"
	}
	finalRich := protocol.ErrorSnapshot{ResponseBody: result.ResponseBody, PageURL: result.PageURL, PageTitle: result.PageTitle, HTML: result.HTML, ScreenshotBase64: result.ScreenshotBase64, ConsoleEntries: result.ConsoleEntries, NetworkEntries: result.NetworkEntries, DOMSummary: result.DOMSummary}
	// Comparing with the final bounded graph also accounts for independent
	// per-field limits, not just aggregate bytes or collection cardinality.
	richComplete := reflect.DeepEqual(originalRich, finalRich)
	snapshotComplete := reflect.DeepEqual(originalSnapshot, result.Snapshot)
	protocolComplete := reflect.DeepEqual(originalProtocolEvidence, result.ProtocolEvidence)
	if !richComplete {
		if originalRich.ScreenshotBase64 != "" {
			result.ScreenshotOmitted = true
		}
		clearConcurrentRichEvidence(result)
	}
	if !snapshotComplete {
		if result.ScreenshotBase64 == "" && originalSnapshot != nil && (originalSnapshot.ScreenshotBase64 != "" || originalSnapshot.ScreenshotOmitted) {
			result.ScreenshotOmitted = true
		}
		result.Snapshot = nil
	}
	if !protocolComplete {
		result.ProtocolEvidence = nil
	}
	complete := !preOmitted && richComplete && snapshotComplete && protocolComplete && metadataComplete
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = make(map[string]any)
	}
	result.ProtocolEvidence["concurrent_group_evidence_bounded"] = true
	result.ProtocolEvidence["concurrent_group_evidence_details_complete"] = complete && !correlationOmitted
	result.ProtocolEvidence["concurrent_group_response_bytes_omitted"] = max(0, originalBodyBytes-len(result.ResponseBody))
	result.ProtocolEvidence["concurrent_group_correlation_omitted"] = correlationOmitted
	return config, nil
}
func detachConcurrentStrings(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[strings.Clone(key)] = strings.Clone(value)
	}
	return out
}
func concurrentCorrelationFits(e *protocol.CorrelationEvidence, remaining int) bool {
	remaining -= 512
	for _, value := range []string{e.RequestURL, e.RequestBody, e.AuthoredRequestURL, e.AuthoredRequestBody, e.ResponseBody} {
		if len(value) > remaining {
			return false
		}
		remaining -= len(value)
	}
	for _, values := range []map[string]string{e.RequestHeaders, e.AuthoredRequestHeaders, e.ResponseHeaders} {
		if len(values) > 256 {
			return false
		}
		for key, value := range values {
			cost := 32 + len(key) + len(value)
			if cost > remaining {
				return false
			}
			remaining -= cost
		}
	}
	return remaining >= 0
}

// Public projection must run before any size limit. Preserve the exact outputs
// needed by the next action and the replay-only raw exchange independently.
func scrubConcurrentPublicResult(result *protocol.Result, config *RedactionConfig) {
	if result == nil {
		return
	}
	extractions, correlation := result.Extractions, result.CorrelationEvidence
	result.Extractions, result.CorrelationEvidence = nil, nil
	scrubSensitiveStructuredResult(result, config)
	scrubSensitiveResultWithOptions(result, config, true)
	result.Extractions, result.CorrelationEvidence = extractions, correlation
}

func concurrentSensitiveExtractionValues(values map[string]string) []string {
	var out []string
	for key, value := range values {
		if value != "" && scrub.SensitiveName(key) {
			out = append(out, value)
		}
	}
	return out
}

// Reflect only fixed typed scalar fields, never adapter-controlled graph nodes.
// This also includes future metadata strings added to these fixed structures.
func concurrentPublicStringPointers(result *protocol.Result) []*string {
	var out []*string
	for _, source := range []any{result, result.ClientMetrics, result.ServerTelemetry} {
		v := reflect.ValueOf(source)
		if v.Kind() != reflect.Pointer || v.IsNil() {
			continue
		}
		v = v.Elem()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if field.Kind() == reflect.String && field.CanAddr() && field.CanInterface() {
				out = append(out, field.Addr().Interface().(*string))
			}
		}
	}
	return out
}

func clearConcurrentRichEvidence(result *protocol.Result) {
	result.ResponseBody, result.PageURL, result.PageTitle, result.HTML, result.ScreenshotBase64 = "", "", "", "", ""
	result.ConsoleEntries, result.NetworkEntries, result.DOMSummary = nil, nil, nil
}

// If a complete group matcher cannot be established, retain numeric outcomes,
// exact extraction state and the separately bounded private exchange only.
func omitConcurrentPublicEvidence(result *protocol.Result) {
	if result == nil {
		return
	}
	originalError := result.ErrorMessage
	result.ScreenshotOmitted = false
	// A result carrying byte evidence keeps it and its protocol, so its byte
	// counts stay measurements of their population (NA-C). Only adapters
	// with constant dependency types (HTTP, gRPC, WebSocket, and the protocols
	// wrapping HTTP) set byte evidence; every other dependency type, which an
	// action parameter can shape, is cleared.
	dependency, byteEvidence := result.DependencyType, result.ByteEvidence
	if byteEvidence == protocol.ByteEvidenceGRPCUncompressedPayload && result.ServerTelemetry != nil && result.ServerTelemetry.GRPC != nil && result.ServerTelemetry.GRPC.Valid() {
		result.BytesSent = result.ServerTelemetry.GRPC.RequestBytes
		result.BytesReceived = result.ServerTelemetry.GRPC.ResponseBytes
	}
	for _, field := range concurrentPublicStringPointers(result) {
		*field = ""
	}
	if byteEvidence != "" {
		result.DependencyType, result.ByteEvidence = dependency, byteEvidence
	}
	if originalError != "" {
		result.ErrorMessage = "concurrent HTTP public evidence unavailable"
	}
	clearConcurrentRichEvidence(result)
	result.ClientMetrics, result.ServerTelemetry, result.Snapshot = nil, nil, nil
	result.PropertyDimensions, result.CSVExportIDs, result.WarningCodes = nil, nil, nil
	result.ProtocolEvidence = map[string]any{"concurrent_group_evidence_bounded": true, "concurrent_group_evidence_details_complete": false}
}

// Final outputs are bounded to 64 KiB; observed retry values have a separate
// cumulative group ceiling. Apply both sensitive subsets to every sibling's
// remaining complete public evidence before any source-ordered publication.
func scrubConcurrentJoinedEvidence(children []preparedHTTPChild, outputs map[string]string, observed ...[]string) error {
	values := concurrentSensitiveExtractionValues(outputs)
	for _, history := range observed {
		values = append(values, history...)
	}
	matcher, err := WithSecretValueRedactionChecked(nil, values)
	if err != nil {
		omitConcurrentJoinedPublicEvidence(children)
		return errors.New("concurrent HTTP publication redaction failed")
	}
	cache := make(map[*RedactionConfig]*RedactionConfig)
	for i := range children {
		child := &children[i]
		config, found := cache[child.redaction]
		if !found {
			config, err = attachConcurrentRuntimeMatcher(child.redaction, matcher)
			if err == nil {
				cache[child.redaction] = config
			}
		}
		if err != nil {
			omitConcurrentJoinedPublicEvidence(children)
			return errors.New("concurrent HTTP publication redaction failed")
		}
		scrubConcurrentPublicResult(child.result, config)
		if child.err != nil {
			child.err = errors.New(scrubSensitiveTextWithConfig(child.err.Error(), config))
		}
		child.redaction = config
	}
	return nil
}

func omitConcurrentJoinedPublicEvidence(children []preparedHTTPChild) {
	for i := range children {
		omitConcurrentPublicEvidence(children[i].result)
		children[i].publicEvidenceUnavailable = true
		if children[i].err != nil {
			children[i].err = errors.New("concurrent HTTP child error evidence unavailable")
		}
	}
}

func (vu *VUser) resultPublicationRedactionConfig() *RedactionConfig {
	if vu.runtimeCredentialRedactionErr != nil {
		return unavailableRuntimePublicationConfig
	}
	if vu.concurrentPublicationRedaction != nil {
		return vu.concurrentPublicationRedaction
	}
	return vu.RedactionConfig
}

// Fresh matcher graphs are immutable. Workers attach only their bounded current
// output matcher to the shared inherited base, and the join attaches one shared
// group matcher. Neither path flattens a large inherited matcher per sibling.
func attachConcurrentRuntimeMatcher(base, matcher *RedactionConfig) (*RedactionConfig, error) {
	if matcher == nil {
		return base, nil
	}
	depth := 0
	for current := base; current != nil; current = current.runtimeParent {
		depth++
		if depth >= maxRuntimeSecretRedactionLayers {
			return nil, errors.New("concurrent HTTP publication redaction failed")
		}
	}
	count, bytes := concurrentRuntimeValueTotals(base)
	for value := range matcher.runtimeSecretValues {
		if concurrentRuntimeValuePresent(base, value) {
			continue
		}
		if count+1 > maxRuntimeSecretRedactionValues || len(value) > maxRuntimeSecretRedactionRawBytes-bytes {
			return nil, errors.New("concurrent HTTP publication redaction failed")
		}
		count++
		bytes += len(value)
	}
	if base == nil {
		return matcher, nil
	}
	out := cloneRedactionPolicy(base)
	out.secretReplacers, out.runtimeSecretValues, out.runtimeSecretVariants = matcher.secretReplacers, matcher.runtimeSecretValues, matcher.runtimeSecretVariants
	out.runtimeSecretRawBytes, out.runtimePatternBytes, out.runtimeMatcherComplete = matcher.runtimeSecretRawBytes, matcher.runtimePatternBytes, matcher.runtimeMatcherComplete
	out.runtimeScriptSourcedValues, out.runtimeParent = matcher.runtimeScriptSourcedValues, base
	return out, nil
}

// Constructor-owned root accounting is immutable. Only the small overlay maps
// need inspection; inherited membership is checked in place, never flattened.
func concurrentRuntimeValueTotals(config *RedactionConfig) (count, bytes int) {
	if config == nil {
		return 0, 0
	}
	if config.runtimeParent == nil {
		return len(config.runtimeSecretValues), config.runtimeSecretRawBytes
	}
	count, bytes = concurrentRuntimeValueTotals(config.runtimeParent)
	for value := range config.runtimeSecretValues {
		if !concurrentRuntimeValuePresent(config.runtimeParent, value) {
			count++
			bytes += len(value)
		}
	}
	return count, bytes
}

func concurrentRuntimeValuePresent(config *RedactionConfig, value string) bool {
	for depth := 0; config != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		if _, exists := config.runtimeSecretValues[value]; exists {
			return true
		}
		config = config.runtimeParent
	}
	return false
}

func (vu *VUser) resultPublicationUnavailable() bool {
	return vu.concurrentPublicationUnavailable || vu.runtimeCredentialRedactionErr != nil
}
