// Package harsynth writes HAR 1.2 from retained browser network evidence for
// sessions that have no native recording: older peers and contexts recorded
// without native capture. It writes only what the evidence holds. An optional
// timing phase the evidence lacks is HAR's -1; the required send, wait and
// receive and content.size, which HAR 1.2 requires to be non-negative, are 0
// and named in the entry's unavailable_fields. Content is never written, and
// every value that is not a direct measurement is labelled where it appears. Each
// entry passes through the same scrubber as a native HAR before it is written.
package harsynth

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"math"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	// Algorithm and AlgorithmVersion name this writer in every HAR it writes.
	Algorithm        = "apexload.synthetic_har"
	AlgorithmVersion = 1
	SchemaVersion    = "1.2"
	Fidelity         = "synthetic"
	// BodyPolicy: request and response bodies are never written.
	BodyPolicy = "omitted"

	// MaxEntries and MaxBytes match the native per-HAR limits.
	MaxEntries = 100_000
	MaxBytes   = 100 << 20

	// Capture modes name the evidence a HAR was built from.
	CaptureRetainedNetworkEntries = "retained_network_entries"
	CaptureReplayNetworkEntries   = "replay_network_entries"

	// Completeness: retained evidence is a bounded sample of what the browser
	// issued, so a synthetic HAR is at best partial; truncated adds that this
	// writer itself left retained entries out.
	CompletenessPartial   = "partial"
	CompletenessTruncated = "truncated"

	// Reasons an entry is left out, counted in the HAR's _apexload block.
	OmittedEntryLimit   = "entry_limit"
	OmittedByteLimit    = "byte_limit"
	OmittedInvalidURL   = "invalid_url"
	OmittedNoMethod     = "missing_method"
	OmittedNoStartTime  = "missing_start_time"
	OmittedCoveredByHAR = "native_har"
	OmittedUnscrubbable = "scrub_failed"

	// Where an entry's startedDateTime came from.
	StartedRequestStart = "request_start"
	StartedRecordTime   = "record_time"

	// Where an entry's time came from.
	TimeTimings       = "timings"
	TimeEntryDuration = "entry_duration"
	TimeUnavailable   = "unavailable"

	trailerReserve = 64 << 10
	maxHeaderPairs = 256
	maxNameBytes   = 1 << 10
	maxValueBytes  = 16 << 10
	maxURLBytes    = 64 << 10
	maxQueryPairs  = 256
)

// OmittedFields are the HAR fields a synthetic HAR never carries a measured
// value for: they are written empty (strings, arrays), -1 (optional sizes and
// timings), 0 (the required timings.send and content.size, also named in each
// entry's unavailable_fields) or left out (bodies).
var OmittedFields = []string{
	"request.httpVersion",
	"request.cookies",
	"request.postData",
	"response.httpVersion",
	"response.statusText",
	"response.cookies",
	"response.content.size",
	"response.content.text",
	"timings.blocked",
	"timings.send",
}

// Entry is one retained network observation.
type Entry struct {
	// RecordTime is when the observation was retained (its transaction or
	// replay event time); it stands in for the request start only when the
	// evidence has no measured start, and is labelled as such.
	RecordTime    time.Time
	ActionName    string
	PageURL       string
	BrowserEngine string
	Network       protocol.NetworkEntry
}

// Metadata describes where the entries came from.
type Metadata struct {
	CaptureMode string
	// CreatorVersion is the HAR's creator version; empty means the algorithm
	// version.
	CreatorVersion string
	// Policy is the scrub policy applied to every entry (scrub.HAR).
	Policy scrub.Policy
	// ExtraOmittedFields names fields this source never holds (for example
	// headers in a projection that keeps none), added to OmittedFields.
	ExtraOmittedFields []string
	// NativeHARCoverageUnknown counts native HARs in the same evidence that
	// do not say which contexts they cover, so their requests may appear in
	// both HARs.
	NativeHARCoverageUnknown int
}

// Summary describes a written HAR.
type Summary struct {
	Entries      int64
	Omitted      map[string]int64
	Completeness string
	Bytes        int64
	SHA256       string
}

var errClosed = errors.New("synthetic HAR writer is closed")

