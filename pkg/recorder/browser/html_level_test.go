package browser

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/selector"
)

func harEntry(started time.Time, method, url, mime, body string, headers map[string]string, postData string) map[string]any {
	requestHeaders := []any{}
	for name, value := range headers {
		requestHeaders = append(requestHeaders, map[string]any{"name": name, "value": value})
	}
	request := map[string]any{"method": method, "url": url, "httpVersion": "HTTP/1.1", "headers": requestHeaders, "queryString": []any{}, "cookies": []any{}, "headersSize": -1, "bodySize": len(postData)}
	if postData != "" {
		request["postData"] = map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": postData}
	}
	return map[string]any{
		"startedDateTime": started.Format(time.RFC3339Nano), "time": 5, "request": request,
		"response": map[string]any{"status": 200, "statusText": "OK", "httpVersion": "HTTP/1.1", "headers": []any{map[string]any{"name": "Content-Type", "value": mime}}, "cookies": []any{},
			"content": map[string]any{"size": len(body), "mimeType": mime, "text": body}, "redirectURL": "", "headersSize": -1, "bodySize": len(body)},
		"cache": map[string]any{}, "timings": map[string]any{"send": 0, "wait": 5, "receive": 0},
	}
}

func TestHTMLLevelHTTPScriptReplaysWithoutHiddenFieldCorrelation(t *testing.T) {
	var logins atomic.Int32
	issued := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/login":
			// The hidden token rotates on every page load.
			token := "TOKEN-" + string('A'+issued.Add(1))
			io.WriteString(w, `<html><a href="/help">Help</a><form method="post" action="/session"><input type="hidden" name="token" value="`+token+`"><input name="user"><input type="submit" name="go" value="Sign in"></form></html>`)
		case "/session":
			if r.Referer() != "http://"+r.Host+"/login" {
				t.Errorf("Referer = %q, want the live page", r.Referer())
			}
			body, _ := io.ReadAll(r.Body)
			want := "token=TOKEN-" + string('A'+issued.Load()) + "&user=ada&go=Sign+in"
			if string(body) != want {
				t.Errorf("login body = %q, want %q", body, want)
				w.WriteHeader(http.StatusForbidden)
				return
			}
			logins.Add(1)
			io.WriteString(w, `<html><a href="/account?id=7">Account</a></html>`)
		default:
			io.WriteString(w, "<html>ok</html>")
		}
	}))
	defer server.Close()

	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nav := map[string]string{"Accept": "text/html", "Sec-Fetch-Dest": "document"}
	recorded := map[string]any{"log": map[string]any{"version": "1.2", "creator": map[string]any{"name": "test", "version": "1"}, "entries": []any{
		harEntry(started, "GET", server.URL+"/login", "text/html", `<html><a href="/help">Help</a><form method="post" action="/session"><input type="hidden" name="token" value="T-RECORDED"><input name="user"><input type="submit" name="go" value="Sign in"></form></html>`, nav, ""),
		harEntry(started.Add(3*time.Second), "POST", server.URL+"/session", "text/html", `<html><a href="/account?id=7">Account</a></html>`, map[string]string{"Accept": "text/html", "Sec-Fetch-Dest": "document", "Referer": server.URL + "/login?recorded=1"}, "token=T-RECORDED&user=ada&go=Sign+in"),
		harEntry(started.Add(5*time.Second), "GET", server.URL+"/account?id=7", "text/html", `<html>account</html>`, nav, ""),
	}}}
	harBytes, _ := json.Marshal(recorded)
	steps := []Step{
		{Seq: 1, Kind: StepNavigate, Timestamp: started.Add(-time.Millisecond), URL: server.URL + "/login"},
		{Seq: 2, Kind: StepFill, Timestamp: started.Add(time.Second), Value: "ada", Element: &selector.ElementMetadata{Tag: "input", Name: "user"}},
		{Seq: 3, Kind: StepClick, Timestamp: started.Add(3*time.Second - 100*time.Millisecond), Element: &selector.ElementMetadata{Tag: "input", Type: "submit", Name: "go", Attributes: map[string]string{"value": "Sign in"}}},
		{Seq: 4, Kind: StepClick, Timestamp: started.Add(5*time.Second - 100*time.Millisecond), Element: &selector.ElementMetadata{Tag: "a", Text: " Account "}},
	}
	htmlSteps, notes := HTMLLevelSteps(steps, harBytes)
	if len(notes) != 0 || len(htmlSteps) != 2 {
		t.Fatalf("html steps = %v, notes = %v", htmlSteps, notes)
	}
	if htmlSteps[2]["kind"] != "form" || htmlSteps[3]["kind"] != "link" || htmlSteps[3]["text"] != "Account" {
		t.Fatalf("html steps = %v", htmlSteps)
	}

	result, err := har.Convert(harBytes, har.ConvertOptions{HTMLSteps: htmlSteps})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Script.Actions {
		if strings.Contains(action.Body, "T-RECORDED") {
			t.Fatalf("the recorded hidden token must not be replayed: %+v", action)
		}
		if len(action.Extractors) != 0 {
			t.Fatalf("an HTML-level script needs no hidden-field correlation, got %+v on %s", action.Extractors, action.Name)
		}
	}
	for iteration := 0; iteration < 2; iteration++ {
		report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
		if err != nil || !report.Success {
			t.Fatalf("iteration %d: %v %+v", iteration, err, report)
		}
	}
	if logins.Load() != 2 {
		t.Fatalf("logins = %d, want one per iteration with the live token", logins.Load())
	}
}

