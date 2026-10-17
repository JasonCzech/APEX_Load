package http

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/http/pagecontext"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

// HTML-mode steps (LoadRunner web_link, web_image, web_submit_form and
// web_image_check) carry params.html_step instead of a URL. They resolve
// against the virtual user's page context, the index of the HTML main
// document it last loaded, when they run.
//
//	{"kind": "link", "text": "Next", "ordinal": 2, "case_insensitive": true}
//	{"kind": "image", "alt": "Search", "src": "/img/go.gif", "ordinal": 1}
//	{"kind": "image_check", "alt": "Logo"}
//	{"kind": "form", "fields": [{"name": "q", "value": "x"}], "action": "/search",
//	 "ordinal": 1, "hidden": [{"name": "__VIEWSTATE", "value": "..."}],
//	 "submit": [{"name": "go", "value": "Go"}]}

// HTMLStepParam is the action params key of an HTML-mode step.
const HTMLStepParam = httpcontract.HTMLStepParam

// HTMLPageContextConfig is the protocol config key that turns page indexing
// on. The engine sets it when a script has HTML-mode steps, so other scripts
// never parse their pages.
const HTMLPageContextConfig = "html_page_context"

// HTMLStepKinds are the supported html_step kinds.
var HTMLStepKinds = map[string]bool{"link": true, "image": true, "image_check": true, "form": true}

// Failure codes of an HTML-mode step that cannot be resolved.
const (
	HTMLLinkNotFound  = "html_link_not_found"
	HTMLImageNotFound = "html_image_not_found"
	HTMLFormNotFound  = "html_form_not_found"
)

// pageContextState is a virtual user's current page. It is shared by the
// per-HTTP-version clones of the protocol instance.
type pageContextState struct {
	mu   sync.Mutex
	page *pagecontext.Page
}

func (s *pageContextState) current() *pagecontext.Page {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.page
}

func (s *pageContextState) set(page *pagecontext.Page) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.page = page
	s.mu.Unlock()
}

// observeMainDocument replaces the page context with a successful HTML main
// response. Resource steps leave it unchanged.
func (s *pageContextState) observeMainDocument(action *protocol.Action, resp *http.Response, body []byte, complete bool) {
	if s == nil || resp == nil || !complete || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	if isResourceStep(action) || !stepHTMLMode(action) {
		return
	}
	// A background conversation's response never replaces the page the
	// script is on.
	if id, _ := action.Params["async_conversation"].(string); id != "" {
		return
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		return
	}
	var pageURL *url.URL
	if resp.Request != nil {
		pageURL = resp.Request.URL
	}
	decoded, charsetName := pagecontext.Decode(body, resp.Header.Get("Content-Type"))
	page := pagecontext.Parse(decoded, pageURL)
	page.Charset = charsetName
	s.set(page)
}

func htmlStepFields(raw any) []pagecontext.Field {
	items, _ := raw.([]any)
	fields := make([]pagecontext.Field, 0, len(items))
	for _, item := range items {
		entry, _ := item.(map[string]any)
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}
		value, _ := entry["value"].(string)
		fields = append(fields, pagecontext.Field{Name: name, Value: value})
	}
	return fields
}

func htmlStepInt(step map[string]any, key string) int {
	switch value := step[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	}
	return 0
}

func htmlStepString(step map[string]any, key string) string {
	value, _ := step[key].(string)
	return value
}

// htmlStepFailure is a resolution failure with its code.
type htmlStepFailure struct {
	code string
	err  error
}

func (f *htmlStepFailure) Error() string { return f.code + ": " + f.err.Error() }

