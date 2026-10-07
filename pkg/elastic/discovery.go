package elastic

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func versionAtLeast(v string, major, minor int) bool {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return false
	}
	a, e := strconv.Atoi(parts[0])
	if e != nil {
		return false
	}
	b, e := strconv.Atoi(parts[1])
	return e == nil && (a > major || a == major && b >= minor)
}

// Verify distinguishes unavailable discovery permissions from unsupported
// products. Capability status describes the probe, not a promise for every index.
func (c *Client) Verify(ctx context.Context, indices []string) (Capabilities, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	if !c.cleanupCapacity(nil, 2) {
		return Capabilities{}, errors.New("pending inspection cleanup must be resolved before new probes")
	}
	cap := Capabilities{Product: c.cfg.Product, Status: map[string]string{}}
	policy := c.verificationPolicySnapshot()
	var info struct {
		Version struct {
			Number       string `json:"number"`
			Distribution string `json:"distribution"`
			BuildFlavor  string `json:"build_flavor"`
		} `json:"version"`
		Tagline string `json:"tagline"`
	}
	if e := c.request(ctx, "GET", "/", nil, &info); e != nil {
		return cap, e
	}
	cap.Version = info.Version.Number
	cap.Serverless = info.Version.BuildFlavor == "serverless"
	if c.cfg.Product == ProductOpenSearch {
		if info.Version.Distribution != "opensearch" && !strings.Contains(info.Tagline, "OpenSearch") {
			return cap, errors.New("endpoint does not identify as OpenSearch")
		}
		if !versionAtLeast(cap.Version, 2, 19) {
			return cap, errors.New("OpenSearch version below qualification floor 2.19")
		}
		cap.ShardDoc = false
	} else {
		if info.Version.Distribution == "opensearch" || strings.Contains(info.Tagline, "OpenSearch") {
			return cap, errors.New("endpoint product does not match Elasticsearch")
		}
		if !cap.Serverless && !versionAtLeast(cap.Version, 8, 14) {
			cap.Status["version"] = "unqualified_or_serverless_compatibility"
		}
		cap.ShardDoc = true
	}
	c.mu.Lock()
	c.capabilities = applyVerificationPolicy(Capabilities{Product: cap.Product, Version: cap.Version, Serverless: cap.Serverless, ShardDoc: cap.ShardDoc, Status: map[string]string{}}, policy)
	c.mu.Unlock()
	cap.Status["dsl"] = "not_probed"
	cap.Status["pit"] = "not_probed"
	cap.Status["health"] = "not_requested"
	if c.cfg.Product == ProductElasticsearch && policy.ESQL {
		var r struct {
			Columns []struct {
				Name string `json:"name"`
			} `json:"columns"`
			Values [][]any `json:"values"`
		}
		if e := c.request(ctx, "POST", "/_query", map[string]any{"query": "ROW probe = 1 | LIMIT 1"}, &r); e == nil && len(r.Values) == 1 {
			cap.ESQL = true
			cap.Status["esql"] = "available"
		} else {
			cap.Status["esql"] = "unavailable"
		}
	} else if c.cfg.Product == ProductOpenSearch {
		cap.Status["esql"] = "unsupported"
	} else {
		cap.Status["esql"], cap.Status["async_esql"] = "locally_disabled", "locally_disabled"
	}
	if len(indices) > 0 {
		if e := validateIndices(indices); e != nil {
			return cap, e
		}
		var search searchResponse
		if e := c.request(ctx, "POST", "/"+indexPath(indices)+"/_search?allow_partial_search_results=false", map[string]any{"size": 0, "_source": false, "query": map[string]any{"match_none": map[string]any{}}}, &search); e == nil && checkSearch(search) == nil {
			cap.DSL = true
			cap.Metrics = true
			cap.Status["dsl"] = "available"
		} else {
			cap.Status["dsl"] = "unavailable"
		}
		pitOpenedAt := time.Now().UTC()
		var probeCursor *Continuation
		id, pitErr := c.openPIT(ctx, indices)
		if id != "" {
			probeCursor = &Continuation{QueryID: "inspection-pit", PITID: id, WindowFrom: pitOpenedAt, WindowUntil: pitOpenedAt.Add(time.Nanosecond), KeepAliveUntil: time.Now().UTC().Add(2 * time.Minute)}
			defer func() { c.retainFailedCleanup(ctx, probeCursor, nil) }()
		}
		if pitErr == nil {
			cap.PIT, cap.Logs, cap.Traces = true, true, true
			cap.Status["pit"] = "available"
			if c.cfg.Product == ProductOpenSearch {
				nativeErr := c.searchPIT(ctx, probeCursor, map[string]any{"size": 0, "_source": false, "pit": map[string]any{"id": id, "keep_alive": pitKeepAlive}, "sort": []any{map[string]any{"_shard_doc": "asc"}}}, &search)
				if nativeErr == nil {
					nativeErr = checkSearch(search)
				}
				if nativeErr == nil {
					cap.ShardDoc = true
					cap.Status["shard_doc"] = "available"
				} else {
					cap.ShardDoc = false
					cap.Status["shard_doc"] = "unavailable"
				}
			}
		} else {
			cap.Status["pit"] = "unavailable"
		}

		if _, e := c.DiscoverFields(ctx, indices); e != nil {
			cap.Status["fields"] = "unavailable"
		} else {
			cap.Status["fields"] = "available"
		}
	}
	if cap.ESQL {
		var response esqlResponse
		probeErr := c.request(ctx, "POST", "/_query/async?format=json", map[string]any{"query": "ROW probe = 1 | LIMIT 1", "wait_for_completion_timeout": "1s", "keep_alive": "1m", "keep_on_completion": true}, &response)
		if validOwnedHandle(response.ID) {
			probeAt := time.Now().UTC()
			c.retainFailedCleanup(ctx, &Continuation{QueryID: "inspection-async", AsyncID: response.ID, WindowFrom: probeAt, WindowUntil: probeAt.Add(time.Nanosecond), KeepAliveUntil: probeAt.Add(time.Minute)}, nil)
		}
		cap.Status["async_esql"] = "unavailable"
		if probeErr == nil {
			cap.AsyncESQL = !response.Partial && (validOwnedHandle(response.ID) || response.ID == "" && len(response.Values) == 1)
			if cap.AsyncESQL {
				cap.Status["async_esql"] = "available"
			}
		}
	}

	if policy.Health {
		var health map[string]any
		if e := c.request(ctx, "GET", "/_cluster/health", nil, &health); e == nil && health["status"] != nil {
			cap.Health = true
			cap.Status["health"] = "available"
		} else {
			cap.Status["health"] = "unavailable"
		}
	}
	for _, component := range []string{"kibana", "logstash"} {
		if !policy.Health {
			continue
		}
		if c.componentHTTP[component] == nil {
			continue
		}
		path := "/api/status"
		if component == "logstash" {
			path = "/_node/stats/jvm,process,events,pipelines"
		}
		var result map[string]any
		if e := c.requestAt(ctx, component, "GET", path, nil, &result, false); e == nil {
			if component == "kibana" {
				cap.KibanaHealth = true
			} else {
				cap.LogstashHealth = true
			}
			cap.Status[component] = "available"
		} else {
			cap.Status[component] = "unavailable"
		}
	}
	c.mu.Lock()
	cap = applyVerificationPolicy(cap, c.verificationPolicy)
	stored := cap
	stored.Status = map[string]string{}
	for key, value := range cap.Status {
		stored.Status[key] = value
	}
	c.capabilities = stored
	c.mu.Unlock()
	return cap, nil
}
func indexPath(indices []string) string { return url.PathEscape(strings.Join(indices, ",")) }
func (c *Client) DiscoverIndices(ctx context.Context, patterns []string) ([]Index, error) {
	if e := validateIndices(patterns); e != nil {
		return nil, e
	}
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	var r struct {
		Indices []struct {
			Name string `json:"name"`
		} `json:"indices"`
		Aliases []struct {
			Name string `json:"name"`
		} `json:"aliases"`
		DataStreams []struct {
			Name string `json:"name"`
		} `json:"data_streams"`
	}
	if e := c.request(ctx, "GET", "/_resolve/index/"+indexPath(patterns)+"?expand_wildcards=open", nil, &r); e != nil {
		return nil, e
	}
	out := []Index{}
	seen := map[string]bool{}
	add := func(name, kind string) {
		if !strings.HasPrefix(name, ".") && !seen[name] && len(out) < 200 && len(name) <= 255 {
			out = append(out, Index{Name: name, Type: kind})
			seen[name] = true
		}
	}
	for _, v := range r.Indices {
		add(v.Name, "index")
	}
	for _, v := range r.Aliases {
		add(v.Name, "alias")
	}
	for _, v := range r.DataStreams {
		add(v.Name, "data_stream")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *Client) DiscoverFields(ctx context.Context, indices []string) ([]Field, error) {
	if e := validateIndices(indices); e != nil {
		return nil, e
	}
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	var r struct {
		Fields map[string]map[string]struct {
			Searchable             bool     `json:"searchable"`
			Aggregatable           bool     `json:"aggregatable"`
			NonSearchableIndices   []string `json:"non_searchable_indices"`
			NonAggregatableIndices []string `json:"non_aggregatable_indices"`
		} `json:"fields"`
	}
	if e := c.request(ctx, "POST", "/"+indexPath(indices)+"/_field_caps?fields=*&include_unmapped=true", nil, &r); e != nil {
		return nil, e
	}
	if len(r.Fields) > 10000 {
		return nil, errors.New("field discovery exceeds catalog bound")
	}
	out := []Field{}
	for name, types := range r.Fields {
		if !fieldName.MatchString(name) || strings.HasPrefix(name, "_") {
			continue
		}
		f := Field{Name: name, Searchable: true, Aggregatable: true}
		for typ, v := range types {
			if typ == "unmapped" {
				f.PartiallyMapped = true
				continue
			}
			f.Types = append(f.Types, typ)
			f.Searchable = f.Searchable && v.Searchable && len(v.NonSearchableIndices) == 0
			f.Aggregatable = f.Aggregatable && v.Aggregatable && len(v.NonAggregatableIndices) == 0
		}
		sort.Strings(f.Types)
		f.Conflict = len(f.Types) > 1
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *Client) capabilitiesSnapshot() Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.capabilities
	v.Status = map[string]string{}
	for k, s := range c.capabilities.Status {
		v.Status[k] = s
	}
	return v
}

// UseVerifiedCapabilities is a controller/collector seam for an immutable,
// saved-revision readiness result. Collection still validates every response.
// Public source JSON cannot supply these capabilities.
func (c *Client) UseVerifiedCapabilities(cap Capabilities) error {
	if cap.Product != c.cfg.Product || cap.Version == "" || len(cap.Version) > 128 || len(cap.Status) > 32 {
		return errors.New("verified capabilities do not match destination")
	}
	for key, value := range cap.Status {
		if len(key) > 128 || len(value) > 128 {
			return errors.New("invalid capability metadata")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	copy := cap
	copy.Status = map[string]string{}
	for key, value := range cap.Status {
		copy.Status[key] = value
	}
	c.capabilities = applyVerificationPolicy(copy, c.verificationPolicy)
	return nil
}
func (c *Client) SetCapabilities(cap Capabilities) error { return c.UseVerifiedCapabilities(cap) }
