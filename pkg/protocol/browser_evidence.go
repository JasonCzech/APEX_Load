package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const BrowserEvidenceVersion = 2
const MaxBrowserDocumentEvidenceBytes = 512 * 1024
const BrowserVitalsAlgorithm = "web-vitals/5.1.0"
const BrowserVitalsAlgorithmSHA256 = "8890ec954757ea8e4801f49958afb76e2b8837c47fec5b16598fc20cbd49c220"
const MaxBrowserDocumentObservations = 256
const MaxBrowserDocumentMetricTuples = 64

// All phases are offsets from EpochStartMS, never epochs or server processing.
// A nil endpoint preserves unavailable native timings; zero is an observation.
type BrowserResourcePhase struct {
	Name              string   `json:"name"`
	StartMS           *float64 `json:"start_ms,omitempty"`
	EndMS             *float64 `json:"end_ms,omitempty"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}
type BrowserResourceBytes struct {
	RequestHeaders      *int64 `json:"request_headers,omitempty"`
	RequestBody         *int64 `json:"request_body,omitempty"`
	ResponseHeaders     *int64 `json:"response_headers,omitempty"`
	ResponseEncodedBody *int64 `json:"response_encoded_body,omitempty"`
	UnavailableReason   string `json:"unavailable_reason,omitempty"`
}
type BrowserResourceEvidence struct {
	Version                        int                    `json:"version"`
	SourceGeneration               string                 `json:"source_generation"`
	RequestID                      string                 `json:"request_id"`
	FrameID                        string                 `json:"frame_id,omitempty"`
	DocumentID                     string                 `json:"document_id,omitempty"`
	NavigationID                   string                 `json:"navigation_id,omitempty"`
	RedirectChainID                string                 `json:"redirect_chain_id,omitempty"`
	RedirectFromRequestID          string                 `json:"redirect_from_request_id,omitempty"`
	RedirectHop                    int                    `json:"redirect_hop"`
	InitiatingActionID             string                 `json:"initiating_action_id,omitempty"`
	ObservationActionID            string                 `json:"observation_action_id,omitempty"`
	Origin                         string                 `json:"origin,omitempty"`
	IdentityUnavailableReason      string                 `json:"identity_unavailable_reason,omitempty"`
	EpochStartMS                   *float64               `json:"epoch_start_ms,omitempty"`
	DocumentOffsetMS               *float64               `json:"document_offset_ms,omitempty"`
	ObservedAt                     time.Time              `json:"observed_at"`
	TimingSource                   string                 `json:"timing_source"`
	Phases                         []BrowserResourcePhase `json:"phases"`
	Bytes                          BrowserResourceBytes   `json:"bytes"`
	ServiceWorker                  *bool                  `json:"service_worker,omitempty"`
	ServiceWorkerUnavailableReason string                 `json:"service_worker_unavailable_reason,omitempty"`
	Cache                          *bool                  `json:"cache,omitempty"`
	CacheUnavailableReason         string                 `json:"cache_unavailable_reason,omitempty"`
	InitiatorUnavailableReason     string                 `json:"initiator_unavailable_reason,omitempty"`
	State                          string                 `json:"state"` // finished, failed, censored
	// DocumentAssociation names how DocumentID was proven: cdp_loader_join
	// (Chromium loader of the issuing document) or page_resource_timing_join
	// (a unique Resource Timing entry inside one document).
	DocumentAssociation string `json:"document_association,omitempty"`
	// InitiatorType is the browser's initiator category; InitiatorOrigin is
	// only the scheme and host of the initiating document or script. Stacks,
	// paths and query strings are never retained.
	InitiatorType   string `json:"initiator_type,omitempty"`
	InitiatorOrigin string `json:"initiator_origin,omitempty"`
}

// BrowserResourceInitiatorTypes are the CDP initiator categories retained.
var BrowserResourceInitiatorTypes = []string{"parser", "script", "preload", "preflight", "signed_exchange", "other"}

// A nil cohort or an unapplied policy never qualifies an observation. Values
// describe the actual launched context and successfully applied shaping, not
// merely requested authoring options. Pointer policies distinguish absence
// from an observed false value across historical archives and older peers.
type BrowserRuntimeCohort struct {
	Version              int     `json:"version"`
	BrowserVersion       string  `json:"browser_version"`
	Platform             string  `json:"platform"`
	ViewportWidth        int     `json:"viewport_width"`
	ViewportHeight       int     `json:"viewport_height"`
	Headless             *bool   `json:"headless,omitempty"`
	Mobile               *bool   `json:"mobile,omitempty"`
	Touch                *bool   `json:"touch,omitempty"`
	DeviceScaleFactor    float64 `json:"device_scale_factor"`
	CPUThrottleRate      float64 `json:"cpu_throttle_rate"`
	NetworkLatencyMS     int     `json:"network_latency_ms"`
	NetworkBandwidthKbps int     `json:"network_bandwidth_kbps"`
	ShapingApplied       bool    `json:"shaping_applied"`
	CacheEnabled         *bool   `json:"cache_enabled,omitempty"`
	CacheContentURLs     *bool   `json:"cache_content_urls,omitempty"`
	CheckNewerVersions   *bool   `json:"check_newer_versions,omitempty"`
	NewUserPolicy        string  `json:"new_user_policy"`
	NewUserPolicyApplied bool    `json:"new_user_policy_applied"`
	UnavailableReason    string  `json:"unavailable_reason,omitempty"`
}

func browserEvidenceText(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}
func (c *BrowserRuntimeCohort) Valid() bool {
	if c == nil {
		return false
	}
	if c.Version != 1 || !browserEvidenceText(c.BrowserVersion, 128) || !browserEvidenceReason(c.UnavailableReason) || c.ViewportWidth < 0 || c.ViewportHeight < 0 || c.ViewportWidth > 8192 || c.ViewportHeight > 8192 || int64(c.ViewportWidth)*int64(c.ViewportHeight) > 16<<20 || !browserFinite(c.DeviceScaleFactor, 4) || !browserFinite(c.CPUThrottleRate, 100) || c.NetworkLatencyMS < 0 || c.NetworkLatencyMS > 3600000 || c.NetworkBandwidthKbps < 0 || c.NetworkBandwidthKbps > 100000000 {
		return false
	}
	switch c.Platform {
	case "", "linux", "darwin", "windows", "freebsd", "openbsd", "netbsd":
	default:
		return false
	}
	switch c.NewUserPolicy {
	case "", "persistent_context", "context_each_iteration", "cookies_each_iteration", "cookies_and_cache_each_iteration":
	default:
		return false
	}
	return true
}
func (c *BrowserRuntimeCohort) Eligible() bool {
	return c.Valid() && c.BrowserVersion != "" && c.Platform != "" && c.ViewportWidth > 0 && c.ViewportHeight > 0 && c.Headless != nil && c.Mobile != nil && c.Touch != nil && c.DeviceScaleFactor > 0 && c.CPUThrottleRate >= 1 && c.ShapingApplied && c.CacheEnabled != nil && c.CacheContentURLs != nil && c.CheckNewerVersions != nil && c.NewUserPolicy != "" && c.NewUserPolicyApplied && c.UnavailableReason == ""
}
func (c *BrowserRuntimeCohort) Clone() *BrowserRuntimeCohort {
	if c == nil {
		return nil
	}
	v := *c
	for _, p := range []**bool{&v.Headless, &v.Mobile, &v.Touch, &v.CacheEnabled, &v.CacheContentURLs, &v.CheckNewerVersions} {
		if *p != nil {
			n := **p
			*p = &n
		}
	}
	return &v
}

// Observations upsert by MeasurementID/Revision. They are document lifecycle
// measurements, independent of action durations and legacy ClientMetrics.
type BrowserDocumentObservation struct {
	Version           int                   `json:"version"`
	SourceGeneration  string                `json:"source_generation"`
	DocumentID        string                `json:"document_id"`
	LifecycleID       string                `json:"lifecycle_id"`
	FrameID           string                `json:"frame_id"`
	NavigationID      string                `json:"navigation_id"`
	MeasurementID     string                `json:"measurement_id"`
	Revision          uint64                `json:"revision"`
	Metric            string                `json:"metric"`
	Algorithm         string                `json:"algorithm"`
	AlgorithmSHA256   string                `json:"algorithm_sha256"`
	Unit              string                `json:"unit"`
	Engine            string                `json:"engine"`
	Group             string                `json:"group,omitempty"`
	Region            string                `json:"region,omitempty"`
	DeviceProfile     string                `json:"device_profile"`
	Cohort            *BrowserRuntimeCohort `json:"cohort,omitempty"`
	FrameScope        string                `json:"frame_scope"`     // main_frame or child_frame; never whole-page aggregate.
	LifecyclePhase    string                `json:"lifecycle_phase"` // navigation or bfcache_restore.
	State             string                `json:"state"`           // provisional, final or censored.
	Boundary          string                `json:"boundary"`
	WindowStart       time.Time             `json:"window_start"`
	WindowEnd         time.Time             `json:"window_end"`
	CapturedAt        time.Time             `json:"captured_at"`
	Available         bool                  `json:"available"`
	Qualified         bool                  `json:"qualified"`
	UnavailableReason string                `json:"unavailable_reason,omitempty"`
	Value             *float64              `json:"value,omitempty"`
	InteractionCount  int64                 `json:"interaction_count,omitempty"`
}
type BrowserDocumentEvidence struct {
	Version                  int                          `json:"version"`
	SourceGeneration         string                       `json:"source_generation"`
	Observations             []BrowserDocumentObservation `json:"observations"`
	DroppedObservations      int64                        `json:"dropped_observations"`
	Incomplete               bool                         `json:"incomplete"`
	IssuedRequests           int64                        `json:"issued_requests"`
	InFlightRequests         int64                        `json:"in_flight_requests"`
	DroppedRequestIdentities int64                        `json:"dropped_request_identities"`
}

// Reads are bounded local copies and never evaluate target code or issue probes.
type BrowserDocumentEvidenceProvider interface {
	BrowserDocumentEvidence() *BrowserDocumentEvidence
}

type BrowserMetricDistribution struct {
	Count   int64      `json:"count"`
	Sum     float64    `json:"sum"`
	Min     float64    `json:"min"`
	Max     float64    `json:"max"`
	Buckets [192]int64 `json:"buckets"`
}

// Bucket i has upper bound 0.000001*1.2^i in the explicitly named metric unit;
// final bucket is overflow. Quantiles are approximate; min/max/sum are observed.
func (d *BrowserMetricDistribution) Add(v float64) {
	if d == nil || !browserFinite(v, 86400000) || d.Count >= 1e12 {
		return
	}
	i := 0
	if v > 0.000001 {
		i = min(len(d.Buckets)-1, int(math.Ceil(math.Log(v/0.000001)/math.Log(1.2))))
	}
	if d.Count == 0 || v < d.Min {
		d.Min = v
	}
	d.Max = max(d.Max, v)
	d.Count++
	d.Sum += v
	d.Buckets[i]++
}
func (d BrowserMetricDistribution) Valid() bool {
	if d.Count < 0 || d.Count > 1e12 || !browserFinite(d.Sum, 86400000*1e12) || !browserFinite(d.Min, 86400000) || !browserFinite(d.Max, 86400000) || d.Max < d.Min {
		return false
	}
	var n int64
	for i, b := range d.Buckets {
		if b < 0 || b > d.Count-n {
			return false
		}
		n += b
		if b > 0 {
			lower := 0.0
			if i > 0 {
				lower = 0.000001 * math.Pow(1.2, float64(i-1))
			}
			upper := math.Inf(1)
			if i < len(d.Buckets)-1 {
				upper = 0.000001 * math.Pow(1.2, float64(i))
			}
			rounding := max(1e-15, math.Max(math.Abs(lower), math.Abs(upper))*1e-12)
			if math.IsInf(upper, 1) {
				rounding = max(1e-15, lower*1e-12)
			}
			if d.Max < lower-rounding || d.Min > upper+rounding {
				return false
			}
		}
	}
	// Sequential additions and merges accumulate rounding error. Bound the
	// relative allowance by operation count, with a hard 10ppm ceiling. This
	// accepts millions of legitimate fractional observations without permitting
	// a materially forged sum outside the observed min/max interval.
	relative := min(0.00001, max(64*(math.Nextafter(1, 2)-1), float64(d.Count)*4*(math.Nextafter(1, 2)-1)))
	tolerance := max(0.000000001, d.Max*float64(d.Count)*relative)
	return n == d.Count && (d.Count != 0 || d.Sum == 0 && d.Min == 0 && d.Max == 0) && (d.Count == 0 || d.Sum >= d.Min*float64(d.Count)-tolerance && d.Sum <= d.Max*float64(d.Count)+tolerance)
}
func (d *BrowserMetricDistribution) Merge(other BrowserMetricDistribution) bool {
	if d == nil || !d.Valid() || !other.Valid() || other.Count > 1e12-d.Count {
		return false
	}
	if other.Count == 0 {
		return true
	}
	if d.Count == 0 || other.Min < d.Min {
		d.Min = other.Min
	}
	d.Max = max(d.Max, other.Max)
	d.Count += other.Count
	d.Sum += other.Sum
	for i, b := range other.Buckets {
		d.Buckets[i] += b
	}
	return true
}

// Terminal counts are interval transitions, each admitted once per stable
// measurement ID. Provisional counts the first sight of a still-provisional
// measurement, never later revisions; it may subsequently reach a terminal
// state and is never included in a metric denominator or distribution.
type BrowserDocumentMetricSummary struct {
	Metric           string                    `json:"metric"`
	Algorithm        string                    `json:"algorithm"`
	AlgorithmSHA256  string                    `json:"algorithm_sha256"`
	Unit             string                    `json:"unit"`
	Engine           string                    `json:"engine"`
	Group            string                    `json:"group,omitempty"`
	Region           string                    `json:"region,omitempty"`
	DeviceProfile    string                    `json:"device_profile"`
	Cohort           *BrowserRuntimeCohort     `json:"cohort,omitempty"`
	FrameScope       string                    `json:"frame_scope"`
	LifecyclePhase   string                    `json:"lifecycle_phase"`
	EligibleFinal    int64                     `json:"eligible_final"`
	ExcludedReasons  map[string]int64          `json:"excluded_reasons,omitempty"`
	UnqualifiedFinal int64                     `json:"unqualified_final"`
	UnavailableFinal int64                     `json:"unavailable_final"`
	Censored         int64                     `json:"censored"`
	Provisional      int64                     `json:"provisional"`
	Distribution     BrowserMetricDistribution `json:"distribution"`
}
type BrowserDocumentSummary struct {
	Version             int                            `json:"version"`
	Metrics             []BrowserDocumentMetricSummary `json:"metrics"`
	DroppedObservations int64                          `json:"dropped_observations"`
	Incomplete          bool                           `json:"incomplete"`
}

func browserFinite(v, maxValue float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= maxValue
}
func BrowserEvidenceGeneration(v string) bool {
	if len(v) != 32 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil && v == strings.ToLower(v)
}
func browserEvidenceID(v, generation string, optional bool) bool {
	if v == "" {
		return optional
	}
	if len(v) > 128 || !strings.HasPrefix(v, generation+"-") {
		return false
	}
	for _, r := range v {
		if r != '-' && r != '/' && r != '_' && r != '.' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
func browserEvidenceReason(v string) bool {
	switch v {
	case "", "not_exposed", "unsupported", "no_observation", "no_interaction", "hidden_before_paint", "not_loaded", "frame_unavailable", "document_unavailable", "identity_capacity", "source_censored", "source_incomplete", "measurement_capacity", "malformed", "cohort_unavailable", "shaping_unavailable", "new_user_policy_unavailable", "scope_unqualified",
		"cdp_unobserved", "ambiguous_cdp_join", "document_not_observed", "ambiguous_document_commit", "resource_timing_unobserved", "ambiguous_resource_timing":
		return true
	}
	return false
}
func browserMetricUnit(metric string) string {
	switch metric {
	case "cls":
		return "score"
	case "fcp", "lcp", "inp", "ttfb":
		return "ms"
	}
	return ""
}
func (o BrowserDocumentObservation) Valid() bool {
	if o.Version != BrowserEvidenceVersion || !BrowserEvidenceGeneration(o.SourceGeneration) || !browserEvidenceID(o.DocumentID, o.SourceGeneration, false) || !browserEvidenceID(o.FrameID, o.SourceGeneration, false) || !browserEvidenceID(o.NavigationID, o.SourceGeneration, false) || !browserEvidenceID(o.LifecycleID, o.SourceGeneration, false) || o.MeasurementID != o.DocumentID+"/"+o.LifecycleID+"/"+o.Metric || o.Revision < 1 || o.Algorithm != BrowserVitalsAlgorithm || o.AlgorithmSHA256 != BrowserVitalsAlgorithmSHA256 || o.Unit == "" || o.Unit != browserMetricUnit(o.Metric) || !browserEvidenceText(o.Group, 256) || !browserEvidenceText(o.Region, 256) || !browserEvidenceText(o.DeviceProfile, 128) || o.Cohort != nil && !o.Cohort.Valid() || o.Engine != "chromium" && o.Engine != "chrome" && o.Engine != "webkit" && o.Engine != "firefox" || o.FrameScope != "main_frame" && o.FrameScope != "child_frame" || o.LifecyclePhase != "navigation" && o.LifecyclePhase != "bfcache_restore" || o.WindowStart.IsZero() || o.WindowStart.Location() != time.UTC || o.WindowEnd.Location() != time.UTC || o.WindowEnd.Before(o.WindowStart) || o.CapturedAt.IsZero() || o.CapturedAt.Location() != time.UTC || !browserEvidenceReason(o.UnavailableReason) || o.InteractionCount < 0 || o.InteractionCount > 1e12 {
		return false
	}
	switch o.State {
	case "provisional", "final", "censored":
	default:
		return false
	}
	switch o.Boundary {
	case "progress", "first_input", "hidden", "pagehide", "navigation", "graceful_end", "context_closed", "frame_detached", "crash", "cancelled", "resource_limit", "bfcache_restore", "metric_observed":
	default:
		return false
	}
	if o.State == "final" {
		switch o.Metric {
		case "cls", "inp":
			if o.Boundary != "pagehide" && o.Boundary != "graceful_end" {
				return false
			}
		case "lcp":
			if o.Boundary != "first_input" && o.Boundary != "hidden" && o.Boundary != "pagehide" && o.Boundary != "graceful_end" {
				return false
			}
		case "fcp", "ttfb":
			if o.Boundary != "metric_observed" && o.Boundary != "pagehide" && o.Boundary != "graceful_end" {
				return false
			}
		}
	}
	if o.State == "censored" && o.Boundary != "context_closed" && o.Boundary != "frame_detached" && o.Boundary != "crash" && o.Boundary != "cancelled" && o.Boundary != "resource_limit" {
		return false
	}
	if o.Available != (o.Value != nil) || o.Available && (!browserFinite(*o.Value, 86400000) || o.UnavailableReason != "") || !o.Available && o.UnavailableReason == "" || o.Qualified && (!o.Available || o.State != "final" || o.Cohort == nil || !o.Cohort.Eligible()) || o.State == "censored" && (o.Available || o.Qualified) || o.Metric == "inp" && o.Available && o.InteractionCount == 0 {
		return false
	}
	return true
}

// PackBrowserDocumentEvidence bounds an owned producer snapshot to the shared
// serialized record limit. Omitted observations remain explicit coverage loss;
// this never promotes provisional/censored evidence into final measurements.
func PackBrowserDocumentEvidence(evidence *BrowserDocumentEvidence) *BrowserDocumentEvidence {
	owned := evidence.Clone()
	if owned == nil {
		return nil
	}
	data, err := json.Marshal(owned)
	if err == nil && len(data) <= MaxBrowserDocumentEvidenceBytes {
		return owned
	}
	observations := owned.Observations
	owned.Observations = nil
	header, err := json.Marshal(owned)
	if err != nil {
		return nil
	}
	// Leave room for explicit omitted-count digits and the incomplete flag.
	size := len(header) + 1024
	for _, observation := range observations {
		encoded, err := json.Marshal(observation)
		if err != nil || len(encoded)+1 > MaxBrowserDocumentEvidenceBytes-size {
			owned.DroppedObservations = min(int64(1e12), owned.DroppedObservations+1)
			owned.Incomplete = true
			continue
		}
		size += len(encoded) + 1
		owned.Observations = append(owned.Observations, observation)
	}
	return owned
}

// DecodeBrowserDocumentEvidence admits the bounded shared sidecar envelope.
// Unknown fields, extra JSON values and malformed typed evidence are refused;
// returned evidence owns every retained pointer and collection.
func DecodeBrowserDocumentEvidence(raw []byte) (*BrowserDocumentEvidence, error) {
	if len(raw) == 0 || len(raw) > MaxBrowserDocumentEvidenceBytes {
		return nil, fmt.Errorf("browser document evidence exceeds its record bounds")
	}
	if !browserDocumentWirePresence(raw, reflect.TypeOf(BrowserDocumentEvidence{}), MaxBrowserDocumentEvidenceBytes) {
		return nil, fmt.Errorf("browser document evidence is malformed or incomplete")
	}
	var evidence BrowserDocumentEvidence
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&evidence) != nil {
		return nil, fmt.Errorf("browser document evidence is malformed")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, fmt.Errorf("browser document evidence has trailing data")
	}
	owned := evidence.Clone()
	if owned == nil {
		return nil, fmt.Errorf("browser document evidence is malformed")
	}
	return owned, nil
}
func (e *BrowserDocumentEvidence) Clone() *BrowserDocumentEvidence {
	if e == nil {
		return nil
	}
	v := *e
	if len(v.Observations) > MaxBrowserDocumentObservations {
		return nil
	}
	v.Observations = append([]BrowserDocumentObservation(nil), e.Observations...)
	seen := make(map[string]bool, len(v.Observations))
	for i, o := range v.Observations {
		v.Observations[i].Cohort = o.Cohort.Clone()
		if !o.Valid() || seen[o.MeasurementID] {
			return nil
		}
		seen[o.MeasurementID] = true
		if o.Value != nil {
			x := *o.Value
			v.Observations[i].Value = &x
		}
	}
	if e.Version != BrowserEvidenceVersion || !BrowserEvidenceGeneration(e.SourceGeneration) || e.DroppedObservations < 0 || e.DroppedObservations > 1e12 || e.IssuedRequests < 0 || e.IssuedRequests > 1e12 || e.InFlightRequests < 0 || e.InFlightRequests > e.IssuedRequests || e.DroppedRequestIdentities < 0 || e.DroppedRequestIdentities > e.IssuedRequests {
		return nil
	}
	for _, o := range v.Observations {
		if o.SourceGeneration != e.SourceGeneration {
			return nil
		}
	}
	return &v
}
func browserEvidenceOrigin(v string) bool {
	if v == "" {
		return true
	}
	if !browserEvidenceText(v, 512) {
		return false
	}
	parsed, err := url.Parse(v)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && v == parsed.Scheme+"://"+parsed.Host
}
func (e *BrowserResourceEvidence) Clone() *BrowserResourceEvidence {
	if e == nil {
		return nil
	}
	v := *e
	if v.Version != BrowserEvidenceVersion || !BrowserEvidenceGeneration(v.SourceGeneration) || !browserEvidenceID(v.RequestID, v.SourceGeneration, false) || v.RedirectHop < 0 || v.RedirectHop > 64 || v.ObservedAt.IsZero() || v.ObservedAt.Location() != time.UTC || v.TimingSource != "playwright" || len(v.Phases) > 5 || !browserEvidenceOrigin(v.Origin) || !browserEvidenceReason(v.IdentityUnavailableReason) || !browserEvidenceReason(v.CacheUnavailableReason) || !browserEvidenceReason(v.ServiceWorkerUnavailableReason) || !browserEvidenceReason(v.InitiatorUnavailableReason) {
		return nil
	}
	for _, id := range []string{v.FrameID, v.DocumentID, v.NavigationID, v.RedirectChainID, v.RedirectFromRequestID, v.InitiatingActionID, v.ObservationActionID} {
		if !browserEvidenceID(id, v.SourceGeneration, true) {
			return nil
		}
	}
	if v.State != "finished" && v.State != "failed" && v.State != "censored" {
		return nil
	}
	switch v.DocumentAssociation {
	case "":
	case "cdp_loader_join", "page_resource_timing_join":
		if v.DocumentID == "" {
			return nil
		}
	default:
		return nil
	}
	if v.InitiatorType != "" && !slices.Contains(BrowserResourceInitiatorTypes, v.InitiatorType) || v.InitiatorType == "" && v.InitiatorOrigin != "" || !browserEvidenceOrigin(v.InitiatorOrigin) {
		return nil
	}
	cloneNumber := func(p *float64, max float64, negative bool) *float64 {
		if p == nil {
			return nil
		}
		if math.IsNaN(*p) || math.IsInf(*p, 0) || math.Abs(*p) > max || !negative && *p < 0 {
			return nil
		}
		n := *p
		return &n
	}
	v.EpochStartMS = cloneNumber(v.EpochStartMS, 253402300799999, false)
	if e.EpochStartMS != nil && v.EpochStartMS == nil {
		return nil
	}
	v.DocumentOffsetMS = cloneNumber(v.DocumentOffsetMS, 86400000, true)
	if e.DocumentOffsetMS != nil && v.DocumentOffsetMS == nil {
		return nil
	}
	v.Phases = append([]BrowserResourcePhase(nil), v.Phases...)
	seen := map[string]bool{}
	for i, p := range v.Phases {
		if seen[p.Name] || p.Name != "dns" && p.Name != "connect" && p.Name != "tls" && p.Name != "request_wait" && p.Name != "response" || !browserEvidenceReason(p.UnavailableReason) {
			return nil
		}
		seen[p.Name] = true
		v.Phases[i].StartMS = cloneNumber(p.StartMS, 86400000, false)
		v.Phases[i].EndMS = cloneNumber(p.EndMS, 86400000, false)
		if (p.StartMS != nil) != (v.Phases[i].StartMS != nil) || (p.EndMS != nil) != (v.Phases[i].EndMS != nil) || p.StartMS != nil && p.EndMS != nil && *p.EndMS < *p.StartMS || (p.StartMS == nil || p.EndMS == nil) != (p.UnavailableReason != "") {
			return nil
		}
	}
	for _, p := range []**int64{&v.Bytes.RequestHeaders, &v.Bytes.RequestBody, &v.Bytes.ResponseHeaders, &v.Bytes.ResponseEncodedBody} {
		if *p != nil {
			n := **p
			if n < 0 || n > 1<<50 {
				return nil
			}
			*p = &n
		}
	}
	if !browserEvidenceReason(v.Bytes.UnavailableReason) {
		return nil
	}
	if v.ServiceWorker != nil {
		n := *v.ServiceWorker
		v.ServiceWorker = &n
	}
	if v.Cache != nil {
		n := *v.Cache
		v.Cache = &n
	}
	return &v
}
func (s *BrowserDocumentSummary) Clone() *BrowserDocumentSummary {
	if s == nil {
		return nil
	}
	v := *s
	if v.Version != BrowserEvidenceVersion || len(v.Metrics) > MaxBrowserDocumentMetricTuples || v.DroppedObservations < 0 || v.DroppedObservations > 1e12 {
		return nil
	}
	v.Metrics = append([]BrowserDocumentMetricSummary(nil), v.Metrics...)
	seen := map[string]bool{}
	for i, m := range v.Metrics {
		key := BrowserDocumentMetricKey(m)
		if seen[key] || m.Unit == "" || m.Unit != browserMetricUnit(m.Metric) || m.Algorithm != BrowserVitalsAlgorithm || m.AlgorithmSHA256 != BrowserVitalsAlgorithmSHA256 || m.Engine != "chromium" && m.Engine != "chrome" && m.Engine != "webkit" && m.Engine != "firefox" || !browserEvidenceText(m.Group, 256) || !browserEvidenceText(m.Region, 256) || !browserEvidenceText(m.DeviceProfile, 128) || m.Cohort != nil && !m.Cohort.Valid() || m.EligibleFinal > 0 && (m.Cohort == nil || !m.Cohort.Eligible()) || m.FrameScope != "main_frame" && m.FrameScope != "child_frame" || m.LifecyclePhase != "navigation" && m.LifecyclePhase != "bfcache_restore" || !m.Distribution.Valid() || m.Distribution.Count != m.EligibleFinal {
			return nil
		}
		seen[key] = true
		v.Metrics[i].Cohort = m.Cohort.Clone()
		if len(m.ExcludedReasons) > 16 {
			return nil
		}
		if m.ExcludedReasons != nil {
			v.Metrics[i].ExcludedReasons = make(map[string]int64, len(m.ExcludedReasons))
			var total int64
			for reason, count := range m.ExcludedReasons {
				if reason == "" || !browserEvidenceReason(reason) || count < 0 || count > 1e12-total {
					return nil
				}
				total += count
				v.Metrics[i].ExcludedReasons[reason] = count
			}
			if total > m.UnavailableFinal+m.UnqualifiedFinal+m.Censored {
				return nil
			}
		}
		for _, n := range []int64{m.EligibleFinal, m.UnavailableFinal, m.UnqualifiedFinal, m.Censored, m.Provisional} {
			if n < 0 || n > 1e12 {
				return nil
			}
		}
	}
	return &v
}
func BrowserDocumentMetricKey(m BrowserDocumentMetricSummary) string {
	cohort, _ := json.Marshal(m.Cohort)
	return strings.Join([]string{m.Metric, m.Algorithm, m.AlgorithmSHA256, m.Unit, m.Engine, m.Group, m.Region, m.DeviceProfile, m.FrameScope, m.LifecyclePhase, string(cohort)}, "\x00")
}

// SummarizeBrowserDocumentObservations aggregates a bounded set of independently
// validated, latest-per-measurement observations. Callers own revision admission:
// duplicate IDs are refused rather than counted as additional documents. Live
// interval callers supply only first sightings and new terminal transitions;
// archive callers supply the latest retained lifecycle state once per ID.
func SummarizeBrowserDocumentObservations(observations []BrowserDocumentObservation, dropped int64, incomplete bool) *BrowserDocumentSummary {
	const maxObservations = 4096
	if dropped < 0 || dropped > 1e12 {
		incomplete = true
		dropped = min(int64(1e12), max(int64(0), dropped))
	}
	if len(observations) == 0 && dropped == 0 && !incomplete {
		return nil
	}
	summary := &BrowserDocumentSummary{Version: BrowserEvidenceVersion, DroppedObservations: dropped, Incomplete: incomplete}
	if len(observations) > maxObservations {
		summary.DroppedObservations = min(int64(1e12), summary.DroppedObservations+int64(len(observations)-maxObservations))
		summary.Incomplete = true
		observations = observations[:maxObservations]
	}
	tuples := make(map[string]*BrowserDocumentMetricSummary)
	seen := make(map[string]bool, len(observations))
	for _, o := range observations {
		if !o.Valid() || seen[o.MeasurementID] {
			summary.DroppedObservations = min(int64(1e12), summary.DroppedObservations+1)
			summary.Incomplete = true
			continue
		}
		seen[o.MeasurementID] = true
		m := BrowserDocumentMetricSummary{Metric: o.Metric, Algorithm: o.Algorithm, AlgorithmSHA256: o.AlgorithmSHA256, Unit: o.Unit, Engine: o.Engine, DeviceProfile: o.DeviceProfile, Cohort: o.Cohort.Clone(), Group: o.Group, Region: o.Region, FrameScope: o.FrameScope, LifecyclePhase: o.LifecyclePhase}
		key := BrowserDocumentMetricKey(m)
		tuple := tuples[key]
		if tuple == nil {
			if len(tuples) >= MaxBrowserDocumentMetricTuples {
				summary.DroppedObservations = min(int64(1e12), summary.DroppedObservations+1)
				summary.Incomplete = true
				continue
			}
			tuple = &m
			tuples[key] = tuple
		}
		reason := o.UnavailableReason
		switch o.State {
		case "provisional":
			tuple.Provisional++
		case "censored":
			tuple.Censored++
		case "final":
			if o.Qualified {
				tuple.EligibleFinal++
				tuple.Distribution.Add(*o.Value)
			} else if o.Available {
				tuple.UnqualifiedFinal++
				reason = "cohort_unavailable"
				if o.Cohort != nil && o.Cohort.UnavailableReason != "" {
					reason = o.Cohort.UnavailableReason
				}
			} else {
				tuple.UnavailableFinal++
			}
		}
		if reason != "" && o.State != "provisional" {
			if tuple.ExcludedReasons == nil {
				tuple.ExcludedReasons = map[string]int64{}
			}
			tuple.ExcludedReasons[reason]++
		}
	}
	keys := make([]string, 0, len(tuples))
	for key := range tuples {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		summary.Metrics = append(summary.Metrics, *tuples[key])
	}
	return summary.Clone()
}
func MergeBrowserDocumentSummaries(out **BrowserDocumentSummary, next *BrowserDocumentSummary) {
	if out == nil || next == nil {
		return
	}
	n := next.Clone()
	if n == nil {
		if *out == nil {
			*out = &BrowserDocumentSummary{Version: BrowserEvidenceVersion}
		}
		(*out).Incomplete = true
		return
	}
	if *out == nil {
		*out = n
		return
	}
	current := (*out).Clone()
	if current == nil {
		*out = &BrowserDocumentSummary{Version: BrowserEvidenceVersion, Incomplete: true}
		return
	}
	current.Incomplete = current.Incomplete || n.Incomplete
	current.DroppedObservations = min(int64(1e12), current.DroppedObservations+n.DroppedObservations)
	for _, incoming := range n.Metrics {
		index := -1
		for i, m := range current.Metrics {
			if BrowserDocumentMetricKey(m) == BrowserDocumentMetricKey(incoming) {
				index = i
				break
			}
		}
		if index < 0 {
			if len(current.Metrics) >= MaxBrowserDocumentMetricTuples {
				current.Incomplete = true
				current.DroppedObservations = min(int64(1e12), current.DroppedObservations+incoming.EligibleFinal+incoming.UnavailableFinal+incoming.UnqualifiedFinal+incoming.Censored+incoming.Provisional)
				continue
			}
			current.Metrics = append(current.Metrics, incoming)
			continue
		}
		m := &current.Metrics[index]
		if incoming.EligibleFinal > 1e12-m.EligibleFinal || incoming.UnavailableFinal > 1e12-m.UnavailableFinal || incoming.UnqualifiedFinal > 1e12-m.UnqualifiedFinal || incoming.Censored > 1e12-m.Censored || incoming.Provisional > 1e12-m.Provisional || !m.Distribution.Merge(incoming.Distribution) {
			current.Incomplete = true
			continue
		}
		m.EligibleFinal += incoming.EligibleFinal
		m.UnavailableFinal += incoming.UnavailableFinal
		m.UnqualifiedFinal += incoming.UnqualifiedFinal
		m.Censored += incoming.Censored
		m.Provisional += incoming.Provisional
		if m.ExcludedReasons == nil && len(incoming.ExcludedReasons) > 0 {
			m.ExcludedReasons = map[string]int64{}
		}
		for reason, count := range incoming.ExcludedReasons {
			m.ExcludedReasons[reason] = min(int64(1e12), m.ExcludedReasons[reason]+count)
		}
	}
	*out = current
}

// Runtime measurements describe their native source and scope independently
// from document vitals. Legacy scalar zeros cannot prove this availability.
type BrowserRuntimeMetricAvailability struct {
	Version int                              `json:"version"`
	Metrics []BrowserRuntimeMetricCapability `json:"metrics"`
}
type BrowserRuntimeMetricCapability struct {
	Metric            string `json:"metric"`
	Source            string `json:"source"`
	Scope             string `json:"scope"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

func (c *BrowserRuntimeMetricAvailability) Clone() *BrowserRuntimeMetricAvailability {
	if c == nil {
		return nil
	}
	if c.Version != 1 || len(c.Metrics) > 16 {
		return nil
	}
	out := *c
	out.Metrics = append([]BrowserRuntimeMetricCapability(nil), c.Metrics...)
	seen := map[string]bool{}
	for _, m := range out.Metrics {
		switch m.Metric {
		case "js_heap_used_mb", "js_heap_total_mb", "layout_count", "recalc_style_count", "script_duration_ms", "layout_duration_ms", "task_duration_ms", "browser_process_cpu_pct", "browser_process_mem_mb":
		default:
			return nil
		}
		if seen[m.Metric] || !browserEvidenceReason(m.UnavailableReason) || m.Available != (m.UnavailableReason == "") {
			return nil
		}
		seen[m.Metric] = true
		switch m.Source {
		case "cdp", "page_performance_memory", "driver_process_tree":
		default:
			return nil
		}
		switch m.Scope {
		case "active_page", "driver_process_tree":
		default:
			return nil
		}
	}
	return &out
}