// Writer streams one HAR document. It is not safe for concurrent use.
type Writer struct {
	out      *bufio.Writer
	counter  *countingWriter
	digest   hash.Hash
	meta     Metadata
	entries  int64
	omitted  map[string]int64
	engines  map[string]bool
	started  bool
	closed   bool
	err      error
	maxBytes int64
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// NewWriter starts a HAR document on w.
func NewWriter(w io.Writer, meta Metadata) *Writer {
	digest := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(w, digest)}
	return &Writer{out: bufio.NewWriterSize(counter, 64<<10), counter: counter, digest: digest, meta: meta, omitted: map[string]int64{}, engines: map[string]bool{}, maxBytes: MaxBytes}
}

func (w *Writer) written() int64 { return w.counter.n + int64(w.out.Buffered()) }

func (w *Writer) begin() error {
	if w.started {
		return w.err
	}
	w.started = true
	// Without a named build the creator is the algorithm version, so the
	// same evidence writes the same bytes from any build.
	creatorVersion := w.meta.CreatorVersion
	if creatorVersion == "" {
		creatorVersion = strconv.Itoa(AlgorithmVersion)
	}
	head, _ := json.Marshal(map[string]any{"name": "APEX Load synthetic HAR", "version": creatorVersion, "comment": Algorithm + ".v" + strconv.Itoa(AlgorithmVersion)})
	_, w.err = w.out.WriteString(`{"log":{"version":"` + SchemaVersion + `","creator":` + string(head) + `,"entries":[`)
	return w.err
}

// Omit counts an entry the caller left out, by reason.
func (w *Writer) Omit(reason string) {
	w.omitted[reason]++
}

// Add writes one entry, or counts it omitted when it has no request to write
// or the HAR's limits are reached. It returns an error only when writing fails.
func (w *Writer) Add(e Entry) error {
	if w.closed {
		return errClosed
	}
	if err := w.begin(); err != nil {
		return err
	}
	if w.entries >= MaxEntries {
		w.omitted[OmittedEntryLimit]++
		return nil
	}
	entry, reason := buildEntry(e, w.meta.Policy)
	if reason != "" {
		w.omitted[reason]++
		return nil
	}
	raw, err := scrubEntry(entry, w.meta.Policy)
	if err != nil {
		w.omitted[OmittedUnscrubbable]++
		return nil //nolint:nilerr // an entry that cannot be scrubbed is counted omitted and never written
	}
	if w.written()+int64(len(raw))+1+trailerReserve > w.maxBytes {
		w.omitted[OmittedByteLimit]++
		return nil
	}
	if w.entries > 0 {
		if err := w.out.WriteByte(','); err != nil {
			w.err = err
			return err
		}
	}
	if _, err := w.out.Write(raw); err != nil {
		w.err = err
		return err
	}
	w.entries++
	if engine := cleanText(e.BrowserEngine, 32); engine != "" && len(w.engines) < 8 {
		w.engines[engine] = true
	}
	return nil
}

// Close writes the _apexload block and ends the document.
func (w *Writer) Close() (Summary, error) {
	if w.closed {
		return Summary{}, errClosed
	}
	if err := w.begin(); err != nil {
		return Summary{}, err
	}
	w.closed = true
	completeness := CompletenessPartial
	reasons := []string{"retained_observations_only"}
	if w.omitted[OmittedEntryLimit] > 0 || w.omitted[OmittedByteLimit] > 0 {
		completeness = CompletenessTruncated
		reasons = append(reasons, "writer_limit")
	}
	if w.meta.NativeHARCoverageUnknown > 0 {
		reasons = append(reasons, "native_har_coverage_unknown")
	}
	engines := make([]string, 0, len(w.engines))
	for engine := range w.engines {
		engines = append(engines, engine)
	}
	sort.Strings(engines)
	omitted := map[string]int64{}
	for reason, count := range w.omitted {
		omitted[reason] = count
	}
	block := map[string]any{
		"fidelity":             Fidelity,
		"algorithm":            Algorithm,
		"algorithm_version":    AlgorithmVersion,
		"schema_version":       SchemaVersion,
		"capture_mode":         w.meta.CaptureMode,
		"completeness":         completeness,
		"completeness_reasons": reasons,
		"entry_count":          w.entries,
		"omitted_entries":      omitted,
		"omitted_fields":       w.omittedFields(),
		"body_policy":          BodyPolicy,
		"browser_engines":      engines,
		"ordering":             "source_order",
	}
	if w.meta.NativeHARCoverageUnknown > 0 {
		block["native_har_coverage_unknown"] = w.meta.NativeHARCoverageUnknown
	}
	raw, _ := json.Marshal(block)
	if _, err := w.out.WriteString(`],"_apexloadSensitivity":"` + scrub.SensitivityScrubbed + `","_apexload":` + string(raw) + `}}`); err != nil {
		return Summary{}, err
	}
	if err := w.out.Flush(); err != nil {
		return Summary{}, err
	}
	return Summary{Entries: w.entries, Omitted: omitted, Completeness: completeness, Bytes: w.counter.n, SHA256: hex.EncodeToString(w.digest.Sum(nil))}, nil
}