func TestHTMLLevelStepsFallBackToURLRequests(t *testing.T) {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nav := map[string]string{"Sec-Fetch-Dest": "document"}
	page := `<html><a href="/a">Go</a><a href="/b">Go</a><div id="menu">Menu</div></html>`
	recorded := map[string]any{"log": map[string]any{"entries": []any{
		harEntry(started, "GET", "https://shop.test/", "text/html", page, nav, ""),
		harEntry(started.Add(2*time.Second), "GET", "https://shop.test/b", "text/html", page, nav, ""),
		harEntry(started.Add(4*time.Second), "GET", "https://shop.test/menu", "text/html", page, nav, ""),
		harEntry(started.Add(6*time.Second), "GET", "https://shop.test/c", "text/html", page, nav, ""),
	}}}
	harBytes, _ := json.Marshal(recorded)
	steps := []Step{
		{Seq: 1, Kind: StepClick, Timestamp: started.Add(2*time.Second - 10*time.Millisecond), Element: &selector.ElementMetadata{Tag: "a", Text: "Go"}},
		{Seq: 2, Kind: StepClick, Timestamp: started.Add(4*time.Second - 10*time.Millisecond), Element: &selector.ElementMetadata{Tag: "div", Text: "Menu"}},
		{Seq: 3, Kind: StepClick, Timestamp: started.Add(6*time.Second - 10*time.Millisecond), FramePath: []string{"nav"}, Element: &selector.ElementMetadata{Tag: "a", Text: "Go"}},
		{Seq: 4, Kind: StepHover, Timestamp: started.Add(7 * time.Second)},
	}
	htmlSteps, notes := HTMLLevelSteps(steps, harBytes)
	if len(htmlSteps) != 1 || htmlSteps[2]["ordinal"] != 2 {
		t.Fatalf("the second same-text link must resolve by ordinal: %v", htmlSteps)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "not a link, image or form submit") || !strings.Contains(notes[1], "inside a frame") {
		t.Fatalf("notes = %v", notes)
	}
}

// An XHR is never a main document, whatever order its headers are in.
func TestHARDocumentsSkipXHRWhateverTheHeaderOrder(t *testing.T) {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	xhr := harEntry(started, "GET", "https://shop.test/fragment", "text/html", "<p>x</p>", nil, "")
	xhr["request"].(map[string]any)["headers"] = []any{
		map[string]any{"name": "X-Requested-With", "value": "XMLHttpRequest"},
		map[string]any{"name": "Sec-Fetch-Dest", "value": "document"},
	}
	harBytes, _ := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{xhr}}})
	documents, err := harDocuments(harBytes)
	if err != nil || len(documents) != 0 {
		t.Fatalf("documents = %+v, %v; want the XHR left out", documents, err)
	}
}