// resolveHTMLStep turns an HTML-mode step into an ordinary request against
// the current page. It returns done when the step completes without a request
// (an image check).
func (h *HTTP) resolveHTMLStep(action *protocol.Action, step map[string]any) (*protocol.Action, bool, error) {
	kind := strings.ToLower(strings.TrimSpace(htmlStepString(step, "kind")))
	if !HTMLStepKinds[kind] {
		return nil, false, fmt.Errorf("html_step kind %q is not supported", kind)
	}
	if frame := strings.TrimSpace(htmlStepString(step, "frame")); frame != "" {
		return nil, false, fmt.Errorf("html_step targets frame %q; steps inside frames are not supported yet", frame)
	}
	page := h.page.current()
	caseInsensitive, _ := step["case_insensitive"].(bool)
	ordinal := htmlStepInt(step, "ordinal")
	resolved := *action
	resolved.Headers = cloneStringMap(action.Headers)
	resolved.HeaderList = append(protocol.HeaderList(nil), action.HeaderList...)
	resolved.Body = nil
	resolved.BodyEncoding = ""
	resolved.Method = http.MethodGet
	var formIndex = -1
	var submitter []pagecontext.Field
	switch kind {
	case "image_check":
		if !page.HasImage(htmlStepString(step, "alt"), htmlStepString(step, "src"), caseInsensitive) {
			return nil, false, &htmlStepFailure{HTMLImageNotFound, fmt.Errorf("image (alt %q, src %q) is not on the current page", htmlStepString(step, "alt"), htmlStepString(step, "src"))}
		}
		return nil, true, nil
	case "link":
		href, err := page.FindLink(htmlStepString(step, "text"), ordinal, caseInsensitive)
		if err != nil {
			return nil, false, &htmlStepFailure{HTMLLinkNotFound, err}
		}
		resolved.Target = href
	case "image":
		target, err := page.FindImage(htmlStepString(step, "alt"), htmlStepString(step, "src"), ordinal, caseInsensitive)
		if err != nil {
			return nil, false, &htmlStepFailure{HTMLImageNotFound, err}
		}
		if target.Form < 0 {
			resolved.Target = target.Href
			break
		}
		formIndex = target.Form
		if target.Name != "" {
			submitter = []pagecontext.Field{{Name: target.Name + ".x", Value: "0"}, {Name: target.Name + ".y", Value: "0"}}
		}
	case "form":
		fields := htmlStepFields(step["fields"])
		names := make([]string, 0, len(fields))
		for _, field := range fields {
			names = append(names, field.Name)
		}
		index, err := page.FindForm(names, htmlStepString(step, "action"), ordinal)
		if err != nil {
			return nil, false, &htmlStepFailure{HTMLFormNotFound, err}
		}
		formIndex = index
		submitter = htmlStepFields(step["submit"])
	}
	if formIndex >= 0 {
		form := page.Forms[formIndex]
		var scripted, fallback []pagecontext.Field
		if kind == "form" {
			scripted = htmlStepFields(step["fields"])
			fallback = htmlStepFields(step["hidden"])
		}
		fields := pagecontext.EncodeFields(form.Submission(scripted, fallback, submitter), page.Charset)
		if err := applyFormSubmission(&resolved, form, fields); err != nil {
			return nil, false, err
		}
	}
	if page != nil && page.URL != "" {
		if _, ok := headerValueFold(resolved.Headers, "Referer"); !ok && !headerListHas(resolved.HeaderList, "Referer") {
			if resolved.Headers == nil {
				resolved.Headers = map[string]string{}
			}
			resolved.Headers["Referer"] = page.URL
		}
	}
	return &resolved, false, nil
}