func (w *Writer) omittedFields() []string {
	fields := append([]string(nil), OmittedFields...)
	for _, field := range w.meta.ExtraOmittedFields {
		if field = cleanText(field, 128); field != "" && !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}
	return fields
}

type harNameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harRequest struct {
	Method      string         `json:"method"`
	URL         string         `json:"url"`
	HTTPVersion string         `json:"httpVersion"`
	Cookies     []harNameValue `json:"cookies"`
	Headers     []harNameValue `json:"headers"`
	QueryString []harNameValue `json:"queryString"`
	HeadersSize int64          `json:"headersSize"`
	BodySize    int64          `json:"bodySize"`
}

type harContent struct {
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
}

type harResponse struct {
	Status      int            `json:"status"`
	StatusText  string         `json:"statusText"`
	HTTPVersion string         `json:"httpVersion"`
	Cookies     []harNameValue `json:"cookies"`
	Headers     []harNameValue `json:"headers"`
	Content     harContent     `json:"content"`
	RedirectURL string         `json:"redirectURL"`
	HeadersSize int64          `json:"headersSize"`
	BodySize    int64          `json:"bodySize"`
}

type harTimings struct {
	Blocked float64 `json:"blocked"`
	DNS     float64 `json:"dns"`
	Connect float64 `json:"connect"`
	SSL     float64 `json:"ssl"`
	Send    float64 `json:"send"`
	Wait    float64 `json:"wait"`
	Receive float64 `json:"receive"`
}

type harEntryEvidence struct {
	StartedSource string `json:"started_source"`
	TimeSource    string `json:"time_source"`
	RequestID     string `json:"request_id,omitempty"`
	ActionName    string `json:"action_name,omitempty"`
	PageURL       string `json:"page_url,omitempty"`
	ResourceType  string `json:"resource_type,omitempty"`
	FailureKind   string `json:"failure_kind,omitempty"`
	State         string `json:"state,omitempty"`
	TimingSource  string `json:"timing_source,omitempty"`
	// WaitIncludesSend: the evidence measures request start to response start
	// as one phase, written as wait with send unavailable.
	WaitIncludesSend bool `json:"wait_includes_send,omitempty"`
	// WaitIsEntryDuration: no phase was measured, so the entry's whole
	// duration is written as wait (time_source entry_duration).
	WaitIsEntryDuration bool `json:"wait_is_entry_duration,omitempty"`
	// UnavailableFields names required HAR fields that were not measured
	// and are written as 0 (HAR 1.2 allows -1 only for optional timings and
	// sizes).
	UnavailableFields []string `json:"unavailable_fields,omitempty"`
}

type harEntry struct {
	StartedDateTime string           `json:"startedDateTime"`
	Time            float64          `json:"time"`
	Request         harRequest       `json:"request"`
	Response        harResponse      `json:"response"`
	Cache           struct{}         `json:"cache"`
	Timings         harTimings       `json:"timings"`
	Evidence        harEntryEvidence `json:"_apexload"`
}

// scrubEntry runs one entry through the native HAR scrubber and drops the
// per-entry label it adds; the log carries one label for the document.
func scrubEntry(entry harEntry, policy scrub.Policy) ([]byte, error) {
	raw, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	scrubbed, err := scrub.HAR(raw, policy)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(scrubbed, &doc); err != nil {
		return nil, err
	}
	delete(doc, "_apexloadSensitivity")
	return json.Marshal(doc)
}

