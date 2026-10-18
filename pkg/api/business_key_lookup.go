package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	maxBusinessKeyLookupValues = 20
	maxBusinessKeyValueBytes   = 256
	maxBusinessKeyMatches      = 50
	maxBusinessKeyLogEntries   = 50
	maxBusinessKeyLogMessage   = 512
	// businessKeyLogPadding widens the run window for the log search, for
	// clock skew and asynchronous processing.
	businessKeyLogPadding = 5 * time.Minute
	// businessKeyLogConcurrency bounds the per-value searches in flight.
	businessKeyLogConcurrency = 4
	businessKeyLogTotalLimit  = 10000
)

// Log lookup outcomes.
const (
	businessKeyLogsFound       = "found"
	businessKeyLogsNotFound    = "not_found"
	businessKeyLogsError       = "error"
	businessKeyLogsUnavailable = "unavailable"
)

type businessKeyLookupRequest struct {
	MappingID string   `json:"mapping_id"`
	Values    []string `json:"values"`
}

// BusinessKeyLookupResponse joins each supplied value to the run's archived
// requests and the mapped source's logs. Results are keyed by the value's
// position in the request: values are never echoed, stored or logged.
type BusinessKeyLookupResponse struct {
	Mapping     BusinessKeyLookupMapping  `json:"mapping"`
	Results     []BusinessKeyLookupResult `json:"results"`
	Limitations []string                  `json:"limitations"`
}

type BusinessKeyLookupMapping struct {
	ID         string `json:"id"`
	Revision   int64  `json:"revision"`
	Label      string `json:"label,omitempty"`
	Extraction string `json:"extraction"`
	SourceID   string `json:"source_id"`
	Field      string `json:"field"`
}

type BusinessKeyLookupResult struct {
	Index    int                 `json:"index"`
	Requests BusinessKeyRequests `json:"requests"`
	Logs     BusinessKeyLogs     `json:"logs"`
}

type BusinessKeyRequests struct {
	Total   int                  `json:"total"`
	Matches []BusinessKeyRequest `json:"matches"`
}

type BusinessKeyRequest struct {
	Timestamp  time.Time `json:"timestamp"`
	Population string    `json:"population,omitempty"`
	Group      string    `json:"group,omitempty"`
	Name       string    `json:"name"`
	DurationMs float64   `json:"duration_ms"`
	Success    bool      `json:"success"`
	StatusCode int       `json:"status_code,omitempty"`
	TraceID    string    `json:"trace_id,omitempty"`
	// MatchedBy is "digest" or "value".
	MatchedBy string `json:"matched_by"`
}

type BusinessKeyLogs struct {
	Status  string                `json:"status"`
	Detail  string                `json:"detail,omitempty"`
	Total   int                   `json:"total"`
	Entries []BusinessKeyLogEntry `json:"entries"`
}

type BusinessKeyLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	TraceID   string    `json:"trace_id,omitempty"`
	Message   string    `json:"message,omitempty"`
}

// handleBusinessKeyLookup joins operator-supplied business-key values to a
// run. The values live only in this request.
func (s *Server) handleBusinessKeyLookup(w http.ResponseWriter, r *http.Request) {
	test, record, lease, ok := s.requireRunArchiveTest(w, r)
	if !ok {
		return
	}
	defer lease.Release()
	project := normalizeProjectID(test.ProjectID)
	if !businessKeyProjectEnabled(s.store, project) {
		writeError(w, http.StatusConflict, "business keys are not enabled for this project")
		return
	}
	var request businessKeyLookupRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if !decodeJSONBody(w, r, &request) {
		return
	}
	if len(request.Values) == 0 || len(request.Values) > maxBusinessKeyLookupValues {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("supply 1 to %d values", maxBusinessKeyLookupValues))
		return
	}
	for _, value := range request.Values {
		if strings.TrimSpace(value) == "" || len(value) > maxBusinessKeyValueBytes {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("each value must be non-empty and at most %d bytes", maxBusinessKeyValueBytes))
			return
		}
	}
	mapping, ok := s.businessKeyMappingForLookup(w, project, strings.TrimSpace(request.MappingID))
	if !ok {
		return
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive manifest")
		return
	}
	out := BusinessKeyLookupResponse{
		Mapping: BusinessKeyLookupMapping{ID: mapping.ID, Revision: mapping.Revision, Label: mapping.Label, Extraction: mapping.Extraction, SourceID: mapping.SourceID, Field: mapping.Field},
		Results: make([]BusinessKeyLookupResult, len(request.Values)), Limitations: []string{},
	}
	for i := range out.Results {
		out.Results[i] = BusinessKeyLookupResult{Index: i, Requests: BusinessKeyRequests{Matches: []BusinessKeyRequest{}}, Logs: BusinessKeyLogs{Entries: []BusinessKeyLogEntry{}}}
	}
	policy := manifest.Provenance.BusinessKeys
	if manifest.InitialProvenance != nil {
		policy = manifest.InitialProvenance.BusinessKeys
	}
	digester, digestErr := s.runner.businessKeyDigester(r.Context(), project)
	switch {
	case policy != nil && !slices.Contains(policy.Extractions, mapping.Extraction) && len(policy.Extractions) >= maxBusinessKeyMappings:
		out.Limitations = append(out.Limitations, fmt.Sprintf("a run's archive digests at most %d business keys and this one was not among them, so its values are matched as archived", maxBusinessKeyMappings))
	case policy == nil || !slices.Contains(policy.Extractions, mapping.Extraction):
		out.Limitations = append(out.Limitations, "this run was archived before the mapping was approved, so its values are matched as archived")
	case len(policy.DigestOnlyGroups) > 0 && digestErr != nil:
		out.Limitations = append(out.Limitations, "values of groups "+strings.Join(policy.DigestOnlyGroups, ", ")+" were archived as digests, and this controller cannot compute digests without its key wrapper")
	}
	if manifest.BusinessKeyDigestsUnavailable {
		out.Limitations = append(out.Limitations, "some values were redacted without a digest when the run was archived, so they cannot be matched")
	}
	if err := matchBusinessKeyRequests(r.Context(), record, manifest, mapping.Extraction, request.Values, digester, out.Results); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archived requests")
		return
	}
	s.lookupBusinessKeyLogs(r.Context(), project, test.ID, manifest, mapping, request.Values, out.Results)
	s.recordAuditEvent(r, "business_key.lookup", "success", "test", test.ID, project, map[string]any{"mapping_id": mapping.ID, "mapping_revision": mapping.Revision, "values": len(request.Values)})
	writeJSON(w, http.StatusOK, out)
}

