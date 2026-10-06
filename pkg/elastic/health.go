package elastic

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Completed snapshots retain their observation time between polls. Once a
// settled final snapshot is accepted, later reconciliation passes repeat its
// coverage without creating a new canonical instant measurement. Unsuccessful
// snapshots retry at the normal bounded cadence; they cannot freeze coverage.
func (c *Client) reuseHealthCoverage(s Selection, t task, from, to, now time.Time) (Coverage, bool) {
	if t.health == "" {
		return Coverage{}, false
	}
	prior, ok := s.HealthCoverage[t.id]
	if !ok || prior.QueryID != t.id || prior.AsOf == nil || prior.AsOf.After(now) {
		return Coverage{}, false
	}
	final := !s.FinalHealthAfter.IsZero()
	successful := !prior.Truncated && (prior.Status == "complete" || prior.Status == "empty")
	settled := final && successful && !now.Before(s.FinalHealthAfter) && !prior.AsOf.Before(s.FinalHealthAfter)
	// The first post-settlement snapshot is required even when the last live
	// snapshot was less than thirty seconds ago.
	if final && successful && !now.Before(s.FinalHealthAfter) && !settled {
		return Coverage{}, false
	}
	if !settled && now.Sub(*prior.AsOf) >= 30*time.Second {
		return Coverage{}, false
	}
	asOf := prior.AsOf.UTC()
	return Coverage{QueryID: t.id, Status: prior.Status, Truncated: prior.Truncated, From: from, To: to,
		AsOf: &asOf, LateArrivalSeconds: s.OverlapSeconds,
		Reason: stringValue(c.redact(prior.Reason), 128), Message: stringValue(c.redact(prior.Message), 256)}, true
}

