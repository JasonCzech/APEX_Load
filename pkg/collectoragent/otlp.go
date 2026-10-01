package collectoragent

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/credential"
)

// OTLPReceiver accepts bounded OTLP/HTTP JSON metrics. It is passive: preparing
// a run never activates source polling or sends a health probe.
type OTLPReceiver struct {
	requireJournal bool
	journalReady   bool
	journalPath    string
	admission      chan struct{}
	mu             sync.Mutex
	tokens         map[string]string
	points         map[string][]analysis.MetricPoint
}

func NewOTLPReceiver(tokens map[string]string) (*OTLPReceiver, error) {
	if len(tokens) == 0 || len(tokens) > 16 {
		return nil, errors.New("OTLP requires 1-16 approved streams")
	}
	copyTokens := make(map[string]string, len(tokens))
	for stream, token := range tokens {
		if len(stream) > 128 || len(token) < 32 {
			return nil, errors.New("OTLP stream tokens require at least 32 bytes")
		}
		copyTokens[stream] = token
	}
	return &OTLPReceiver{tokens: copyTokens, points: map[string][]analysis.MetricPoint{}, admission: make(chan struct{}, 4)}, nil
}

type otlpAttribute struct {
	Key   string `json:"key"`
	Value struct {
		StringValue *string `json:"stringValue"`
	} `json:"value"`
}
type otlpPoint struct {
	Time       json.Number     `json:"timeUnixNano"`
	Double     *float64        `json:"asDouble"`
	Integer    json.Number     `json:"asInt"`
	Attributes []otlpAttribute `json:"attributes"`
	Flags      uint32          `json:"flags"`
}
type otlpMetricData struct {
	Points      []otlpPoint `json:"dataPoints"`
	Monotonic   bool        `json:"isMonotonic"`
	Temporality int         `json:"aggregationTemporality"`
}
type otlpRequest struct {
	Resources []struct {
		Resource struct {
			Attributes []otlpAttribute `json:"attributes"`
		} `json:"resource"`
		Scopes []struct {
			Metrics []struct {
				Name                 string          `json:"name"`
				Unit                 string          `json:"unit"`
				Gauge                *otlpMetricData `json:"gauge"`
				Sum                  *otlpMetricData `json:"sum"`
				Histogram            json.RawMessage `json:"histogram"`
				Summary              json.RawMessage `json:"summary"`
				ExponentialHistogram json.RawMessage `json:"exponentialHistogram"`
			} `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}

func (o *OTLPReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.URL.Path != "/v1/metrics" {
		http.NotFound(w, r)
		return
	}
	stream := r.Header.Get("X-APEX-Monitor-Stream")
	token := o.tokens[stream]
	if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	select {
	case o.admission <- struct{}{}:
		defer func() { <-o.admission }()
	default:
		http.Error(w, "OTLP receiver busy", 429)
		return
	}
	if r.Header.Get("Content-Encoding") != "" || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "use uncompressed OTLP JSON", 415)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body otlpRequest
	decoder := json.NewDecoder(r.Body)
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid bounded OTLP request", 400)
		return
	}
	points := make([]analysis.MetricPoint, 0)
	rejected := 0
	now := time.Now().UTC()
	for _, resource := range body.Resources {
		for _, scope := range resource.Scopes {
			for _, metric := range scope.Metrics {
				data := metric.Gauge
				kind := analysis.KindGauge
				if metric.Sum != nil {
					data = metric.Sum
					if !data.Monotonic || data.Temporality != 2 {
						rejected += len(data.Points)
						continue
					}
					kind = analysis.KindCounter
				}
				if data == nil {
					rejected += unsupportedOTLPPointCount(metric.Histogram, metric.Summary, metric.ExponentialHistogram)
					continue
				}
				for _, raw := range data.Points {
					if len(points) >= 512 {
						http.Error(w, "OTLP sample limit exceeded", 413)
						return
					}
					ns, err := strconv.ParseInt(string(raw.Time), 10, 64)
					if err != nil || ns <= 0 {
						rejected++
						continue
					}
					at := time.Unix(0, ns).UTC()
					if at.Before(now.Add(-5*time.Minute)) || at.After(now.Add(30*time.Second)) {
						rejected++
						continue
					}
					value := raw.Double
					if value == nil && raw.Integer != "" {
						integer, e := strconv.ParseInt(string(raw.Integer), 10, 64)
						if e == nil && integer >= -(1<<53) && integer <= 1<<53 {
							v := float64(integer)
							value = &v
						}
					}
					if raw.Flags&1 == 0 && (value == nil || math.IsNaN(*value) || math.IsInf(*value, 0)) {
						rejected++
						continue
					}
					dimensions := map[string]string{}
					invalidAttributes := false
					for _, attr := range append(resource.Resource.Attributes, raw.Attributes...) {
						if attr.Value.StringValue == nil {
							invalidAttributes = true
							break
						}
						dimensions[attr.Key] = *attr.Value.StringValue
					}
					quality := analysis.QualityValid
					if raw.Flags&1 != 0 {
						quality = analysis.QualityMissing
						value = nil
					}
					point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "validation", Source: stream, TimestampUTC: at, MetricName: metric.Name, Value: value, Unit: metric.Unit, Kind: kind, Dimensions: dimensions, Quality: quality}
					if invalidAttributes || normalizeAdapterPoint(&point) != nil || point.Validate() != nil || sensitiveDimensions(dimensions) {
						rejected++
						continue
					}
					duplicate := false
					for _, accepted := range points {
						if sameOTLPIdentity(accepted, point) {
							duplicate = true
							break
						}
					}
					if duplicate {
						rejected++
						continue
					}
					points = append(points, point)
				}
			}
		}
	}
	o.mu.Lock()
	if o.requireJournal && !o.journalReady {
		o.mu.Unlock()
		http.Error(w, "OTLP journal unavailable", 503)
		return
	}
	existing := o.points[stream]
	filtered := existing[:0]
	for _, p := range existing {
		if p.TimestampUTC.After(now.Add(-5 * time.Minute)) {
			filtered = append(filtered, p)
		}
	}
	deduplicated := points[:0]
	for _, point := range points {
		duplicate := false
		for _, accepted := range filtered {
			if sameOTLPIdentity(accepted, point) {
				duplicate = true
				if !sameOTLPValue(accepted, point) {
					rejected++
				}
				break
			}
		}
		if !duplicate {
			deduplicated = append(deduplicated, point)
		}
	}
	points = deduplicated
	if len(filtered)+len(points) > 512 {
		o.mu.Unlock()
		http.Error(w, "OTLP stream buffer full", 429)
		return
	}
	o.points[stream] = append(filtered, points...)
	if err := o.persistJournal(); err != nil {
		o.points[stream] = filtered
		o.mu.Unlock()
		http.Error(w, "OTLP durable acceptance failed", 503)
		return
	}
	o.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"partialSuccess": map[string]any{"rejectedDataPoints": strconv.Itoa(rejected), "errorMessage": otlpRejectionMessage(rejected)}})
}
func otlpRejectionMessage(n int) string {
	if n == 0 {
		return ""
	}
	return "unsupported semantics, timestamp, or dimensions; only gauges and cumulative monotonic sums accepted"
}
func (o *OTLPReceiver) Snapshot(job RunJob) []analysis.MetricPoint {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now().UTC()
	var points []analysis.MetricPoint
	for _, point := range o.points[job.Destination] {
		if !point.TimestampUTC.Before(job.StartedAt) && point.TimestampUTC.After(now.Add(-5*time.Minute)) {
			point.ProjectID = ""
			duplicate := false
			for _, accepted := range points {
				if sameOTLPIdentity(accepted, point) {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			points = append(points, point)
		}
	}
	return points
}

func sensitiveDimensions(dimensions map[string]string) bool {
	for key := range dimensions {
		if credential.SensitiveName(key) {
			return true
		}
	}
	return false
}

func sameOTLPIdentity(a, b analysis.MetricPoint) bool {
	return a.MetricName == b.MetricName && a.TimestampUTC.Equal(b.TimestampUTC) && maps.Equal(a.Dimensions, b.Dimensions)
}
func sameOTLPValue(a, b analysis.MetricPoint) bool {
	return a.Unit == b.Unit && a.Kind == b.Kind && a.Quality == b.Quality && (a.Value == nil && b.Value == nil || a.Value != nil && b.Value != nil && *a.Value == *b.Value)
}

// Identity excludes run-specific metadata so retries match before and after binding.
func otlpPointIdentity(point analysis.MetricPoint) string {
	encoded, _ := json.Marshal(struct {
		Name       string
		Timestamp  time.Time
		Dimensions map[string]string
	}{point.MetricName, point.TimestampUTC, point.Dimensions})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func (a *Agent) acknowledgeOTLP(job RunJob, points []analysis.MetricPoint) {
	if job.Pack != "otel-metrics" && job.Pack != "cloudwatch" && job.Pack != "azure-monitor" {
		return
	}
	if a.otlpAccepted == nil {
		a.otlpAccepted = map[string]map[string]time.Time{}
	}
	if a.otlpAccepted[job.ID] == nil {
		a.otlpAccepted[job.ID] = map[string]time.Time{}
	}
	for _, point := range points {
		if (job.Pack == "cloudwatch" || job.Pack == "azure-monitor") && (point.Value == nil || point.Quality == analysis.QualityMissing) {
			continue
		}
		a.otlpAccepted[job.ID][otlpPointIdentity(point)] = point.TimestampUTC
	}
	retention := 5 * time.Minute
	if job.Pack == "cloudwatch" || job.Pack == "azure-monitor" {
		retention = 7 * time.Minute
	}
	for key, at := range a.otlpAccepted[job.ID] {
		if at.Before(time.Now().Add(-retention)) {
			delete(a.otlpAccepted[job.ID], key)
		}
	}
}

func unsupportedOTLPPointCount(payloads ...json.RawMessage) int {
	count := 0
	for _, payload := range payloads {
		var data struct {
			Points []json.RawMessage `json:"dataPoints"`
		}
		if len(payload) > 0 && json.Unmarshal(payload, &data) == nil {
			count += len(data.Points)
		}
	}
	return max(1, count)
}