// businessKeyMappingForLookup loads an approved mapping, or writes the error.
func (s *Server) businessKeyMappingForLookup(w http.ResponseWriter, project, id string) (businessKeyMappingDefinition, bool) {
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "analysis definitions unavailable")
		return businessKeyMappingDefinition{}, false
	}
	definition, err := store.GetAnalysisDefinition(project, id, 0)
	if err != nil || definition.Kind != storage.AnalysisDefinitionKindBusinessKeyMapping || definition.Deleted {
		writeError(w, http.StatusNotFound, "business key mapping not found")
		return businessKeyMappingDefinition{}, false
	}
	mapping, err := validateBusinessKeyMappingDefinition(definition.Definition)
	if err != nil {
		writeError(w, http.StatusConflict, "business key mapping is invalid: "+err.Error())
		return businessKeyMappingDefinition{}, false
	}
	return businessKeyMappingDefinition{ID: definition.ID, Revision: definition.Revision, businessKeyMapping: mapping}, true
}

// matchBusinessKeyRequests scans the archived requests once. A row matches
// a value by its stored digest, or, where the value was kept, by the value
// itself.
func matchBusinessKeyRequests(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, extraction string, values []string, digester *businessKeyDigester, results []BusinessKeyLookupResult) error {
	byValue := map[string][]int{}
	byDigest := map[string][]int{}
	for i, value := range values {
		byValue[value] = append(byValue[value], i)
		if digester != nil {
			d := digester.digest(extraction, value)
			byDigest[d] = append(byDigest[d], i)
		}
	}
	if manifest.TransactionsFile == "" {
		return nil
	}
	needle := []byte(`"` + extraction + `"`)
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.TransactionsFile)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for rows := 0; scanner.Scan(); rows++ {
		if rows%4096 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Bytes()
		// Most rows carry no extraction; skip them without decoding.
		if !bytes.Contains(line, needle) {
			continue
		}
		var row TransactionEvent
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		matched, by := []int(nil), ""
		if d := row.ExtractionDigests[extraction]; d != "" && byDigest[d] != nil {
			matched, by = byDigest[d], "digest"
		} else if v := row.Extractions[extraction]; v != "" && v != scrub.Redacted && byValue[v] != nil {
			matched, by = byValue[v], "value"
		}
		for _, i := range matched {
			results[i].Requests.Total++
			if len(results[i].Requests.Matches) < maxBusinessKeyMatches {
				results[i].Requests.Matches = append(results[i].Requests.Matches, BusinessKeyRequest{
					Timestamp: row.Timestamp, Population: row.Population, Group: row.Group, Name: row.ActionName, DurationMs: row.DurationMs,
					Success: row.Success, StatusCode: row.StatusCode, TraceID: firstNonEmpty(row.SentTraceID, row.TraceID), MatchedBy: by,
				})
			}
		}
	}
	return scanner.Err()
}

