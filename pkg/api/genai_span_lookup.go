package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultGenAISpanLookupLimit = 25
	maxGenAISpanLookupLimit     = 100
	genAISpanLookupPadding      = 5 * time.Minute
)

// GenAISpanLookupRequest specifies optional filters and trace IDs to correlate
// archived LLM transactions with OpenTelemetry gen_ai.* spans in Elastic or
// Dynatrace Grail.
type GenAISpanLookupRequest struct {
	TraceIDs []string `json:"trace_ids,omitempty"`
	Action   string   `json:"action,omitempty"`
	Model    string   `json:"model,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// GenAISpanRecord represents a single OpenTelemetry gen_ai.* span retrieved
// from an Elasticsearch/OpenSearch trace index or Dynatrace Grail DQL query.
type GenAISpanRecord struct {
	TraceID        string   `json:"trace_id"`
	SpanID         string   `json:"span_id,omitempty"`
	SpanName       string   `json:"span_name,omitempty"`
	ServiceName    string   `json:"service_name,omitempty"`
	Timestamp      string   `json:"timestamp,omitempty"`
	DurationMs     float64  `json:"duration_ms,omitempty"`
	SourceProvider string   `json:"source_provider"`
	SourceName     string   `json:"source_name,omitempty"`
	GenAISystem    string   `json:"gen_ai_system,omitempty"`
	OperationName  string   `json:"operation_name,omitempty"`
	RequestModel   string   `json:"request_model,omitempty"`
	ResponseModel  string   `json:"response_model,omitempty"`
	InputTokens    int      `json:"input_tokens,omitempty"`
	OutputTokens   int      `json:"output_tokens,omitempty"`
	FinishReasons  []string `json:"finish_reasons,omitempty"`
}

// GenAISpanMatch joins an archived client-side LLM transaction with its
// corresponding server-side OpenTelemetry gen_ai.* spans by W3C trace ID.
type GenAISpanMatch struct {
	TraceID                string            `json:"trace_id"`
	ActionName             string            `json:"action_name"`
	Timestamp              string            `json:"timestamp"`
	Dialect                string            `json:"dialect,omitempty"`
	ClientModel            string            `json:"client_model,omitempty"`
	ClientTTFTMs           float64           `json:"client_ttft_ms,omitempty"`
	ClientTPOTMs           float64           `json:"client_tpot_ms,omitempty"`
	ClientE2EMs            float64           `json:"client_e2e_ms,omitempty"`
	ClientPromptTokens     int               `json:"client_prompt_tokens,omitempty"`
	ClientCompletionTokens int               `json:"client_completion_tokens,omitempty"`
	ClientFinishReason     string            `json:"client_finish_reason,omitempty"`
	StatusCode             int               `json:"status_code,omitempty"`
	ServerModel            string            `json:"server_model,omitempty"`
	ServerInputTokens      int               `json:"server_input_tokens,omitempty"`
	ServerOutputTokens     int               `json:"server_output_tokens,omitempty"`
	InputTokenDelta        int               `json:"input_token_delta,omitempty"`
	OutputTokenDelta       int               `json:"output_token_delta,omitempty"`
	Spans                  []GenAISpanRecord `json:"spans"`
}

// GenAISpanLookupResponse returns correlated client LLM transactions and
// server-side OpenTelemetry gen_ai.* spans from monitored APM/trace backends.
type GenAISpanLookupResponse struct {
	TestID              string            `json:"test_id"`
	TraceIDs            []string          `json:"trace_ids"`
	MatchedTransactions int               `json:"matched_transactions"`
	Matches             []GenAISpanMatch  `json:"matches"`
	Spans               []GenAISpanRecord `json:"spans"`
	QueriedSources      []string          `json:"queried_sources,omitempty"`
	Warnings            []string          `json:"warnings,omitempty"`
}

func (s *Server) handleGenAISpanLookup(w http.ResponseWriter, r *http.Request) {
	test, record, lease, ok := s.requireRunArchiveTest(w, r)
	if !ok {
		return
	}
	defer lease.Release()

	var req GenAISpanLookupRequest
	if r.Body != nil && r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if !decodeJSONBody(w, r, &req) {
			return
		}
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultGenAISpanLookupLimit
	}
	if limit > maxGenAISpanLookupLimit {
		limit = maxGenAISpanLookupLimit
	}

	requestedTraces := make(map[string]struct{}, len(req.TraceIDs))
	traceOrder := make([]string, 0, len(req.TraceIDs))
	for _, rawID := range req.TraceIDs {
		trimmed := strings.ToLower(strings.TrimSpace(rawID))
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 128 {
			writeError(w, http.StatusBadRequest, "trace_id exceeds maximum length")
			return
		}
		if _, exists := requestedTraces[trimmed]; !exists {
			requestedTraces[trimmed] = struct{}{}
			traceOrder = append(traceOrder, trimmed)
		}
	}
	if len(traceOrder) > maxGenAISpanLookupLimit {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("supply at most %d trace_ids", maxGenAISpanLookupLimit))
		return
	}

	actionFilter := strings.TrimSpace(req.Action)
	modelFilter := strings.TrimSpace(req.Model)

	matchesByTrace := make(map[string]*GenAISpanMatch)
	matchOrder := make([]string, 0, limit)
	var minTS, maxTS time.Time

	_ = forEachArchiveTransaction(record, func(tx *TransactionEvent) error {
		if tx == nil || tx.BusinessTransaction || tx.Population == "transaction" {
			return nil
		}
		if tx.ServerTelemetry == nil || tx.ServerTelemetry.LLM == nil {
			return nil
		}
		llm := tx.ServerTelemetry.LLM
		if actionFilter != "" && !strings.EqualFold(tx.ActionName, actionFilter) {
			return nil
		}
		if modelFilter != "" && !strings.EqualFold(llm.Model, modelFilter) {
			return nil
		}
		if !tx.Timestamp.IsZero() {
			if minTS.IsZero() || tx.Timestamp.Before(minTS) {
				minTS = tx.Timestamp
			}
			if maxTS.IsZero() || tx.Timestamp.After(maxTS) {
				maxTS = tx.Timestamp
			}
		}
		traceID := strings.ToLower(strings.TrimSpace(firstNonEmpty(tx.SentTraceID, tx.TraceID)))
		if traceID == "" {
			return nil
		}
		if len(requestedTraces) > 0 {
			if _, ok := requestedTraces[traceID]; !ok {
				return nil
			}
		}
		if _, exists := matchesByTrace[traceID]; !exists {
			if len(matchOrder) >= limit {
				return nil
			}
			matchOrder = append(matchOrder, traceID)
			matchesByTrace[traceID] = &GenAISpanMatch{
				TraceID:                traceID,
				ActionName:             tx.ActionName,
				Timestamp:              tx.Timestamp.UTC().Format(time.RFC3339Nano),
				Dialect:                llm.Dialect,
				ClientModel:            llm.Model,
				ClientTTFTMs:           llm.TTFTMs,
				ClientTPOTMs:           llm.TPOTMs,
				ClientE2EMs:            llm.E2EMs,
				ClientPromptTokens:     llm.PromptTokens,
				ClientCompletionTokens: llm.CompletionTokens,
				ClientFinishReason:     llm.FinishReason,
				StatusCode:             tx.StatusCode,
				Spans:                  []GenAISpanRecord{},
			}
			if _, inOrder := requestedTraces[traceID]; !inOrder {
				requestedTraces[traceID] = struct{}{}
				traceOrder = append(traceOrder, traceID)
			}
		}
		return nil
	})

	manifest, _ := loadRunArchiveManifestFromRecord(record)
	from, to := minTS, maxTS
	if manifest != nil {
		if from.IsZero() && manifest.Provenance.StartedAt != nil && !manifest.Provenance.StartedAt.IsZero() {
			from = *manifest.Provenance.StartedAt
		}
		if completed := archiveExecutionCompletedAt(manifest); !completed.IsZero() && (to.IsZero() || completed.After(to)) {
			to = completed
		}
	}
	if from.IsZero() && record != nil && !record.CreatedAt.IsZero() {
		from = record.CreatedAt
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.Add(-15 * time.Minute)
	}
	from = from.Add(-genAISpanLookupPadding)
	to = to.Add(genAISpanLookupPadding)

	resp := GenAISpanLookupResponse{
		TestID:              test.ID,
		TraceIDs:            traceOrder,
		MatchedTransactions: len(matchOrder),
		Matches:             make([]GenAISpanMatch, 0, len(matchOrder)),
		Spans:               []GenAISpanRecord{},
	}

	if len(traceOrder) == 0 {
		resp.Warnings = append(resp.Warnings, "No W3C trace IDs were found in archived LLM transactions or request trace_ids.")
		writeJSON(w, http.StatusOK, resp)
		return
	}

	project := normalizeProjectID(test.ProjectID)
	searchCtx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	// 1. Query monitored Elastic/OpenSearch and governed Dynatrace trace sources.
	queriedDynatrace := false
	if monitors, ok := s.store.(storage.RunMonitorStore); ok {
		if jobs, err := monitors.ListRunMonitorJobs(project, test.ID); err == nil {
			for i := range jobs {
				job := jobs[i]
				if job.Source.Pack == monitorprovider.PackElasticQuery && job.ElasticSnapshot != nil {
					sourceLabel := firstNonEmpty(strings.TrimSpace(job.Source.Name), job.Source.ID)
					resp.QueriedSources = append(resp.QueriedSources, "elastic:"+sourceLabel)
					spans, err := s.queryElasticGenAISpans(searchCtx, job, traceOrder, from, to, limit)
					if err != nil {
						resp.Warnings = append(resp.Warnings, fmt.Sprintf("Elastic GenAI span query (%s) failed: %s", sourceLabel, scrubTextBounded(err.Error(), 200)))
						continue
					}
					resp.Spans = append(resp.Spans, spans...)
				} else if job.Source.Pack == monitorprovider.PackDynatrace && job.DynatraceSnapshot != nil && strings.TrimSpace(job.DynatraceSnapshot.Config.PlatformURL) != "" {
					queriedDynatrace = true
					sourceLabel := firstNonEmpty(strings.TrimSpace(job.Source.Name), job.Source.ID)
					resp.QueriedSources = append(resp.QueriedSources, "dynatrace:"+sourceLabel)
					dtCfg := &DynatraceCaptureConfig{
						ProjectID:               project,
						ConnectionID:            job.DynatraceSnapshot.ConnectionID,
						ConnectionName:          sourceLabel,
						EnvironmentURL:          job.DynatraceSnapshot.Config.EnvironmentURL,
						PlatformURL:             job.DynatraceSnapshot.Config.PlatformURL,
						PlatformAuthType:        job.DynatraceSnapshot.Config.PlatformAuthType,
						PlatformTokenSecret:     job.DynatraceSnapshot.PlatformTokenSecret,
						OAuthClientIDSecret:     job.DynatraceSnapshot.OAuthClientIDSecret,
						OAuthClientSecretSecret: job.DynatraceSnapshot.OAuthClientSecretSecret,
						OAuthResource:           job.DynatraceSnapshot.Config.OAuthResource,
						OAuthScopes:             append([]string(nil), job.DynatraceSnapshot.Config.OAuthScopes...),
					}
					spans, err := s.queryDynatraceGenAISpans(searchCtx, dtCfg, traceOrder, from, to, limit)
					if err != nil {
						resp.Warnings = append(resp.Warnings, fmt.Sprintf("Dynatrace Grail GenAI span query (%s) failed: %s", sourceLabel, scrubTextBounded(err.Error(), 200)))
						continue
					}
					resp.Spans = append(resp.Spans, spans...)
				}
			}
		}
	}

	// 2. Query monitored Dynatrace Grail spans when configured directly on the run.
	dtCfg := test.DynatraceCapture
	if !queriedDynatrace && dtCfg != nil && dtCfg.PlatformURL == "" && dtCfg.ConnectionID != "" && s.runner != nil {
		if resolved, err := s.runner.resolveDynatraceCaptureForTest(project, dtCfg); err == nil && resolved != nil {
			dtCfg = resolved
		}
	}
	if !queriedDynatrace && dtCfg != nil && strings.TrimSpace(dtCfg.PlatformURL) != "" {
		sourceLabel := firstNonEmpty(strings.TrimSpace(dtCfg.ConnectionName), dtCfg.ConnectionID, "dynatrace")
		resp.QueriedSources = append(resp.QueriedSources, "dynatrace:"+sourceLabel)
		spans, err := s.queryDynatraceGenAISpans(searchCtx, dtCfg, traceOrder, from, to, limit)
		if err != nil {
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("Dynatrace Grail GenAI span query (%s) failed: %s", sourceLabel, scrubTextBounded(err.Error(), 200)))
		} else {
			resp.Spans = append(resp.Spans, spans...)
		}
	}

	if len(resp.QueriedSources) == 0 {
		resp.Warnings = append(resp.Warnings, "No Elastic query or Dynatrace Grail monitor sources were attached to this run.")
	}

	for _, span := range resp.Spans {
		tid := strings.ToLower(strings.TrimSpace(span.TraceID))
		if tid == "" {
			continue
		}
		m, exists := matchesByTrace[tid]
		if !exists {
			m = &GenAISpanMatch{
				TraceID: tid,
				Spans:   []GenAISpanRecord{},
			}
			matchesByTrace[tid] = m
			matchOrder = append(matchOrder, tid)
		}
		m.Spans = append(m.Spans, span)
		if m.ServerModel == "" {
			m.ServerModel = firstNonEmpty(span.ResponseModel, span.RequestModel)
		}
		m.ServerInputTokens += span.InputTokens
		m.ServerOutputTokens += span.OutputTokens
	}

	for _, tid := range matchOrder {
		m := matchesByTrace[tid]
		if m == nil {
			continue
		}
		if m.ServerInputTokens > 0 && m.ClientPromptTokens > 0 {
			m.InputTokenDelta = m.ServerInputTokens - m.ClientPromptTokens
		}
		if m.ServerOutputTokens > 0 && m.ClientCompletionTokens > 0 {
			m.OutputTokenDelta = m.ServerOutputTokens - m.ClientCompletionTokens
		}
		resp.Matches = append(resp.Matches, *m)
	}
	resp.MatchedTransactions = len(resp.Matches)

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) queryElasticGenAISpans(ctx context.Context, job storage.RunMonitorJob, traceIDs []string, from, to time.Time, limit int) ([]GenAISpanRecord, error) {
	conn := job.ElasticSnapshot
	if conn == nil {
		return nil, nil
	}
	tsField := firstNonEmpty(strings.TrimSpace(conn.TimestampField), "@timestamp")
	traceField := firstNonEmpty(strings.TrimSpace(conn.TraceIDField), "trace.id")

	filters := []any{
		map[string]any{
			"range": map[string]any{
				tsField: map[string]any{
					"gte": from.UTC().Format(time.RFC3339Nano),
					"lte": to.UTC().Format(time.RFC3339Nano),
				},
			},
		},
		map[string]any{
			"terms": map[string]any{
				traceField: traceIDs,
			},
		},
	}
	if strings.TrimSpace(conn.BaseFilter) != "" {
		filters = append(filters, map[string]any{"query_string": map[string]any{"query": conn.BaseFilter}})
	}

	body := map[string]any{
		"size": max(limit*2, 50),
		"sort": []any{map[string]any{tsField: map[string]any{"order": "asc"}}},
		"query": map[string]any{
			"bool": map[string]any{
				"filter": filters,
			},
		},
	}

	data, err := s.searchElastic(ctx, job, body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Hits struct {
			Hits []struct {
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("invalid elastic response: %w", err)
	}

	sourceLabel := firstNonEmpty(strings.TrimSpace(job.Source.Name), job.Source.ID)
	out := make([]GenAISpanRecord, 0, len(parsed.Hits.Hits))
	for _, hit := range parsed.Hits.Hits {
		rec := extractGenAISpanFromMap(hit.Source, "elastic", sourceLabel, tsField, traceField)
		if rec.TraceID == "" && len(traceIDs) == 1 {
			rec.TraceID = traceIDs[0]
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *Server) queryDynatraceGenAISpans(ctx context.Context, cfg *DynatraceCaptureConfig, traceIDs []string, from, to time.Time, limit int) ([]GenAISpanRecord, error) {
	quoted := make([]string, 0, min(len(traceIDs), 64))
	for _, id := range traceIDs {
		if len(quoted) >= 64 {
			break
		}
		quoted = append(quoted, `"`+escapeDQLString(id)+`"`)
	}
	traceField := firstNonEmpty(strings.TrimSpace(cfg.LogTraceIDField), "trace.id")
	dql := fmt.Sprintf(
		`fetch spans, from: toTimestamp("%s"), to: toTimestamp("%s") | filter in(toString(%s), {%s}) | limit %d`,
		from.UTC().Format(time.RFC3339Nano),
		to.UTC().Format(time.RFC3339Nano),
		traceField,
		strings.Join(quoted, ","),
		max(limit*2, 50),
	)
	client := s.elasticHTTPClient
	records, err := executeDynatraceDQL(ctx, s.runner, cfg, dql, client)
	if err != nil {
		return nil, err
	}
	sourceLabel := firstNonEmpty(strings.TrimSpace(cfg.ConnectionName), cfg.ConnectionID, "dynatrace")
	out := make([]GenAISpanRecord, 0, len(records))
	for _, raw := range records {
		rec := extractGenAISpanFromMap(raw, "dynatrace", sourceLabel, "start_time", traceField)
		if rec.TraceID != "" && len(traceIDs) > 0 && !slices.Contains(traceIDs, strings.ToLower(rec.TraceID)) {
			continue
		}
		if rec.TraceID == "" && len(traceIDs) == 1 {
			rec.TraceID = traceIDs[0]
		}
		out = append(out, rec)
	}
	return out, nil
}

func extractGenAISpanFromMap(m map[string]any, provider, sourceName, tsField, traceField string) GenAISpanRecord {
	lookupStr := func(keys ...string) string {
		for _, k := range keys {
			if k == "" {
				continue
			}
			for _, candidate := range []string{k, "attributes." + k, "span.attributes." + k, "labels." + k} {
				if v := elasticSourceField(m, candidate); v != nil {
					if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
						return s
					}
				}
			}
		}
		return ""
	}
	lookupInt := func(keys ...string) int {
		for _, k := range keys {
			for _, candidate := range []string{k, "attributes." + k, "span.attributes." + k, "labels." + k} {
				v := elasticSourceField(m, candidate)
				switch n := v.(type) {
				case float64:
					return int(n)
				case int:
					return n
				case int64:
					return int(n)
				case json.Number:
					if iv, err := n.Int64(); err == nil {
						return int(iv)
					}
				case string:
					if iv, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
						return iv
					}
				}
			}
		}
		return 0
	}
	lookupFloat := func(keys ...string) float64 {
		for _, k := range keys {
			v := elasticSourceField(m, k)
			switch n := v.(type) {
			case float64:
				return n
			case int:
				return float64(n)
			case int64:
				return float64(n)
			case string:
				if fv, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
					return fv
				}
			}
		}
		return 0
	}
	lookupReasons := func(keys ...string) []string {
		for _, k := range keys {
			for _, candidate := range []string{k, "attributes." + k, "span.attributes." + k, "labels." + k} {
				v := elasticSourceField(m, candidate)
				switch arr := v.(type) {
				case []any:
					out := make([]string, 0, len(arr))
					for _, item := range arr {
						if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
							out = append(out, s)
						}
					}
					if len(out) > 0 {
						return out
					}
				case []string:
					if len(arr) > 0 {
						return append([]string(nil), arr...)
					}
				case string:
					if s := strings.TrimSpace(arr); s != "" {
						return []string{s}
					}
				}
			}
		}
		return nil
	}

	durMs := lookupFloat("duration_ms", "span.duration.ms")
	if durMs == 0 {
		if durUs := lookupFloat("transaction.duration.us", "span.duration.us", "duration.us"); durUs > 0 {
			durMs = durUs / 1000.0
		} else if durNs := lookupFloat("duration", "span.duration.ns", "duration_ns"); durNs > 1e5 {
			durMs = durNs / 1e6
		}
	}

	return GenAISpanRecord{
		TraceID:        strings.ToLower(lookupStr(traceField, "trace.id", "trace_id", "dt.trace_id")),
		SpanID:         lookupStr("span.id", "span_id", "dt.span_id", "id"),
		SpanName:       lookupStr("span.name", "span_name", "name", "transaction.name"),
		ServiceName:    lookupStr("service.name", "service_name", "dt.entity.service"),
		Timestamp:      lookupStr(tsField, "@timestamp", "timestamp", "start_time"),
		DurationMs:     durMs,
		SourceProvider: provider,
		SourceName:     sourceName,
		GenAISystem:    lookupStr("gen_ai.system", "gen_ai.provider.name"),
		OperationName:  lookupStr("gen_ai.operation.name"),
		RequestModel:   lookupStr("gen_ai.request.model"),
		ResponseModel:  lookupStr("gen_ai.response.model"),
		InputTokens:    lookupInt("gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens"),
		OutputTokens:   lookupInt("gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens"),
		FinishReasons:  lookupReasons("gen_ai.response.finish_reasons", "gen_ai.response.finish_reason"),
	}
}
