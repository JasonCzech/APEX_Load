package browser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol/http/pagecontext"
	"github.com/jasonczech/apexload/pkg/selector"
)

// HTTP script levels for the HTTP script generated from a browser recording,
// mirroring VuGen's URL-based and HTML-based recording levels.
const (
	HTTPScriptLevelURL  = "url"
	HTTPScriptLevelHTML = "html"
)

// stepMatchWindow bounds how long after a DOM step its navigation may start.
const stepMatchWindow = 10 * time.Second

type harDocument struct {
	index   int // 1-based entry index, the converter's SourceEntryID
	started time.Time
	method  string
	url     string
	html    string
}

// HTMLLevelSteps ties each recorded click and form submission to the main
// document request it caused and describes it as an HTML-mode step, so the
// generated HTTP script follows links, images and forms on the replayed page
// (VuGen's HTML-based script) instead of replaying recorded URLs. It returns
// the steps by 1-based HAR entry index and a note for each DOM step that
// stays a URL-level request.
func HTMLLevelSteps(steps []Step, har []byte) (map[int]map[string]any, []string) {
	documents, err := harDocuments(har)
	if err != nil || len(documents) == 0 {
		return nil, nil
	}
	ordered := append([]Step(nil), steps...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Timestamp.Before(ordered[j].Timestamp) })
	result := map[int]map[string]any{}
	var notes []string
	used := map[int]bool{}
	for index, step := range ordered {
		if step.Excluded || (step.Kind != StepClick && !(step.Kind == StepPressKey && strings.EqualFold(step.Value, "Enter"))) {
			continue
		}
		next := time.Time{}
		if index+1 < len(ordered) {
			next = ordered[index+1].Timestamp
		}
		document, previous := documentAfter(documents, step.Timestamp, next, used)
		if document == nil {
			continue // the step did not navigate
		}
		used[document.index] = true
		htmlStep, reason := htmlStepForDOMStep(step, ordered[:index], document, previous)
		if reason != "" {
			notes = append(notes, fmt.Sprintf("step %d stays a URL request: %s", step.Seq, reason))
			continue
		}
		result[document.index] = htmlStep
	}
	return result, notes
}

// documentAfter returns the first unused main-document request that starts
// after the step (and before the next step, within the match window), and
// the main document loaded before it.
func documentAfter(documents []harDocument, at, next time.Time, used map[int]bool) (*harDocument, *harDocument) {
	var previous *harDocument
	for i := range documents {
		document := &documents[i]
		if document.started.Before(at.Add(-50 * time.Millisecond)) {
			previous = document
			continue
		}
		if used[document.index] || document.started.After(at.Add(stepMatchWindow)) || (!next.IsZero() && document.started.After(next)) {
			return nil, nil
		}
		return document, previous
	}
	return nil, nil
}

func htmlStepForDOMStep(step Step, earlier []Step, document, previous *harDocument) (map[string]any, string) {
	if previous == nil || previous.html == "" {
		return nil, "the page it was taken on was not recorded"
	}
	if len(step.FramePath) > 0 || step.FrameSelector != "" {
		return nil, "it acts inside a frame"
	}
	base, _ := url.Parse(previous.url)
	page := pagecontext.Parse([]byte(previous.html), base)
	element := step.TargetElement
	if element == nil {
		element = step.Element
	}
	if element == nil {
		return nil, "the recorder kept no element details"
	}
	target := strings.TrimSuffix(document.url, "#")
	switch tag := strings.ToLower(element.Tag); {
	case tag == "a":
		text := pagecontext.NormalizeText(element.Text)
		if text == "" {
			return nil, "the link has no visible text"
		}
		return ordinalStep(map[string]any{"kind": "link", "text": text}, func(ordinal int) (string, error) {
			return page.FindLink(text, ordinal, false)
		}, target)
	case tag == "img" || (tag == "input" && strings.EqualFold(element.Type, "image") && document.method == "GET"):
		alt, src := element.Attributes["alt"], element.Attributes["src"]
		if alt == "" && src == "" {
			return nil, "the image has no alt or src"
		}
		htmlStep := map[string]any{"kind": "image"}
		if alt != "" {
			htmlStep["alt"] = alt
		}
		if src != "" {
			htmlStep["src"] = src
		}
		return ordinalStep(htmlStep, func(ordinal int) (string, error) {
			found, err := page.FindImage(alt, src, ordinal, false)
			return found.Href, err
		}, target)
	case tag == "button" || tag == "input" || step.Kind == StepPressKey:
		return formStep(step, earlier, element, page, document, previous.started)
	default:
		return nil, fmt.Sprintf("a click on <%s> is not a link, image or form submit", tag)
	}
}

