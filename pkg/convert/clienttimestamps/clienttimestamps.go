// Package clienttimestamps finds epoch timestamps a recorded client made up
// itself, such as cache-busters and poll times, and gives each replayed
// request a fresh value instead of the recorded literal, as VuGen's
// web_save_timestamp_param does for recorded scripts.
//
// A 10-digit (seconds) or 13-digit (milliseconds) number in a request is a
// client timestamp when it falls within an hour of the capture window, no
// recorded response contains it (otherwise it is correlation territory), and
// either its key names a time or cache-buster (_, t, ts, time, timestamp,
// nocache, cb, _dc, ...) or the same key carries a different value every time
// it occurs. Other numbers, such as order or account numbers that happen to
// look like a recent epoch, are left as recorded.
package clienttimestamps

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

const (
	// Millis and Seconds are the template functions that replace recorded
	// millisecond and second timestamps.
	Millis  = "{{fn.timestamp.unix_ms}}"
	Seconds = "{{fn.timestamp.unix}}"
	// slack widens the capture window: clients often send a timestamp taken
	// slightly before or after the recorded request.
	slack = time.Hour
)

var (
	digitRun   = regexp.MustCompile(`[0-9]+`)
	keyPattern = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.-]{0,63})\\*"?\s*[:=]\s*\\*"?$`)
)

// timeKeys are keys that name a timestamp or a cache-buster.
var timeKeys = map[string]bool{
	"_": true, "t": true, "ts": true, "time": true, "timestamp": true, "nocache": true, "no_cache": true,
	"cb": true, "cachebuster": true, "cache_buster": true, "_dc": true, "dc": true, "now": true,
	"rnd": true, "rand": true, "_t": true, "_ts": true, "tstamp": true, "epoch": true,
}

// Evidence is what the recorded responses say about the capture.
type Evidence struct {
	// Server holds every 10- and 13-digit number a recorded response contains.
	Server map[string]bool
	// From and To bound the capture.
	From, To time.Time
}

// Report summarizes what Apply changed.
type Report struct {
	Replaced     int
	Keys         []string
	LeftAsRecord int
}

// Warning describes the change for the import summary, or returns "".
func (r Report) Warning() string {
	if r.Replaced == 0 {
		return ""
	}
	return fmt.Sprintf("Recorded client timestamps were replaced with fresh values at replay (%d occurrence(s), keys %s): no response contained them and they fall within the capture window.", r.Replaced, strings.Join(r.Keys, ", "))
}

// FromHAR collects the evidence from a HAR: every response body (base64
// decoded), header and redirect URL, and the entries' start times.
func FromHAR(har []byte) (Evidence, error) {
	var parsed struct {
		Log struct {
			Entries []struct {
				StartedDateTime string `json:"startedDateTime"`
				Response        struct {
					Headers []struct {
						Value string `json:"value"`
					} `json:"headers"`
					RedirectURL string `json:"redirectURL"`
					Content     struct {
						Text     string `json:"text"`
						Encoding string `json:"encoding"`
					} `json:"content"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(har, &parsed); err != nil {
		return Evidence{}, err
	}
	evidence := Evidence{Server: map[string]bool{}}
	for _, entry := range parsed.Log.Entries {
		if started, err := time.Parse(time.RFC3339Nano, entry.StartedDateTime); err == nil {
			evidence.Observe(started)
		}
		text := entry.Response.Content.Text
		if strings.EqualFold(entry.Response.Content.Encoding, "base64") {
			if decoded, err := base64.StdEncoding.DecodeString(text); err == nil {
				text = string(decoded)
			}
		}
		evidence.AddServerText(text)
		evidence.AddServerText(entry.Response.RedirectURL)
		for _, header := range entry.Response.Headers {
			evidence.AddServerText(header.Value)
		}
	}
	return evidence, nil
}

// Observe widens the capture window to include t.
func (e *Evidence) Observe(t time.Time) {
	if t.IsZero() {
		return
	}
	if e.From.IsZero() || t.Before(e.From) {
		e.From = t
	}
	if e.To.IsZero() || t.After(e.To) {
		e.To = t
	}
}

// AddServerText records the timestamp-shaped numbers a response carries.
func (e *Evidence) AddServerText(text string) {
	if e.Server == nil {
		e.Server = map[string]bool{}
	}
	for _, run := range digitRun.FindAllString(text, -1) {
		if len(run) == 10 || len(run) == 13 {
			e.Server[run] = true
		}
	}
}

// templateFor returns the template for a recorded number, or "" when it is
// not an epoch timestamp within the capture window.
func (e Evidence) templateFor(number string) string {
	value, err := strconv.ParseInt(number, 10, 64)
	if err != nil || e.From.IsZero() {
		return ""
	}
	var at time.Time
	template := ""
	switch len(number) {
	case 13:
		at, template = time.UnixMilli(value), Millis
	case 10:
		at, template = time.Unix(value, 0), Seconds
	default:
		return ""
	}
	if at.Before(e.From.Add(-slack)) || at.After(e.To.Add(slack)) {
		return ""
	}
	return template
}

