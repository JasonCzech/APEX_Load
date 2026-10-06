package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// maxRCANarrativeBytes bounds a narrative to check.
const maxRCANarrativeBytes = 32 << 10

// Narration issue kinds.
const (
	narrationUnknownReference = "unknown_reference"
	narrationUnsupportedNumber = "unsupported_number"
	narrationUnsupportedTime   = "unsupported_time"
)

// RCANarrationCheck says whether a narrative about an analysis stays within
// its bundle: every reference ID it cites must be in the bundle, and every
// number and time must match the bundle's within the rounding the narrative
// shows. A narrative that fails is rejected; nothing is stored.
type RCANarrationCheck struct {
	OK         bool                `json:"ok"`
	References []string            `json:"references"`
	Numbers    int                 `json:"numbers"`
	Issues     []RCANarrationIssue `json:"issues"`
	// BundleTruncated says the bundle was cut to fit, so a reference to a
	// part it left out is reported as unknown.
	BundleTruncated bool `json:"bundle_truncated"`
}

type RCANarrationIssue struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Offset is the issue's byte offset in the narrative.
	Offset int    `json:"offset"`
	Detail string `json:"detail"`
}

var (
	narrationReferencePattern = regexp.MustCompile(`\b(?:anomaly|hypothesis|evidence)-[0-9a-f]{16}\b|\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	narrationTimestampPattern = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?)?(?:Z|\s?UTC)?`)
	narrationClockPattern     = regexp.MustCompile(`\b\d{1,2}:\d{2}(?::\d{2})?\b`)
	narrationNumberPattern    = regexp.MustCompile(`[-+−]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?`)
)

// checkRCANarration checks a narrative against a bundle.
func checkRCANarration(bundle RCABundle, narrative string) RCANarrationCheck {
	out := RCANarrationCheck{References: []string{}, Issues: []RCANarrationIssue{}, BundleTruncated: bundle.Truncated}
	ids, numbers, times := rcaBundleFacts(bundle)
	text := []byte(narrative)
	mask := func(start, end int) {
		for i := start; i < end; i++ {
			text[i] = ' '
		}
	}
	seen := map[string]bool{}
	for _, m := range narrationReferencePattern.FindAllIndex(text, -1) {
		id := narrative[m[0]:m[1]]
		if !seen[id] {
			seen[id] = true
			out.References = append(out.References, id)
		}
		if !ids[id] {
			out.Issues = append(out.Issues, RCANarrationIssue{Kind: narrationUnknownReference, Text: id, Offset: m[0], Detail: "the bundle has no reference with this ID"})
		}
		mask(m[0], m[1])
	}
	for _, m := range narrationTimestampPattern.FindAllIndex(text, -1) {
		if !narrationTimestampSupported(strings.TrimSpace(narrative[m[0]:m[1]]), times) {
			out.Issues = append(out.Issues, RCANarrationIssue{Kind: narrationUnsupportedTime, Text: narrative[m[0]:m[1]], Offset: m[0], Detail: "no time in the bundle matches at this precision"})
		}
		mask(m[0], m[1])
	}
	for _, m := range narrationClockPattern.FindAllIndex(text, -1) {
		if !narrationClockSupported(narrative[m[0]:m[1]], times) {
			out.Issues = append(out.Issues, RCANarrationIssue{Kind: narrationUnsupportedTime, Text: narrative[m[0]:m[1]], Offset: m[0], Detail: "no time in the bundle has this UTC time of day"})
		}
		mask(m[0], m[1])
	}
	for _, m := range narrationNumberPattern.FindAllIndex(text, -1) {
		// A number joined to a word ("p95", "v2", "3xx") names something;
		// one followed by its unit ("300ms") is a claim.
		if m[0] > 0 && isNarrationWordByte(text[m[0]-1]) || m[1] < len(text) && isNarrationWordByte(text[m[1]]) && !narrationUnits[narrationWordAt(text, m[1])] {
			continue
		}
		raw := narrative[m[0]:m[1]]
		value, decimals, ok := parseNarrationNumber(raw)
		if !ok {
			continue
		}
		out.Numbers++
		if !narrationNumberSupported(value, decimals, numbers) {
			out.Issues = append(out.Issues, RCANarrationIssue{Kind: narrationUnsupportedNumber, Text: raw, Offset: m[0], Detail: "no number in the bundle matches within the rounding shown"})
		}
	}
	out.OK = len(out.Issues) == 0
	return out
}

func isNarrationWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_'
}

// narrationUnits are the units a number may be written against.
var narrationUnits = map[string]bool{
	"ms": true, "s": true, "sec": true, "secs": true, "min": true, "mins": true, "h": true, "hr": true, "hrs": true,
	"pts": true, "rps": true, "tps": true, "kb": true, "mb": true, "gb": true, "kib": true, "mib": true, "gib": true, "x": true,
}

// narrationWordAt is the lower-cased run of word bytes starting at i.
func narrationWordAt(text []byte, i int) string {
	end := i
	for end < len(text) && isNarrationWordByte(text[end]) {
		end++
	}
	return strings.ToLower(string(text[i:end]))
}

