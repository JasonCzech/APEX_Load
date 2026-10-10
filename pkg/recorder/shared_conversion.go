package recorder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// A proxy recording converts through the same pipeline as a HAR import and
// the browser recorder's HTTP script (pkg/convert/sample): the session is
// written as an in-memory HAR and converted there, so request, redirect,
// body, header, correlation, async and WebSocket rules land once for every
// recorded source. The recorder keeps what only it knows: which headers a
// recording keeps (headerPolicy), the step, transaction and think-time edits
// a tester made, its own static classification, and the mapping from HAR
// entries back to its request sequence numbers.

// harNoteForRecordings is a HAR-import note that does not apply to a
// recording, whose asset classification travels with it.
const harNoteForRecordings = "HAR import preserves inter-request think time when entry timestamps are available, but browser-specific asset metadata is not preserved"

var harEntryLocation = regexp.MustCompile(`^HAR entry (\d+)$`)

// convertThroughSharedPipeline converts the HTTP traffic of a recording.
func convertThroughSharedPipeline(session *Session, name string, opts ConvertOptions, policy headerPolicy, result *ConvertResult) {
	content, entrySeqs, err := conversionHAR(session, policy)
	if err != nil {
		result.Warnings = append(result.Warnings, "recording conversion was not completed: "+err.Error())
		result.Script = emptyRecordingScript(session, name)
		return
	}
	sanitize := false
	converted, err := sample.Convert(sample.ImportRequest{
		Name:    name,
		Format:  "har",
		Content: string(content),
		Options: sample.ConvertOptions{
			BaseURL:           session.BaseURL,
			SanitizeSensitive: &sanitize,
			PageTitleChecks:   opts.PageTitleChecks,
			ResourceMode:      opts.ResourceMode,
			AsyncScan:         opts.AsyncScan,
			Profile:           opts.Profile,
			OmitSourceReview:  true,
			// As before convergence, a recording rewrites only
			// high-confidence correlations.
			HighConfidenceOnly: true,
		},
	})
	if err != nil {
		result.Warnings = append(result.Warnings, "recording conversion produced no requests: "+err.Error())
		result.Script = emptyRecordingScript(session, name)
		return
	}
	seqOf := func(entry int) int {
		if entry < 1 || entry > len(entrySeqs) {
			return 0
		}
		return entrySeqs[entry-1]
	}

	script := converted.Script
	script.GeneratedBy = "recorder"
	script.GeneratedFrom = "proxy_recording"
	// The save workflow and baseline attestation identify a recording by
	// its import source.
	ensureRecorderImportPreserved(script, session.Name)
	script.Metadata.Import.Source = "recorder"
	for index := range script.Actions {
		// HAR page and timing notes say nothing about a proxy recording.
		delete(script.Actions[index].Metadata, "har.pageref")
		delete(script.Actions[index].Metadata, "har.timings")
		if len(script.Actions[index].Metadata) == 0 {
			script.Actions[index].Metadata = nil
		}
	}
	// Recording lineage hashes the exact action graph, and a draft comes
	// back from the client as JSON: typed values inside metadata and params
	// maps must already be in their JSON form, or the hashes differ.
	if normalized, err := jsonNormalizedActions(script.Actions); err == nil {
		script.Actions = normalized
	}
	for _, warning := range converted.Warnings {
		if warning != harNoteForRecordings {
			result.Warnings = append(result.Warnings, warning)
		}
	}
	for _, item := range converted.Unsupported {
		if item.Severity == diag.Info && item.Code == sample.OriginAdvertisesH3Code {
			result.Diagnostics = append(result.Diagnostics, item)
			continue
		}
		if item.Severity != diag.Warning && item.Severity != diag.Blocker {
			continue
		}
		if item.Severity == diag.Warning && (item.Element == "HTTP version" || item.Element == "HAR correlation analysis incomplete" || item.Code == sample.ClientTimestampsDiagnosticCode) {
			// A proxy capture without a replayable version keeps the
			// script's policy, and incomplete analysis and fresh client
			// timestamps are already warnings.
			continue
		}
		where := item.Location
		if match := harEntryLocation.FindStringSubmatch(where); match != nil {
			entry, _ := strconv.Atoi(match[1])
			where = fmt.Sprintf("recorded request %d", seqOf(entry))
		}
		if where != "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s (%s): %s", item.Element, where, item.Detail))
		} else {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %s", item.Element, item.Detail))
		}
	}

	correlations := make([]Correlation, 0, len(converted.Correlations))
	for _, corr := range converted.Correlations {
		used := make([]int, 0, len(corr.UsedInEntries))
		for _, entry := range corr.UsedInEntries {
			used = append(used, seqOf(entry))
		}
		correlations = append(correlations, Correlation{
			Name:         corr.Name,
			Source:       corr.Source,
			Expression:   corr.Expression,
			ExtractType:  corr.ExtractType,
			Convert:      corr.Convert,
			FoundInSeq:   seqOf(corr.FoundInEntry),
			UsedInSeqs:   used,
			Value:        corr.Value,
			SampleValue:  corr.SampleValue,
			RuleID:       corr.RuleID,
			Confidence:   corr.Confidence,
			Reasons:      corr.Reasons,
			PreApplied:   corr.PreApplied,
			ReviewAction: corr.ReviewAction,
		})
	}
	if skipped := countUnappliedRecorderCorrelations(correlations); skipped > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Detected %d correlation candidate(s) left for review because confidence or encoding semantics did not permit an automatic rewrite.", skipped))
	}
	result.Correlations = disclosureSafeCorrelations(correlations)
	result.Baseline = converted.Baseline
	result.Script = script
}