func buildEntry(e Entry, policy scrub.Policy) (harEntry, string) {
	n := e.Network
	rawURL := strings.TrimSpace(n.URL)
	parsed, err := url.Parse(rawURL)
	if rawURL == "" || len(rawURL) > maxURLBytes || !utf8.ValidString(rawURL) || err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return harEntry{}, OmittedInvalidURL
	}
	method := strings.TrimSpace(n.Method)
	if method == "" || len(method) > 32 || strings.ContainsAny(method, " \t\r\n") {
		return harEntry{}, OmittedNoMethod
	}
	evidence := n.BrowserEvidence
	var out harEntry
	switch {
	case evidence != nil && evidence.EpochStartMS != nil && validEpochMS(*evidence.EpochStartMS):
		// Whole milliseconds plus the fraction to the microsecond: float
		// nanoseconds would print representation noise as precision.
		whole := math.Floor(*evidence.EpochStartMS)
		fraction := time.Duration(math.Round((*evidence.EpochStartMS-whole)*1000)) * time.Microsecond
		out.StartedDateTime = time.UnixMilli(int64(whole)).Add(fraction).UTC().Format(time.RFC3339Nano)
		out.Evidence.StartedSource = StartedRequestStart
	case !e.RecordTime.IsZero():
		out.StartedDateTime = e.RecordTime.UTC().Format(time.RFC3339Nano)
		out.Evidence.StartedSource = StartedRecordTime
	default:
		return harEntry{}, OmittedNoStartTime
	}
	// Optional timings are -1 when unmeasured; send, wait and receive are
	// required non-negative, so an unmeasured one is 0 and named in
	// unavailable_fields.
	out.Timings = harTimings{Blocked: -1, DNS: -1, Connect: -1, SSL: -1}
	waitMeasured, receiveMeasured := false, false
	if evidence != nil {
		for _, phase := range evidence.Phases {
			duration, ok := phaseDuration(phase)
			if !ok {
				continue
			}
			switch phase.Name {
			case "dns":
				out.Timings.DNS = duration
			case "connect":
				out.Timings.Connect = duration
			case "tls":
				out.Timings.SSL = duration
			case "request_wait":
				out.Timings.Wait, waitMeasured = duration, true
				out.Evidence.WaitIncludesSend = true
			case "response":
				out.Timings.Receive, receiveMeasured = duration, true
			}
		}
		out.Evidence.RequestID = cleanText(evidence.RequestID, 128)
		out.Evidence.State = cleanText(evidence.State, 32)
		out.Evidence.TimingSource = cleanText(evidence.TimingSource, 64)
	}
	total, measured := 0.0, waitMeasured || receiveMeasured
	if waitMeasured {
		total += out.Timings.Wait
	}
	if receiveMeasured {
		total += out.Timings.Receive
	}
	// HAR includes ssl inside connect, so it is not added again.
	for _, value := range []float64{out.Timings.Blocked, out.Timings.DNS, out.Timings.Connect} {
		if value >= 0 {
			total += value
			measured = true
		}
	}
	switch {
	case measured:
		out.Time = roundMS(total)
		out.Evidence.TimeSource = TimeTimings
	case n.DurationMs > 0 && !math.IsInf(n.DurationMs, 0) && !math.IsNaN(n.DurationMs) && n.DurationMs <= 86_400_000:
		// time is the sum of the timings: the whole duration is one wait.
		out.Time = roundMS(n.DurationMs)
		out.Timings.Wait, waitMeasured = out.Time, true
		out.Evidence.TimeSource, out.Evidence.WaitIsEntryDuration = TimeEntryDuration, true
	default:
		out.Evidence.TimeSource = TimeUnavailable
	}
	out.Evidence.UnavailableFields = append(out.Evidence.UnavailableFields, "timings.send")
	if !waitMeasured {
		out.Evidence.UnavailableFields = append(out.Evidence.UnavailableFields, "timings.wait")
	}
	if !receiveMeasured {
		out.Evidence.UnavailableFields = append(out.Evidence.UnavailableFields, "timings.receive")
	}
	out.Request = harRequest{Method: method, URL: rawURL, HTTPVersion: "", Cookies: []harNameValue{}, Headers: headerPairs(n.RequestHeaders), QueryString: queryPairs(parsed.RawQuery), HeadersSize: -1, BodySize: -1}
	// The decoded content size is never measured; HAR 1.2 gives it no -1.
	out.Response = harResponse{Status: 0, StatusText: "", HTTPVersion: "", Cookies: []harNameValue{}, Headers: headerPairs(n.ResponseHeaders), Content: harContent{Size: 0, MimeType: "x-unknown"}, HeadersSize: -1, BodySize: -1}
	out.Evidence.UnavailableFields = append(out.Evidence.UnavailableFields, "response.content.size")
	if n.StatusCode > 0 && n.StatusCode <= 999 {
		out.Response.Status = n.StatusCode
	}
	if value := headerValue(n.ResponseHeaders, "content-type"); value != "" {
		out.Response.Content.MimeType = value
	}
	if value := headerValue(n.ResponseHeaders, "location"); value != "" {
		out.Response.RedirectURL = value
	}
	if evidence != nil {
		bytes := evidence.Bytes
		if bytes.RequestHeaders != nil && *bytes.RequestHeaders >= 0 {
			out.Request.HeadersSize = *bytes.RequestHeaders
		}
		if bytes.RequestBody != nil && *bytes.RequestBody >= 0 {
			out.Request.BodySize = *bytes.RequestBody
		}
		if bytes.ResponseHeaders != nil && *bytes.ResponseHeaders >= 0 {
			out.Response.HeadersSize = *bytes.ResponseHeaders
		}
		if bytes.ResponseEncodedBody != nil && *bytes.ResponseEncodedBody >= 0 {
			out.Response.BodySize = *bytes.ResponseEncodedBody
		}
	}
	out.Evidence.ActionName = cleanText(scrub.ValueLabel(e.ActionName, policy), 256)
	out.Evidence.PageURL = cleanText(scrub.ValueURL(e.PageURL, policy), 2048)
	out.Evidence.ResourceType = cleanText(n.ResourceType, 64)
	out.Evidence.FailureKind = cleanText(n.FailureKind, 32)
	return out, ""
}