// parseNarrationNumber reads a number and how many decimals it shows.
func parseNarrationNumber(raw string) (float64, int, bool) {
	clean := strings.ReplaceAll(strings.ReplaceAll(raw, ",", ""), "−", "-")
	value, err := strconv.ParseFloat(clean, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, 0, false
	}
	decimals := 0
	if dot := strings.IndexByte(clean, '.'); dot >= 0 {
		decimals = len(clean) - dot - 1
	}
	return value, decimals, true
}

// narrationNumberSupported accepts a number that some bundle number, in its
// own unit or a common conversion (ns to ms, s or min, ms to s, fraction to
// percent), rounds to. Integers up to ten count things and pass.
func narrationNumberSupported(value float64, decimals int, numbers []float64) bool {
	if decimals == 0 && value >= 0 && value <= 10 {
		return true
	}
	tolerance := 0.5*math.Pow(10, -float64(decimals)) + 1e-9
	magnitude := math.Abs(value)
	for _, x := range numbers {
		for _, candidate := range [...]float64{x, x / 1e6, x / 1e9, x / 60e9, x / 1000, x * 100} {
			if math.Abs(math.Abs(candidate)-magnitude) <= tolerance {
				return true
			}
		}
	}
	return false
}

func narrationTimestampSupported(raw string, times []time.Time) bool {
	raw = strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(raw, "UTC")), "Z")
	raw = strings.Replace(raw, " ", "T", 1)
	// A time is compared at the precision it is written to.
	layouts := []struct {
		layout    string
		precision time.Duration
	}{
		{"2006-01-02T15:04:05", time.Second},
		{"2006-01-02T15:04", time.Minute},
		{"2006-01-02", 24 * time.Hour},
	}
	if dot := strings.IndexByte(raw, '.'); dot >= 0 {
		precision := time.Duration(math.Pow(10, float64(9-min(9, len(raw)-dot-1))))
		layouts = layouts[:0]
		layouts = append(layouts, struct {
			layout    string
			precision time.Duration
		}{"2006-01-02T15:04:05.999999999", precision})
	}
	for _, l := range layouts {
		at, err := time.ParseInLocation(l.layout, raw, time.UTC)
		if err != nil {
			continue
		}
		for _, t := range times {
			if t.UTC().Truncate(l.precision).Equal(at.Truncate(l.precision)) {
				return true
			}
		}
		return false
	}
	return false
}

func narrationClockSupported(raw string, times []time.Time) bool {
	parts := strings.Split(raw, ":")
	values := make([]int, len(parts))
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		values[i] = v
	}
	for _, t := range times {
		t = t.UTC()
		if t.Hour() == values[0] && t.Minute() == values[1] && (len(values) < 3 || t.Second() == values[2]) {
			return true
		}
	}
	return false
}

// rcaBundleFacts lists a bundle's reference IDs, numbers and times.
func rcaBundleFacts(bundle RCABundle) (map[string]bool, []float64, []time.Time) {
	ids := map[string]bool{bundle.AnalysisID: true}
	a := bundle.Assessment
	for _, anomaly := range a.Anomalies {
		ids[anomaly.ID] = true
		for _, ref := range anomaly.Evidence {
			ids[ref.ID] = true
		}
	}
	for _, h := range a.Hypotheses {
		ids[h.ID] = true
		for _, ref := range h.Supporting {
			ids[ref.ID] = true
		}
		for _, ref := range h.Contradicting {
			ids[ref.ID] = true
		}
	}
	for _, e := range bundle.Excerpts {
		ids[e.RefID] = true
	}
	delete(ids, "")
	var numbers []float64
	var times []time.Time
	body, _ := json.Marshal(bundle)
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var tree any
	if decoder.Decode(&tree) == nil {
		var walk func(any)
		walk = func(node any) {
			switch v := node.(type) {
			case map[string]any:
				for _, child := range v {
					walk(child)
				}
			case []any:
				for _, child := range v {
					walk(child)
				}
			case json.Number:
				if f, err := v.Float64(); err == nil {
					numbers = append(numbers, f)
				}
			case string:
				if len(v) >= 20 && len(v) <= 40 {
					if at, err := time.Parse(time.RFC3339Nano, v); err == nil {
						times = append(times, at)
					}
				}
			}
		}
		walk(tree)
	}
	return ids, numbers, times
}

type rcaNarrationRequest struct {
	Narrative string `json:"narrative"`
}

// handleCheckRCANarration checks a narrative against a completed analysis's
// bundle.
func (s *Server) handleCheckRCANarration(w http.ResponseWriter, r *http.Request) {
	bundle, _, ok := s.rcaBundleForRequest(w, r)
	if !ok {
		return
	}
	var request rcaNarrationRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxRCANarrativeBytes+1024)
	if !decodeJSONBody(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Narrative) == "" || len(request.Narrative) > maxRCANarrativeBytes {
		writeError(w, http.StatusBadRequest, "narrative must be 1 to 32 KiB")
		return
	}
	writeJSON(w, http.StatusOK, checkRCANarration(bundle, request.Narrative))
}