// conversionHAR writes a recording as the HAR the shared conversion reads:
// the correlation view of each header the policy keeps, with excluded
// requests left out. entrySeqs[i] is the recorder sequence of entry i+1.
func conversionHAR(session *Session, policy headerPolicy) ([]byte, []int, error) {
	pageID := "page_" + session.ID
	archive := harArchive{Log: harLog{
		Version: "1.2",
		Creator: harCreator{Name: "APEX Load Recorder", Version: "2.0"},
		Pages:   []harPage{{StartedDateTime: session.StartedAt, ID: pageID, Title: session.Name}},
	}}
	entrySeqs := make([]int, 0, len(session.Requests))
	for _, req := range session.Requests {
		if req.Excluded {
			continue
		}
		entry := buildHAREntry(req, pageID, keptRequestHeaders(req, policy), conversionResponseHeaders(req), recordedAt(req))
		if len(req.WebSocketMessages) > 0 {
			entry.WebSocketMessages = conversionWebSocketMessages(req)
			if entry.Response.Status == 0 {
				entry.Response.Status = 101
			}
		}
		archive.Log.Entries = append(archive.Log.Entries, entry)
		entrySeqs = append(entrySeqs, req.Seq)
	}
	data, err := json.Marshal(archive)
	if err != nil {
		return nil, nil, fmt.Errorf("write the recording for conversion: %w", err)
	}
	return data, entrySeqs, nil
}

// keptRequestHeaders is the correlation view of a request's headers (the
// redacted view when the correlation view is gone, as after a reload), with
// the headers the policy drops left out.
func keptRequestHeaders(req RecordedRequest, policy headerPolicy) map[string][]string {
	source := req.RawHeadersMulti
	if len(source) == 0 && len(req.RawHeaders) > 0 {
		source = singleValueHeaders(req.RawHeaders)
	}
	if len(source) == 0 {
		source = req.HeadersMulti
	}
	if len(source) == 0 {
		source = singleValueHeaders(req.Headers)
	}
	kept := make(map[string][]string, len(source))
	for name, values := range source {
		if includeRecordedHeader(name, policy) {
			kept[name] = append([]string(nil), values...)
		}
	}
	return kept
}

// conversionResponseHeaders is the correlation view of a response's
// headers, falling back to the redacted view.
func conversionResponseHeaders(req RecordedRequest) map[string][]string {
	switch {
	case len(req.RawResponseHeadersMulti) > 0:
		return req.RawResponseHeadersMulti
	case len(req.RawResponseHeaders) > 0:
		return singleValueHeaders(req.RawResponseHeaders)
	case len(req.ResponseHeadersMulti) > 0:
		return req.ResponseHeadersMulti
	default:
		return singleValueHeaders(req.ResponseHeaders)
	}
}

func singleValueHeaders(headers map[string]string) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	multi := make(map[string][]string, len(headers))
	for name, value := range headers {
		multi[name] = []string{value}
	}
	return multi
}

// conversionWebSocketMessages writes a connection's text and binary
// messages, continuation frames joined to their message, each with a time.
func conversionWebSocketMessages(req RecordedRequest) []harWebSocketMessage {
	started := recordedAt(req)
	var messages []harWebSocketMessage
	for index, msg := range coalesceWebSocketMessages(req.WebSocketMessages) {
		if msg.Opcode != webSocketOpcodeText && msg.Opcode != webSocketOpcodeBinary {
			continue
		}
		at := msg.Timestamp
		if at.IsZero() {
			at = started.Add(time.Duration(index+1) * time.Microsecond)
		}
		opcode, data := webSocketOpcodeText, msg.Payload
		if webSocketDSLMessageType(msg) == "binary" {
			opcode = webSocketOpcodeBinary
		}
		messages = append(messages, harWebSocketMessage{
			Time:      at,
			Type:      harWebSocketMessageType(msg.Direction),
			Opcode:    opcode,
			Data:      data,
			Encoding:  msg.PayloadEncoding,
			Size:      msg.PayloadSizeBytes,
			Truncated: msg.PayloadTruncated,
		})
	}
	return messages
}

// recordedResourceURLs lists the static resources a recording kept (not
// excluded), once each.
func recordedResourceURLs(requests []RecordedRequest) []string {
	urls := make([]string, 0)
	seen := map[string]bool{}
	for _, req := range requests {
		raw := strings.TrimSpace(req.URL)
		if !req.IsStatic || req.Excluded || raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		urls = append(urls, raw)
	}
	return urls
}

// jsonNormalizedActions round-trips actions through JSON, so values held in
// their any-typed maps take the form a JSON client sends back.
func jsonNormalizedActions(actions []dsl.Action) ([]dsl.Action, error) {
	data, err := json.Marshal(actions)
	if err != nil {
		return nil, err
	}
	var normalized []dsl.Action
	if err := json.Unmarshal(data, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}