// Health APIs expose snapshots, not event buckets. In particular a cumulative
// indexing/event counter is never presented as a per-interval count or rate.
func (c *Client) collectHealth(ctx context.Context, s Selection, t task, from, to time.Time, runID string, result *Result) error {
	appendFields := func(prefix string, row map[string]any, dimensions map[string]string, fields []healthField) error {
		for _, field := range fields {
			v := lookup(row, field.path)
			if v == nil {
				continue
			}
			value, err := mappedNumber(v)
			if err != nil {
				return err
			}
			if len(result.Points) >= c.cfg.MaxPoints {
				return ErrTruncated
			}
			at := time.Now().UTC()
			point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: s.ProjectID, RunID: runID, Source: c.cfg.Product, MetricName: prefix + field.name, Kind: field.kind, Unit: field.unit, Value: value, Quality: analysis.QualityValid, TimestampUTC: at, ReceivedAt: at, Dimensions: dimensions, Provider: &analysis.ProviderMeasurement{Name: c.cfg.Product, MetricID: t.id, QueryID: t.id, WindowKind: "instant", BucketStart: at, BucketEnd: at, Statistic: "value", Settled: true}}
			if err := point.Validate(); err != nil {
				return err
			}
			if err := c.appendPoint(result, point); err != nil {
				return err
			}
		}
		return nil
	}
	switch t.health {
	case "cluster":
		var health map[string]any
		if err := c.request(ctx, "GET", "/_cluster/health", nil, &health); err != nil {
			return err
		}
		if status, ok := health["status"].(string); ok {
			code, known := healthStatus(status)
			if !known {
				return errors.New("unknown cluster health status")
			}
			health["status_code"] = code
		}
		if err := appendFields(c.cfg.Product+".cluster.", health, nil, []healthField{{"status", "status_code", "gauge", "ratio"}, {"nodes", "number_of_nodes", "gauge", "count"}, {"active_shards", "active_shards", "gauge", "count"}, {"unassigned_shards", "unassigned_shards", "gauge", "count"}, {"pending_tasks", "number_of_pending_tasks", "gauge", "count"}, {"active_shards_percent", "active_shards_percent_as_number", "gauge", "percent"}}); err != nil {
			return err
		}
		var nodes struct {
			Nodes  map[string]map[string]any `json:"nodes"`
			Shards responseShards            `json:"_nodes"`
		}
		if err := c.request(ctx, "GET", "/_nodes/stats/jvm,process,indices?filter_path=_nodes.*,nodes.*.jvm.mem.heap_used_percent,nodes.*.jvm.mem.heap_used_in_bytes,nodes.*.process.cpu.percent,nodes.*.indices.indexing.index_total,nodes.*.indices.search.query_total", nil, &nodes); err != nil {
			return err
		}
		if nodes.Shards.partial() {
			return errors.New("node statistics are partial")
		}
		if len(nodes.Nodes) > 50 {
			return ErrTruncated
		}
		for _, id := range sortedMapKeys(nodes.Nodes) {
			if len(id) > 128 {
				return errors.New("invalid health entity identity")
			}
			if err := appendFields(c.cfg.Product+".node.", nodes.Nodes[id], map[string]string{"node": id}, []healthField{{"heap_percent", "jvm.mem.heap_used_percent", "gauge", "percent"}, {"heap_bytes", "jvm.mem.heap_used_in_bytes", "gauge", "bytes"}, {"cpu_percent", "process.cpu.percent", "gauge", "percent"}, {"index_total", "indices.indexing.index_total", "counter", "count"}, {"query_total", "indices.search.query_total", "counter", "count"}}); err != nil {
				return err
			}
		}
		if len(s.Indices) == 0 {
			return nil
		}
		var stats struct {
			Shards  responseShards            `json:"_shards"`
			Indices map[string]map[string]any `json:"indices"`
		}
		if err := c.request(ctx, "GET", "/"+indexPath(s.Indices)+"/_stats/docs,indexing,search,store?filter_path=_shards.*,indices.*.primaries.docs.count,indices.*.primaries.store.size_in_bytes,indices.*.primaries.indexing.index_total,indices.*.primaries.search.query_total", nil, &stats); err != nil {
			return err
		}
		if stats.Shards.partial() {
			return errors.New("index statistics are partial")
		}
		if len(stats.Indices) > 50 {
			return ErrTruncated
		}
		for _, index := range sortedMapKeys(stats.Indices) {
			if len(index) > 128 {
				return errors.New("health index identity exceeds bounds")
			}
			if err := appendFields(c.cfg.Product+".index.", stats.Indices[index], map[string]string{"index": index}, []healthField{{"documents", "primaries.docs.count", "gauge", "count"}, {"store_bytes", "primaries.store.size_in_bytes", "gauge", "bytes"}, {"index_total", "primaries.indexing.index_total", "counter", "count"}, {"query_total", "primaries.search.query_total", "counter", "count"}}); err != nil {
				return err
			}
		}
	case "kibana":
		var status map[string]any
		if err := c.requestAt(ctx, "kibana", "GET", "/api/status", nil, &status, false); err != nil {
			return err
		}
		level := lookup(status, "status.overall.level")
		if level == nil {
			level = lookup(status, "status.overall.state")
		}
		text, ok := level.(string)
		if !ok {
			return errors.New("Kibana status response lacks overall status")
		}
		code, ok := healthStatus(text)
		if !ok {
			return errors.New("unknown Kibana health status")
		}
		return appendFields("kibana.", map[string]any{"status": code}, nil, []healthField{{"status", "status", "gauge", "ratio"}})
	case "logstash":
		var stats map[string]any
		if err := c.requestAt(ctx, "logstash", "GET", "/_node/stats/jvm,process,events,pipelines", nil, &stats, false); err != nil {
			return err
		}
		if err := appendFields("logstash.", stats, nil, []healthField{{"heap_percent", "jvm.mem.heap_used_percent", "gauge", "percent"}, {"heap_bytes", "jvm.mem.heap_used_in_bytes", "gauge", "bytes"}, {"threads", "jvm.threads.count", "gauge", "threads"}, {"cpu_percent", "process.cpu.percent", "gauge", "percent"}, {"events_in", "events.in", "counter", "count"}, {"events_out", "events.out", "counter", "count"}}); err != nil {
			return err
		}
		pipelines, _ := stats["pipelines"].(map[string]any)
		if len(pipelines) > 50 {
			return ErrTruncated
		}
		for _, id := range sortedKeys(pipelines) {
			row, ok := pipelines[id].(map[string]any)
			if !ok || len(id) > 128 {
				return errors.New("invalid Logstash pipeline identity")
			}
			if err := appendFields("logstash.pipeline.", row, map[string]string{"pipeline": id}, []healthField{{"events_in", "events.in", "counter", "count"}, {"events_out", "events.out", "counter", "count"}, {"event_duration_ms", "events.duration_in_millis", "counter", "ms"}, {"queue_events", "queue.events_count", "gauge", "count"}, {"queue_bytes", "queue.queue_size_in_bytes", "gauge", "bytes"}}); err != nil {
				return err
			}
		}
	default:
		return errors.New("unsupported component health selection")
	}
	return nil
}

type healthField struct{ name, path, kind, unit string }

func sortedMapKeys(m map[string]map[string]any) []string {
	v := map[string]any{}
	for k := range m {
		v[k] = nil
	}
	return sortedKeys(v)
}
func healthStatus(value string) (float64, bool) {
	switch strings.ToLower(value) {
	case "green", "available":
		return 0, true
	case "yellow", "degraded":
		return 1, true
	case "red", "critical", "unavailable":
		return 2, true
	}
	return 0, false
}