// applyFormSubmission encodes a form submission onto the resolved request
// as the form element says: GET replaces the action URL's query; POST sends
// the urlencoded, multipart or text/plain body.
func applyFormSubmission(resolved *protocol.Action, form pagecontext.Form, fields []pagecontext.Field) error {
	if form.Method == http.MethodGet {
		target, err := url.Parse(form.Action)
		if err != nil {
			return fmt.Errorf("form action %q is not a URL", form.Action)
		}
		target.RawQuery = pagecontext.EncodeURLForm(fields)
		resolved.Target = target.String()
		resolved.Method = http.MethodGet
		return nil
	}
	resolved.Target = form.Action
	resolved.Method = http.MethodPost
	var contentType string
	switch form.Enctype {
	case "multipart/form-data":
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for _, field := range fields {
			if err := writer.WriteField(field.Name, field.Value); err != nil {
				return err
			}
		}
		if err := writer.Close(); err != nil {
			return err
		}
		resolved.Body = body.Bytes()
		contentType = writer.FormDataContentType()
	case "text/plain":
		var body strings.Builder
		for _, field := range fields {
			body.WriteString(field.Name + "=" + field.Value + "\r\n")
		}
		resolved.Body = []byte(body.String())
		contentType = "text/plain"
	default:
		resolved.Body = []byte(pagecontext.EncodeURLForm(fields))
		contentType = "application/x-www-form-urlencoded"
	}
	if resolved.Headers == nil {
		resolved.Headers = map[string]string{}
	}
	for name := range resolved.Headers {
		if strings.EqualFold(name, "Content-Type") {
			delete(resolved.Headers, name)
		}
	}
	resolved.HeaderList = headerListWithout(resolved.HeaderList, "Content-Type")
	resolved.Headers["Content-Type"] = contentType
	return nil
}

func headerListHas(list protocol.HeaderList, name string) bool {
	for _, header := range list {
		if strings.EqualFold(header.Name, name) {
			return true
		}
	}
	return false
}

func headerListWithout(list protocol.HeaderList, name string) protocol.HeaderList {
	kept := list[:0:0]
	for _, header := range list {
		if !strings.EqualFold(header.Name, name) {
			kept = append(kept, header)
		}
	}
	return kept
}

func headerValueFold(headers map[string]string, name string) (string, bool) {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return "", false
}

// htmlStepFailureCode returns the failure code of a resolution error.
func htmlStepFailureCode(err error) string {
	var failure *htmlStepFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return ""
}

// pageContextEvidence summarizes the page an HTML-mode step resolved against.
func pageContextEvidence(page *pagecontext.Page) map[string]any {
	if page == nil {
		return map[string]any{"page_context_url": ""}
	}
	return map[string]any{
		"page_context_url":    page.URL,
		"page_context_links":  len(page.Links),
		"page_context_images": len(page.Images),
		"page_context_forms":  len(page.Forms),
	}
}

// isResourceStep reports a step recorded as a resource (LoadRunner
// Resource=1): its failures follow the non-critical resource policy and it
// never becomes the page context.
func isResourceStep(action *protocol.Action) bool {
	step, _ := action.Params["resource_step"].(bool)
	return step
}

// stepHTMLMode reports whether a step is replayed in HTML mode (LoadRunner
// Mode=HTML, the default): its page is parsed for resources and becomes the
// page context. Mode=HTTP (html_mode false) does neither.
func stepHTMLMode(action *protocol.Action) bool {
	mode, set := action.Params["html_mode"].(bool)
	return !set || mode
}

// downgradeResourceStepFailure turns a resource step's HTTP error or
// transport failure into a warning, as LoadRunner reports a failed resource
// step, unless fail_noncritical_resources is on (or the failure is a timeout
// and resource_timeout_as_warning is off).
func (p replayOutcomePolicy) downgradeResourceStepFailure(result *protocol.Result) {
	if result == nil || result.Success || p.failResources {
		return
	}
	transport := result.StatusCode == 0 && result.ErrorMessage != ""
	timeout := (transport && strings.Contains(strings.ToLower(result.ErrorMessage), "timeout")) || result.ErrorMessage == errPageDeadline.Error()
	if result.StatusCode < 400 && !transport && !timeout {
		return
	}
	if timeout && p.timeoutConfigured && !p.resourceTimeoutWarning {
		return
	}
	addReplayWarning(result, "resource_step_failed")
	result.ProtocolEvidence["resource_step_error"] = result.ErrorMessage
	if result.ErrorMessage == "" {
		result.ProtocolEvidence["resource_step_error"] = fmt.Sprintf("HTTP %d", result.StatusCode)
	}
	result.ErrorMessage = ""
	result.Success = true
}