// ordinalStep finds which same-text (or same-image) match led to the
// recorded URL, so duplicates resolve to the one the user clicked.
func ordinalStep(htmlStep map[string]any, find func(int) (string, error), target string) (map[string]any, string) {
	for ordinal := 1; ordinal <= 64; ordinal++ {
		href, err := find(ordinal)
		if err != nil {
			break
		}
		if sameURL(href, target) {
			if ordinal > 1 {
				htmlStep["ordinal"] = ordinal
			}
			return htmlStep, ""
		}
	}
	return nil, "no matching link or image on the recorded page leads to the recorded URL"
}

// formStep describes a form submission with the fields the user typed
// after the page loaded and the button used to submit it.
func formStep(step Step, earlier []Step, submit *selector.ElementMetadata, page *pagecontext.Page, document *harDocument, pageLoaded time.Time) (map[string]any, string) {
	var fields []any
	var names []string
	for _, fill := range earlier {
		if fill.Excluded || fill.Timestamp.Before(pageLoaded) {
			continue
		}
		if fill.Kind != StepFill && fill.Kind != StepSelect {
			continue
		}
		element := fill.Element
		if element == nil || element.Name == "" {
			continue
		}
		if fill.Masked {
			return nil, fmt.Sprintf("field %q holds a masked value", element.Name)
		}
		value := fill.Value
		if fill.Kind == StepSelect && len(fill.Values) > 0 {
			value = fill.Values[0]
		}
		fields = append(fields, map[string]any{"name": element.Name, "value": value})
		names = append(names, element.Name)
	}
	index, err := page.FindForm(names, "", 1)
	if err != nil {
		return nil, "no form on the recorded page has every field the user typed"
	}
	form := page.Forms[index]
	if !sameURL(strings.SplitN(form.Action, "?", 2)[0], strings.SplitN(document.url, "?", 2)[0]) || !strings.EqualFold(form.Method, document.method) {
		return nil, "the form on the recorded page does not submit to the recorded request"
	}
	htmlStep := map[string]any{"kind": "form", "fields": fields}
	if submit != nil && submit.Name != "" && step.Kind == StepClick {
		if strings.EqualFold(submit.Type, "image") {
			htmlStep["submit"] = []any{map[string]any{"name": submit.Name + ".x", "value": "0"}, map[string]any{"name": submit.Name + ".y", "value": "0"}}
		} else {
			value := submit.Attributes["value"]
			htmlStep["submit"] = []any{map[string]any{"name": submit.Name, "value": value}}
		}
	}
	return htmlStep, ""
}

func sameURL(a, b string) bool {
	left, errLeft := url.Parse(a)
	right, errRight := url.Parse(b)
	if errLeft != nil || errRight != nil {
		return a == b
	}
	left.Fragment, right.Fragment = "", ""
	return strings.EqualFold(left.Host, right.Host) && left.Path == right.Path && left.RawQuery == right.RawQuery && strings.EqualFold(left.Scheme, right.Scheme)
}

// harDocuments lists the HAR's main-document requests in start order.
func harDocuments(har []byte) ([]harDocument, error) {
	var parsed struct {
		Log struct {
			Entries []struct {
				StartedDateTime string `json:"startedDateTime"`
				ResourceType    string `json:"_resourceType"`
				Request         struct {
					Method  string `json:"method"`
					URL     string `json:"url"`
					Headers []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"headers"`
				} `json:"request"`
				Response struct {
					Content struct {
						MimeType string `json:"mimeType"`
						Text     string `json:"text"`
						Encoding string `json:"encoding"`
					} `json:"content"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(har, &parsed); err != nil {
		return nil, err
	}
	var documents []harDocument
	for i, entry := range parsed.Log.Entries {
		started, err := time.Parse(time.RFC3339Nano, entry.StartedDateTime)
		if err != nil {
			continue
		}
		isDocument := strings.EqualFold(entry.ResourceType, "document")
		accept, xhr := "", false
		for _, header := range entry.Request.Headers {
			switch strings.ToLower(header.Name) {
			case "sec-fetch-dest":
				isDocument = isDocument || strings.EqualFold(header.Value, "document")
			case "accept":
				accept = header.Value
			case "x-requested-with":
				xhr = xhr || header.Value != ""
			}
		}
		html := strings.Contains(strings.ToLower(entry.Response.Content.MimeType), "text/html")
		if xhr || (!isDocument && !(html && strings.HasPrefix(strings.ToLower(accept), "text/html"))) {
			continue
		}
		text := entry.Response.Content.Text
		if strings.EqualFold(entry.Response.Content.Encoding, "base64") {
			if decoded, err := base64.StdEncoding.DecodeString(text); err == nil {
				text = string(decoded)
			}
		}
		if !html {
			text = ""
		}
		documents = append(documents, harDocument{index: i + 1, started: started, method: strings.ToUpper(entry.Request.Method), url: entry.Request.URL, html: text})
	}
	sort.SliceStable(documents, func(i, j int) bool { return documents[i].started.Before(documents[j].started) })
	return documents, nil
}