// lookupBusinessKeyLogs searches the mapped source's logs over the run for
// the values, in one query. Only elastic-query sources can be searched.
func (s *Server) lookupBusinessKeyLogs(ctx context.Context, project, testID string, manifest *RunArchiveManifest, mapping businessKeyMappingDefinition, values []string, results []BusinessKeyLookupResult) {
	set := func(status, detail string) {
		for i := range results {
			results[i].Logs.Status, results[i].Logs.Detail = status, detail
		}
	}
	monitors, ok := s.store.(storage.RunMonitorStore)
	if !ok {
		set(businessKeyLogsUnavailable, "monitor sources are unavailable")
		return
	}
	jobs, err := monitors.ListRunMonitorJobs(project, testID)
	if err != nil {
		set(businessKeyLogsUnavailable, "the run's monitor sources could not be read")
		return
	}
	var job *storage.RunMonitorJob
	for i := range jobs {
		if jobs[i].Source.ID == mapping.SourceID {
			job = &jobs[i]
			break
		}
	}
	switch {
	case job == nil:
		set(businessKeyLogsUnavailable, "the mapped source did not monitor this run")
		return
	case job.Source.ElasticQuery == nil || job.ElasticSnapshot == nil || job.Source.Pack != monitorprovider.PackElasticQuery:
		set(businessKeyLogsUnavailable, "only Elasticsearch and OpenSearch query sources can be searched for business keys")
		return
	}
	from, to := job.StartedAt, archiveExecutionCompletedAt(manifest)
	if started := manifest.Provenance.StartedAt; started != nil && !started.IsZero() {
		from = *started
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	from, to = from.Add(-businessKeyLogPadding), to.Add(businessKeyLogPadding)
	searchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// One bounded query per value, so each value's total and entries are
	// its own, a few at a time.
	var wg sync.WaitGroup
	slots := make(chan struct{}, businessKeyLogConcurrency)
	for i := range values {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i].Logs = s.searchBusinessKeyLogs(searchCtx, job, mapping.Field, values[i], from, to)
		}(i)
	}
	wg.Wait()
	for i := range results {
		if results[i].Logs.Status == businessKeyLogsError {
			// The error never carries the value: it is only in the body.
			slog.Warn("business key log lookup failed", "test", testID, "source", mapping.SourceID, "detail", results[i].Logs.Detail)
			break
		}
	}
}

// searchBusinessKeyLogs searches one value's log entries over a window.
func (s *Server) searchBusinessKeyLogs(ctx context.Context, job *storage.RunMonitorJob, field, value string, from, to time.Time) BusinessKeyLogs {
	conn := job.ElasticSnapshot
	logs := BusinessKeyLogs{Entries: []BusinessKeyLogEntry{}}
	filters := []any{
		map[string]any{"range": map[string]any{conn.TimestampField: map[string]any{"gte": from.UTC().Format(time.RFC3339Nano), "lte": to.UTC().Format(time.RFC3339Nano)}}},
		map[string]any{"term": map[string]any{field: value}},
	}
	if strings.TrimSpace(conn.BaseFilter) != "" {
		filters = append(filters, map[string]any{"query_string": map[string]any{"query": conn.BaseFilter}})
	}
	source := []string{conn.TimestampField, "message"}
	if conn.TraceIDField != "" {
		source = append(source, conn.TraceIDField)
	}
	body := map[string]any{
		"size": maxBusinessKeyLogEntries, "track_total_hits": businessKeyLogTotalLimit, "_source": source,
		"sort": []any{map[string]any{conn.TimestampField: map[string]any{"order": "asc"}}}, "query": map[string]any{"bool": map[string]any{"filter": filters}},
	}
	data, err := s.searchElastic(ctx, *job, body)
	if err != nil {
		logs.Status, logs.Detail = businessKeyLogsError, scrubTextBounded(err.Error(), 256)
		return logs
	}
	var response struct {
		TimedOut bool `json:"timed_out"`
		Hits     struct {
			Total struct {
				Value    int    `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		logs.Status, logs.Detail = businessKeyLogsError, "the search response is unreadable"
		return logs
	}
	for _, hit := range response.Hits.Hits {
		at, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(elasticSourceField(hit.Source, conn.TimestampField)))
		entry := BusinessKeyLogEntry{Timestamp: at}
		if conn.TraceIDField != "" {
			if id, ok := elasticSourceField(hit.Source, conn.TraceIDField).(string); ok {
				entry.TraceID = id
			}
		}
		if message, ok := hit.Source["message"].(string); ok {
			entry.Message = scrubTextBounded(message, maxBusinessKeyLogMessage)
		}
		logs.Entries = append(logs.Entries, entry)
	}
	logs.Total = max(response.Hits.Total.Value, len(logs.Entries))
	logs.Status = businessKeyLogsNotFound
	if logs.Total > 0 {
		logs.Status = businessKeyLogsFound
	}
	switch {
	case response.TimedOut:
		logs.Detail = "the search timed out; results may be partial"
	case response.Hits.Total.Relation == "gte":
		logs.Detail = fmt.Sprintf("more than %d entries", businessKeyLogTotalLimit)
	}
	return logs
}

// elasticSourceField reads a dotted field from a hit's _source, whether it
// is stored flat ("order.id") or nested.
func elasticSourceField(source map[string]any, field string) any {
	if value, ok := source[field]; ok {
		return value
	}
	var current any = source
	for _, part := range strings.Split(field, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}
