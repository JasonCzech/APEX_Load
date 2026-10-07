package elastic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func recordFields(s Selection) []string {
	fields := []string{s.TimestampField, s.IngestedField, s.ServiceField, s.RunIDField, s.TraceIDField, s.SpanIDField, s.ParentSpanIDField, s.TransactionIDField, s.MessageField, s.SeverityField, s.DurationField, s.StatusField, "processor.event", "span.name", "transaction.name", "name", "operationName", "span.duration.us", "transaction.duration.us", "event.duration", "error.message", "error.log.message", "error.exception.message", "error.log.level"}
	fields = append(fields, s.AttributeFields...)
	fields = append(fields, s.ProjectedFields...)
	seen := map[string]bool{}
	out := []string{}
	for _, f := range fields {
		if f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}
func (c *Client) collectRecords(ctx context.Context, s Selection, t task, from, to time.Time, runID string, r *Result) (next *Continuation, err error) {
	if c.cfg.Product == ProductOpenSearch && !r.Capabilities.ShardDoc && s.UniqueSortField == "" {
		return nil, errors.New("OpenSearch below 3.3 requires a unique sortable document key")
	}
	cursor := cursorFor(s, t, from, to)
	retained := false
	defer func() {
		if !retained {
			c.retainFailedCleanup(ctx, cursor, r)
		}
	}()
	if cursor.PITID == "" {
		if len(cursor.ResolvedIndices) == 0 {
			cursor.ResolvedIndices = append([]string{}, r.ResolvedIndices...)
			if len(cursor.ResolvedIndices) == 0 {
				cursor.ResolvedIndices, err = c.ResolveConcreteIndices(ctx, s.Indices)
				if err != nil {
					if errors.Is(err, ErrBudget) {
						retained = true
						return cursor, nil
					}
					return nil, err
				}
			}
		}
		cursor.PITID, err = c.openPIT(ctx, s.Indices)
		if cursor.PITID != "" {
			cursor.KeepAliveUntil = time.Now().UTC().Add(2 * time.Minute)
		}
		if err != nil {
			if errors.Is(err, ErrBudget) {
				retained = true
				return cursor, nil
			}
			return nil, err
		}
	}
	r.ResolvedIndices = append([]string{}, cursor.ResolvedIndices...)
	s.ResolvedIndices = cursor.ResolvedIndices
	restarted := false
	seen := map[string]int{}
	for i, e := range r.Evidence {
		seen[e.ID] = i
	}
	sort := []any{map[string]any{s.TimestampField: map[string]any{"order": "asc", "unmapped_type": "date"}}}
	if r.Capabilities.ShardDoc {
		if c.cfg.Product == ProductOpenSearch {
			// OpenSearch 3.3 encodes the shard ID within an index, rather
			// than Elasticsearch's PIT-wide shard ordinal. Keep the
			// concrete index in the tie vector for multi-index queries.
			sort = append(sort, map[string]any{"_index": "asc"})
		}
		sort = append(sort, map[string]any{"_shard_doc": "asc"})
	} else {
		sort = append(sort, map[string]any{"_index": "asc"}, map[string]any{s.UniqueSortField: "asc"})
	}
	for {
		remaining := c.cfg.MaxEvidence - len(r.Evidence)
		if remaining <= 0 {
			retained = true
			return cursor, nil
		}
		size := min(s.PageSize, remaining)
		body := map[string]any{"size": size, "track_total_hits": false, "pit": map[string]any{"id": cursor.PITID, "keep_alive": pitKeepAlive}, "sort": sort, "_source": recordFields(s), "query": searchFilter(s, t, from, to, runID), "timeout": fmt.Sprintf("%ds", c.cfg.TimeoutSeconds)}
		if len(cursor.SearchAfter) > 0 {
			body["search_after"] = scalarList(cursor.SearchAfter)
		}
		var response searchResponse
		if e := c.searchPIT(ctx, cursor, body, &response); e != nil {
			var upstream *HTTPError
			if !restarted && errors.As(e, &upstream) && upstream.Status == 404 {
				restarted = true
				err = c.restartPIT(ctx, s.Indices, cursor, r)
				if errors.Is(err, ErrBudget) {
					retained = true
					return cursor, nil
				}
				if err != nil {
					return nil, err
				}
				continue
			}
			if errors.Is(e, ErrBudget) {
				retained = true
				return cursor, nil
			}
			return nil, e
		}
		if e := checkSearch(response); e != nil {
			return nil, e
		}
		if len(response.Hits.Hits) > size {
			return nil, errors.New("search returned more hits than requested")
		}
		page := []Evidence{}
		pageIdentities := map[string]bool{}
		after := cursor.SearchAfter
		for _, hit := range response.Hits.Hits {
			if len(hit.Sort) != len(sort) {
				return nil, errors.New("search hit lacks complete sort cursor")
			}
			values := []analysis.ScalarValue{}
			for _, v := range hit.Sort {
				sv, e := scalar(v)
				if e != nil {
					return nil, e
				}
				values = append(values, sv)
			}
			if cursorEqual(after, values) {
				return nil, errors.New("search cursor did not advance")
			}
			after = values
			evidence, e := c.projectRecord(s, t, hit.Source, hit.Index, hit.ID, from, to, runID)
			if e != nil {
				return nil, e
			}
			if index, ok := seen[evidence.ID]; ok {
				r.Evidence[index] = evidence
			} else {
				if pageIdentities[evidence.ID] {
					return nil, errors.New("duplicate document identity in a source page")
				}
				pageIdentities[evidence.ID] = true
				page = append(page, evidence)
			}
		}
		for _, e := range page {
			seen[e.ID] = len(r.Evidence)
			r.Evidence = append(r.Evidence, e)
		}
		cursor.SearchAfter = after
		if len(response.Hits.Hits) < size {
			return nil, nil
		}
	}
}
func (c *Client) projectRecord(s Selection, t task, row map[string]any, index, id string, from, to time.Time, runID string) (Evidence, error) {
	at, ok := timestamp(lookup(row, s.TimestampField))
	if !ok || at.Before(from) || !at.Before(to) {
		return Evidence{}, errors.New("record timestamp outside enforced window")
	}
	if s.Preset == "elastic_apm" && (t.kind == "span" || t.kind == "transaction" || t.kind == "error") && lookup(row, "processor.event") != t.kind {
		return Evidence{}, errors.New("APM record kind does not match selected signal")
	}
	if index == "" || len(index) > 255 || strings.HasPrefix(index, ".") && !strings.HasPrefix(index, ".ds-") || id == "" || len(id) > 512 {
		return Evidence{}, errors.New("invalid document identity")
	}
	if len(s.ResolvedIndices) > 0 {
		matched := false
		for _, approved := range s.ResolvedIndices {
			matched = matched || approved == index
		}
		if !matched {
			return Evidence{}, errors.New("document index outside resolved fixed-window scope")
		}
	}
	if s.RunIDField != "" {
		if v, ok := lookup(row, s.RunIDField).(string); !ok || v != runID {
			return Evidence{}, errors.New("record run identity does not match scope")
		}
	}
	attrs := map[string]analysis.ScalarValue{}
	for _, f := range append(append([]string{}, s.AttributeFields...), s.ProjectedFields...) {
		sv, e := scalar(lookup(row, f))
		if e != nil {
			return Evidence{}, e
		}
		if e = sv.Validate(); e != nil {
			return Evidence{}, e
		}
		if credential.SensitiveName(f) || sensitiveProjectionName(f) {
			sv = analysis.ScalarValue{Kind: "string", StringValue: scrub.Redacted}
		} else if sv.Kind == "string" {
			sv.StringValue = c.redact(sv.StringValue)
		}
		attrs[f] = sv
	}
	service := stringValue(lookup(row, s.ServiceField), 256)
	if len(s.ServiceValues) > 0 {
		matched := false
		for _, v := range s.ServiceValues {
			matched = matched || service == v
		}
		if !matched {
			return Evidence{}, errors.New("record service does not match scope")
		}
	}
	h := sha256.Sum256([]byte(c.cfg.Product + "\x00" + t.id + "\x00" + index + "\x00" + id))
	e := Evidence{ID: hex.EncodeToString(h[:]), Provider: c.cfg.Product, QueryID: t.id, Kind: t.kind, Index: index, DocumentID: id, Service: service, TimestampUTC: at, Severity: stringValue(lookup(row, s.SeverityField), 128), Status: stringValue(lookup(row, s.StatusField), 128), Correlation: "service_time"}
	if c.redactIdentity(id) != id {
		e.DocumentID = "redacted:" + e.ID[:32]
	}
	if service == "" {
		e.Correlation = "unmatched"
	}
	traceID := c.redactIdentity(stringValue(lookup(row, s.TraceIDField), 128))
	if traceID != "" {
		e.EntityIDs = map[string]string{"trace_id": traceID}
		for _, observed := range s.ObservedTraceIDs {
			if strings.EqualFold(observed, traceID) {
				e.Correlation = "trace_id"
				break
			}
		}
	}
	if s.RunIDField != "" {
		e.Correlation = "run_id"
	}
	e.ReceivedAt = time.Now().UTC()
	if value := lookup(row, s.IngestedField); value != nil {
		if ingested, ok := timestamp(value); ok {
			e.IngestedAt = &ingested
		} else {
			return Evidence{}, errors.New("invalid mapped ingestion timestamp")
		}
	}
	e.Service = c.redact(e.Service)
	e.Status = c.redact(e.Status)
	e.Severity = c.redact(e.Severity)
	message := c.redact(stringValue(lookup(row, s.MessageField), 4096))
	if t.kind == "error" && message == "" {
		message = c.redact(errorMessage(row))
		if e.Severity == "" {
			e.Severity = c.redact(stringValue(lookup(row, "error.log.level"), 128))
		}
	}
	if t.kind == "log" || t.kind == "error" {
		e.Summary = message
		e.Log = &analysis.ProviderLog{Message: message, Level: e.Severity, Attributes: attrs}
	} else {
		durationField, unit := s.DurationField, s.DurationUnit
		if t.kind == "span" && s.Preset == "elastic_apm" && durationField == "transaction.duration.us" {
			durationField, unit = "span.duration.us", "us"
		}
		durationValue, durationErr := mappedNumber(lookup(row, durationField))
		if durationErr != nil {
			return Evidence{}, durationErr
		}
		duration, ok := float64(0), durationValue != nil && durationErr == nil
		if ok {
			duration = *durationValue
		}
		if !ok || duration < 0 {
			return Evidence{}, errors.New("record lacks a nonnegative mapped duration")
		}
		duration = scaleValue(duration, unit)
		if !finite(duration) {
			return Evidence{}, errors.New("record duration exceeds bounds")
		}
		operation := stringValue(lookup(row, "name"), 256)
		for _, f := range []string{"span.name", "transaction.name", "operationName"} {
			if v := stringValue(lookup(row, f), 256); v != "" {
				operation = v
				break
			}
		}
		operation = c.redact(operation)
		if t.kind == "transaction" {
			if operation == "" {
				return Evidence{}, errors.New("transaction lacks a mapped name")
			}
			e.Transaction = &analysis.ProviderTransaction{Name: operation, DurationMS: duration, Attributes: attrs}
			e.Summary = c.redact(operation)
		} else {
			trace, span := c.redactIdentity(stringValue(lookup(row, s.TraceIDField), 128)), c.redactIdentity(stringValue(lookup(row, s.SpanIDField), 128))
			if trace == "" || span == "" {
				return Evidence{}, errors.New("span lacks trace and span identity")
			}
			e.Span = &analysis.ProviderSpan{TraceID: trace, SpanID: span, ParentSpanID: c.redactIdentity(stringValue(lookup(row, s.ParentSpanIDField), 128)), Operation: operation, DurationMS: duration, Attributes: attrs}
			e.Summary = c.redact(operation)
		}
	}
	if err := e.Validate(); err != nil {
		return Evidence{}, err
	}
	encoded, err := json.Marshal(e)
	if err != nil || len(encoded) > 8<<10 {
		return Evidence{}, errors.New("projected record exceeds byte limit")
	}
	return e, nil
}

func errorMessage(row map[string]any) string {
	for _, name := range []string{"error.message", "error.log.message", "error.exception.message", "span.name", "transaction.name"} {
		if message := stringValue(lookup(row, name), 4096); message != "" {
			return message
		}
	}
	if errorValue, ok := row["error"].(map[string]any); ok {
		if exceptions, ok := errorValue["exception"].([]any); ok && len(exceptions) > 0 {
			if first, ok := exceptions[0].(map[string]any); ok {
				return stringValue(first["message"], 4096)
			}
		}
	}
	return ""
}
func (c *Client) redact(s string) string {
	for _, cc := range []ComponentCredentials{c.credentials.primary(c.cfg.AuthType), c.credentials.Kibana, c.credentials.Logstash} {
		for _, secret := range []string{cc.APIKey, cc.Password, cc.BearerToken, cc.ClientCertPEM, cc.ClientKeyPEM} {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "[redacted]")
			}
		}
	}
	return analysis.ScrubProviderText(s)
}

// Trace identifiers retain their exact spelling, including opaque hex IDs,
// while exact runtime credential matches and structured disclosures are scrubbed.
func (c *Client) redactIdentity(value string) string {
	for _, cc := range []ComponentCredentials{c.credentials.primary(c.cfg.AuthType), c.credentials.Kibana, c.credentials.Logstash} {
		for _, secret := range []string{cc.APIKey, cc.Password, cc.BearerToken, cc.ClientCertPEM, cc.ClientKeyPEM} {
			if secret != "" {
				value = strings.ReplaceAll(value, secret, scrub.Redacted)
			}
		}
	}
	return analysis.ScrubProviderLabel(value)
}

func sensitiveProjectionName(name string) bool {
	for _, part := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return r == '.' || r == '_' || r == '-' }) {
		switch part {
		case "email", "ssn", "telephone", "phone", "creditcard":
			return true
		}
	}
	return false
}