func validEpochMS(ms float64) bool {
	// 2000-01-01 to 2200-01-01: a start outside that range is not a wall time.
	return !math.IsNaN(ms) && !math.IsInf(ms, 0) && ms >= 946_684_800_000 && ms <= 7_258_118_400_000
}

func phaseDuration(phase protocol.BrowserResourcePhase) (float64, bool) {
	if phase.StartMS == nil || phase.EndMS == nil {
		return 0, false
	}
	start, end := *phase.StartMS, *phase.EndMS
	if math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end < start || end-start > 86_400_000 {
		return 0, false
	}
	return roundMS(end - start), true
}

func roundMS(v float64) float64 { return math.Round(v*1000) / 1000 }

func cleanText(s string, limit int) string {
	if len(s) > limit || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\r\n") {
		return ""
	}
	return s
}

func headerPairs(headers map[string]string) []harNameValue {
	pairs := make([]harNameValue, 0, len(headers))
	for name, value := range headers {
		if len(pairs) >= maxHeaderPairs {
			break
		}
		name, value = cleanText(name, maxNameBytes), cleanText(value, maxValueBytes)
		if name == "" {
			continue
		}
		pairs = append(pairs, harNameValue{Name: name, Value: value})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Name != pairs[j].Name {
			return pairs[i].Name < pairs[j].Name
		}
		return pairs[i].Value < pairs[j].Value
	})
	return pairs
}

// headerValue returns the named header; of differently cased duplicates, the
// first in byte order wins so the output is stable.
func headerValue(headers map[string]string, name string) string {
	found, value := "", ""
	for key, candidate := range headers {
		if strings.EqualFold(key, name) && (found == "" || key < found) {
			found, value = key, candidate
		}
	}
	return cleanText(value, maxValueBytes)
}

// queryPairs keeps the URL's query parameters in their written order.
func queryPairs(rawQuery string) []harNameValue {
	pairs := []harNameValue{}
	for _, part := range strings.Split(rawQuery, "&") {
		if part == "" || len(pairs) >= maxQueryPairs {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = decoded
		}
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		name, value = cleanText(name, maxNameBytes), cleanText(value, maxValueBytes)
		if name == "" {
			continue
		}
		pairs = append(pairs, harNameValue{Name: name, Value: value})
	}
	return pairs
}