// Critique authors.
const (
	rcaCritiqueByAgent   = "agent"
	rcaCritiqueByAnalyst = "analyst"
)

type rcaCritiqueRequest struct {
	Narrative  string `json:"narrative"`
	AuthorKind string `json:"author_kind,omitempty"`
	Model      string `json:"model,omitempty"`
}

// RCACritique is a review of an analysis: a narrative that passed the
// narration check against the analysis's bundle, stored apart from it.
type RCACritique struct {
	ID         string            `json:"id"`
	AnalysisID string            `json:"analysis_id"`
	Revision   int64             `json:"revision"`
	CreatedAt  time.Time         `json:"created_at"`
	CreatedBy  string            `json:"created_by,omitempty"`
	AuthorKind string            `json:"author_kind"`
	Model      string            `json:"model,omitempty"`
	Narrative  string            `json:"narrative"`
	Check      RCANarrationCheck `json:"check"`
}

// rcaCritiquePayload is what a stored critique holds.
type rcaCritiquePayload struct {
	Revision   int64             `json:"revision"`
	AuthorKind string            `json:"author_kind"`
	Model      string            `json:"model,omitempty"`
	Narrative  string            `json:"narrative"`
	Check      RCANarrationCheck `json:"check"`
}

func rcaCritiqueView(c storage.RCACritique) (RCACritique, bool) {
	var payload rcaCritiquePayload
	if json.Unmarshal(c.Payload, &payload) != nil {
		return RCACritique{}, false
	}
	return RCACritique{ID: c.ID, AnalysisID: c.AnalysisID, Revision: payload.Revision, CreatedAt: c.CreatedAt, CreatedBy: c.CreatedBy,
		AuthorKind: payload.AuthorKind, Model: payload.Model, Narrative: payload.Narrative, Check: payload.Check}, true
}

// handleCreateRCACritique stores a critique of a completed analysis. Its
// narrative must pass the narration check; the analysis is not changed.
func (s *Server) handleCreateRCACritique(w http.ResponseWriter, r *http.Request) {
	bundle, analysis, ok := s.rcaBundleForRequest(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(storage.RCACritiqueStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "root-cause critiques unavailable")
		return
	}
	var request rcaCritiqueRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxRCANarrativeBytes+1024)
	if !decodeJSONBody(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Narrative) == "" || len(request.Narrative) > maxRCANarrativeBytes {
		writeError(w, http.StatusBadRequest, "narrative must be 1 to 32 KiB")
		return
	}
	if request.AuthorKind == "" {
		request.AuthorKind = rcaCritiqueByAgent
	}
	if request.AuthorKind != rcaCritiqueByAgent && request.AuthorKind != rcaCritiqueByAnalyst {
		writeError(w, http.StatusBadRequest, "author_kind must be agent or analyst")
		return
	}
	if request.Model != "" && !plainText(request.Model, 128) {
		writeError(w, http.StatusBadRequest, "model must be plain text of at most 128 characters")
		return
	}
	check := checkRCANarration(bundle, request.Narrative)
	if !check.OK {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the narrative is not supported by the analysis's bundle", "check": check})
		return
	}
	payload, _ := json.Marshal(rcaCritiquePayload{Revision: analysis.Revision, AuthorKind: request.AuthorKind, Model: request.Model, Narrative: request.Narrative, Check: check})
	_, actor, _ := s.resolveAuditActor(r)
	saved, err := store.CreateRCACritique(storage.RCACritique{ID: uuid.NewString(), ProjectID: analysis.ProjectID, TestID: analysis.TestID, AnalysisID: analysis.ID, CreatedAt: time.Now(), CreatedBy: actor, Payload: payload})
	if errors.Is(err, storage.ErrRCACritiqueLimit) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to store the critique")
		return
	}
	s.recordAuditEvent(r, "rca.critique.create", "success", "rca_analysis", analysis.ID, analysis.ProjectID, map[string]any{"test_id": analysis.TestID, "critique_id": saved.ID, "author_kind": request.AuthorKind})
	view, _ := rcaCritiqueView(*saved)
	writeJSON(w, http.StatusCreated, view)
}

// handleListRCACritiques lists an analysis's critiques, oldest first.
func (s *Server) handleListRCACritiques(w http.ResponseWriter, r *http.Request) {
	analysis, ok := s.rcaAnalysisForRequest(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(storage.RCACritiqueStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "root-cause critiques unavailable")
		return
	}
	list, err := store.ListRCACritiques(analysis.ProjectID, analysis.TestID, analysis.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to read critiques")
		return
	}
	out := []RCACritique{}
	for _, c := range list {
		if view, ok := rcaCritiqueView(c); ok {
			out = append(out, view)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"critiques": out})
}

// rcaNarrationAccess bounds a narrative-carrying request's body; a viewer's
// check is a read for service accounts.
func rcaNarrationAccess(base accessRequirement) accessRequirement {
	base.MaxBodyBytes = maxRCANarrativeBytes + 8<<10
	if len(base.ProjectRoles) > 0 && base.ProjectRoles[0] == RoleViewer {
		base.ServiceAccountScope = "results:read"
	}
	return base
}