type occurrence struct {
	number, key string
}

// Apply replaces client timestamps in request targets and text bodies
// (multipart text values included). Headers are left as recorded.
func Apply(script *dsl.Script, evidence Evidence) Report {
	report := Report{}
	if script == nil || evidence.From.IsZero() {
		return report
	}
	var found []occurrence
	scan := func(text string) {
		for _, span := range digitRun.FindAllStringIndex(text, -1) {
			number := text[span[0]:span[1]]
			if evidence.Server[number] || evidence.templateFor(number) == "" {
				continue
			}
			found = append(found, occurrence{number: number, key: keyBefore(text[:span[0]])})
		}
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if action := ref.Action; action != nil && safeBody(action) {
			scan(action.Target)
			scan(action.Body)
			for _, value := range action.BodyMultipart.MultipartTextValues() {
				scan(*value)
			}
		} else if action != nil {
			scan(action.Target)
		} else if ref.Request != nil {
			target, _ := ref.Request["target"].(string)
			scan(target)
			if safeRequestBody(ref.Request) {
				body, _ := ref.Request["body"].(string)
				scan(body)
			}
		}
		return nil
	})
	if len(found) == 0 {
		return report
	}
	// A key qualifies by name, or by carrying a new value every time.
	values := map[string]map[string]int{}
	for _, occ := range found {
		if values[occ.key] == nil {
			values[occ.key] = map[string]int{}
		}
		values[occ.key][occ.number]++
	}
	qualifies := map[string]bool{}
	for key, numbers := range values {
		if key == "" {
			continue
		}
		fresh := len(numbers) >= 2
		for _, count := range numbers {
			fresh = fresh && count == 1
		}
		qualifies[key] = timeKeys[strings.ToLower(key)] || fresh
	}
	replace := func(text string) string {
		var out strings.Builder
		last := 0
		for _, span := range digitRun.FindAllStringIndex(text, -1) {
			number := text[span[0]:span[1]]
			if evidence.Server[number] || !qualifies[keyBefore(text[:span[0]])] {
				continue
			}
			template := evidence.templateFor(number)
			if template == "" {
				continue
			}
			out.WriteString(text[last:span[0]])
			out.WriteString(template)
			last = span[1]
			report.Replaced++
		}
		if last == 0 {
			return text
		}
		out.WriteString(text[last:])
		return out.String()
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		action := ref.Action
		if action == nil {
			if ref.Request != nil {
				if target, ok := ref.Request["target"].(string); ok {
					ref.Request["target"] = replace(target)
				}
				if body, ok := ref.Request["body"].(string); ok && safeRequestBody(ref.Request) {
					ref.Request["body"] = replace(body)
				}
			}
			return nil
		}
		action.Target = replace(action.Target)
		if safeBody(action) {
			action.Body = replace(action.Body)
			for _, value := range action.BodyMultipart.MultipartTextValues() {
				*value = replace(*value)
			}
		}
		return nil
	})
	for key, ok := range qualifies {
		if ok {
			report.Keys = append(report.Keys, key)
		} else {
			report.LeftAsRecord += len(values[key])
		}
	}
	sort.Strings(report.Keys)
	return report
}

// safeBody reports whether a body is plain text the pass may rewrite.
func safeBody(action *dsl.Action) bool {
	return action.BodyFile == "" && action.BodyEncoding == "" &&
		action.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] == nil &&
		action.Metadata[dsl.ImportAuraTemplateContractMetadataKey] == nil
}

// safeRequestBody is safeBody for a request held as a map (an async
// conversation's request or an http_sequence child).
func safeRequestBody(request map[string]any) bool {
	if file, _ := request["body_file"].(string); file != "" {
		return false
	}
	if encoding, _ := request["body_encoding"].(string); encoding != "" {
		return false
	}
	metadata, _ := request["metadata"].(map[string]any)
	return metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] == nil && metadata[dsl.ImportAuraTemplateContractMetadataKey] == nil
}

func keyBefore(prefix string) string {
	if len(prefix) > 96 {
		prefix = prefix[len(prefix)-96:]
	}
	prefix = strings.NewReplacer("%22", `"`, "%3A", ":", "%3a", ":", "%3D", "=", "%3d", "=").Replace(prefix)
	if match := keyPattern.FindStringSubmatch(prefix); match != nil {
		key := match[1]
		if index := strings.LastIndexAny(key, ".["); index >= 0 {
			key = key[index+1:]
		}
		return key
	}
	return ""
}
