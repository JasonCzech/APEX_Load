package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHTMLStepsResolveAgainstTheCurrentPage(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" ref="+r.Referer()+" body="+string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/start":
			io.WriteString(w, `<a href="/next?n=1">Next</a><a href="/next?n=2">Next</a>
<form method="post" action="/login"><input type="hidden" name="vs" value="live"><input name="user"><input type="submit" name="go" value="Go"></form>
<img src="/logo.png" alt="Logo">`)
		default:
			io.WriteString(w, "<p>done</p>")
		}
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{HTMLPageContextConfig: true})
	run := func(action *protocol.Action) *protocol.Result {
		t.Helper()
		result, err := h.Execute(context.Background(), action)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := run(&protocol.Action{Type: "GET", Target: server.URL + "/start"}); !result.Success {
		t.Fatalf("start: %s", result.ErrorMessage)
	}
	step := func(fields map[string]any) *protocol.Action {
		return &protocol.Action{Type: "GET", Target: "#html-step", Params: map[string]any{HTMLStepParam: fields}}
	}
	if result := run(step(map[string]any{"kind": "image_check", "alt": "Logo"})); !result.Success {
		t.Fatalf("image check: %s", result.ErrorMessage)
	}
	if result := run(step(map[string]any{"kind": "link", "text": "Next", "ordinal": float64(2)})); !result.Success || result.ProtocolEvidence["page_context_url"] != server.URL+"/start" {
		t.Fatalf("link: %s %v", result.ErrorMessage, result.ProtocolEvidence)
	}
	// The link loaded a new page without the form, so the form is gone.
	result := run(&protocol.Action{Type: "POST", Target: "#html-step", Params: map[string]any{HTMLStepParam: map[string]any{"kind": "form", "fields": []any{map[string]any{"name": "user", "value": "ada"}}}}})
	if result.Success || !strings.Contains(result.ErrorMessage, HTMLFormNotFound) || result.ProtocolEvidence["failure_code"] != HTMLFormNotFound {
		t.Fatalf("form on the wrong page: %v %s %v", result.Success, result.ErrorMessage, result.ProtocolEvidence)
	}
	run(&protocol.Action{Type: "GET", Target: server.URL + "/start"})
	result = run(&protocol.Action{Type: "POST", Target: "#html-step", Params: map[string]any{HTMLStepParam: map[string]any{
		"kind": "form", "fields": []any{map[string]any{"name": "user", "value": "ada"}}, "submit": []any{map[string]any{"name": "go", "value": "Go"}},
	}}})
	if !result.Success {
		t.Fatalf("form: %s", result.ErrorMessage)
	}
	if err := h.ClearCookies(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result := run(step(map[string]any{"kind": "link", "text": "Next"})); result.Success || !strings.Contains(result.ErrorMessage, "no HTML page has been loaded") {
		t.Fatalf("a cleared session must forget its page: %v %s", result.Success, result.ErrorMessage)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"GET /start ref= body=",
		"GET /next?n=2 ref=" + server.URL + "/start body=",
		"GET /start ref= body=",
		"POST /login ref=" + server.URL + "/start body=vs=live&user=ada&go=Go",
	}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
	}
}

func TestHTMLStepsNeedPageContextConfig(t *testing.T) {
	h := initCompatibilityHTTP(t, map[string]any{})
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: "#html-step", Params: map[string]any{HTMLStepParam: map[string]any{"kind": "link", "text": "x"}}})
	if err != nil || result.Success || !strings.Contains(result.ErrorMessage, HTMLLinkNotFound) {
		t.Fatalf("without page context: %v %v", result, err)
	}
}

func TestResourceStepsAndHTTPMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.png" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<a href="/next">Next</a>`)
	}))
	defer server.Close()
	resource := &protocol.Action{Type: "GET", Target: server.URL + "/missing.png", Params: map[string]any{"resource_step": true}}

	lenient := initCompatibilityHTTP(t, map[string]any{HTMLPageContextConfig: true})
	result, err := lenient.Execute(context.Background(), resource)
	if err != nil || !result.Success || result.ProtocolEvidence["resource_step_error"] == nil {
		t.Fatalf("a missing resource step must warn: %+v %v", result, err)
	}
	// A resource step that runs past the page deadline warns too, unless
	// resource timeouts are explicitly failures.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	slowResource := &protocol.Action{Type: "GET", Target: slow.URL + "/slow.png", Params: map[string]any{"resource_step": true}}
	paced := initCompatibilityHTTP(t, map[string]any{"page_timeout_ms": 50})
	if result, err := paced.Execute(context.Background(), slowResource); err != nil || !result.Success || result.ProtocolEvidence["resource_step_failed"] != true {
		t.Fatalf("a resource step past the page deadline must warn: %+v %v", result, err)
	}
	timeoutsFail := initCompatibilityHTTP(t, map[string]any{"page_timeout_ms": 50, "resource_timeout_as_warning": false})
	if result, _ := timeoutsFail.Execute(context.Background(), slowResource); result.Success {
		t.Fatal("resource_timeout_as_warning=false must keep a resource step's timeout a failure")
	}
	// An invalid resource step is an authoring error, not a resource failure.
	invalid := &protocol.Action{Type: "GET", Target: server.URL + "/missing.png", Params: map[string]any{"resource_step": true}, Headers: map[string]string{"Bad\nName": "x"}}
	if result, _ := lenient.Execute(context.Background(), invalid); result.Success || result.ProtocolEvidence["resource_step_failed"] == true {
		t.Fatalf("an invalid resource step must fail: %+v", result)
	}
	strict := initCompatibilityHTTP(t, map[string]any{"fail_noncritical_resources": true})
	if result, _ := strict.Execute(context.Background(), resource); result.Success {
		t.Fatal("fail_noncritical_resources must keep the failure")
	}

	// A Mode=HTTP page never becomes the page context.
	if result, _ := lenient.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL + "/page", Params: map[string]any{"html_mode": false}}); !result.Success {
		t.Fatalf("http-mode page: %s", result.ErrorMessage)
	}
	if lenient.page.current() != nil {
		t.Fatal("a Mode=HTTP step must leave the page context unchanged")
	}
	lenient.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL + "/page"})
	if page := lenient.page.current(); page == nil || len(page.Links) != 1 {
		t.Fatalf("an HTML-mode page must become the page context, got %+v", page)
	}
}

// A page served in a legacy charset is decoded before it is indexed, so
// non-ASCII link text matches, and its form is submitted in that charset as
// a browser does (characters the charset lacks as &#NNNN; references).
func TestHTMLStepsFollowThePageCharset(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.URL.RequestURI()+" "+string(body))
		mu.Unlock()
		switch r.URL.Path {
		case "/start":
			w.Header().Set("Content-Type", "text/html; charset=windows-1252")
			io.WriteString(w, "<a href=\"/overview\">\xdcbersicht</a><form method=\"post\" action=\"/save\"><input type=\"hidden\" name=\"size\" value=\"Gr\xf6\xdfe\"><input name=\"who\"></form>")
		case "/meta":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<meta charset=\"iso-8859-1\"><a href=\"/caf\">Caf\xe9</a>")
		default:
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<p>done</p>")
		}
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{HTMLPageContextConfig: true})
	run := func(action *protocol.Action) {
		t.Helper()
		result, err := h.Execute(context.Background(), action)
		if err != nil || !result.Success {
			t.Fatalf("%s: %v %v", action.Target, err, result)
		}
	}
	step := func(fields map[string]any) *protocol.Action {
		return &protocol.Action{Type: "GET", Target: "#html-step", Params: map[string]any{HTMLStepParam: fields}}
	}
	run(&protocol.Action{Type: "GET", Target: server.URL + "/start"})
	run(step(map[string]any{"kind": "link", "text": "Übersicht"}))
	run(&protocol.Action{Type: "GET", Target: server.URL + "/start"})
	run(step(map[string]any{"kind": "form", "fields": []any{map[string]any{"name": "who", "value": "Müller €☃"}}}))
	run(&protocol.Action{Type: "GET", Target: server.URL + "/meta"})
	run(step(map[string]any{"kind": "link", "text": "Café"}))
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/start ", "/overview ", "/start ", "/save size=Gr%F6%DFe&who=M%FCller+%80%26%239731%3B", "/meta ", "/caf "}
	if strings.Join(bodies, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests =\n%s\nwant\n%s", strings.Join(bodies, "\n"), strings.Join(want, "\n"))
	}
}
