package paritytest

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func strPtr(value string) *string { return &value }

func TestServerServesScriptedResponsesInOrderAndRepeatsTheLast(t *testing.T) {
	server := NewServer([]Response{
		{Method: "GET", Path: "/poll", Body: "first"},
		{Method: "GET", Path: "/poll", Status: 202, Body: "second " + Origin},
	})
	defer server.Close()
	var bodies []string
	var statuses []int
	for i := 0; i < 3; i++ {
		resp, err := http.Get(server.URL + "/poll")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodies = append(bodies, string(body))
		statuses = append(statuses, resp.StatusCode)
	}
	want := []string{"first", "second " + server.URL, "second " + server.URL}
	for i := range want {
		if bodies[i] != want[i] {
			t.Fatalf("body %d = %q, want %q", i, bodies[i], want[i])
		}
	}
	if statuses[0] != 200 || statuses[2] != 202 {
		t.Fatalf("statuses = %v", statuses)
	}
	if got := len(server.Recorded()); got != 3 {
		t.Fatalf("recorded %d requests, want 3", got)
	}
}

func TestCompareReportsEveryKindOfMismatch(t *testing.T) {
	server := NewServer(nil)
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/login?a=1", strings.NewReader("user=x"))
	req.Header.Set("Referer", server.URL+"/home")
	req.Header.Set("Authorization", "Basic abc")
	req.Header.Set("Cookie", "SESSIONID=a; SESSIONID=b")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, err := http.Get(server.URL + "/extra"); err != nil {
		t.Fatal(err)
	}

	problems := server.Compare([]ExpectedRequest{{
		Method:        "POST",
		Path:          "/login",
		Query:         strPtr("a=2"),
		Headers:       map[string]string{"Referer": Origin + "/elsewhere"},
		AbsentHeaders: []string{"Authorization"},
		HeaderCounts:  map[string]int{"Cookie:SESSIONID": 1},
		Body:          strPtr("user=y"),
	}})
	joined := strings.Join(problems, "\n")
	for _, fragment := range []string{"query =", "header Referer =", "Authorization must be absent", "SESSIONID 2 time(s)", "body =", "unexpected extra request GET /extra"} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("problems do not mention %q:\n%s", fragment, joined)
		}
	}

	if problems := server.Compare(nil); len(problems) != 2 {
		t.Fatalf("an empty expectation must flag both requests as extra, got %v", problems)
	}
}

func TestCompareAcceptsAnExactMatchIncludingBinaryBodies(t *testing.T) {
	server := NewServer(nil)
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/bin", strings.NewReader("\x00\x01A\xff"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	problems := server.Compare([]ExpectedRequest{{Method: "POST", Path: "/bin", Query: strPtr(""), BodyBase64: strPtr("AAFB/w==")}})
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

func TestLoadExpectationValidatesAndPrefersTheReplayCapture(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("expected.json", `{"description":"d","outcome":"gated","source":"s"}`)
	if _, err := LoadExpectation(dir); err == nil || !strings.Contains(err.Error(), "reason codes") {
		t.Fatalf("a gated case without reason codes must be rejected, got %v", err)
	}
	write("expected.json", `{"description":"d","outcome":"replay","source":"s","typo":1}`)
	if _, err := LoadExpectation(dir); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	write("expected.json", `{"description":"d","outcome":"replay","source":"","requests":[]}`)
	if _, err := LoadExpectation(dir); err == nil {
		t.Fatal("a case without a source must be rejected")
	}
	write("expected.json", `{"description":"d","outcome":"replay","source":"s","requests":[{"method":"GET","path":"/hand-written"}]}`)
	write("replay.har", `{"log":{"entries":[
	  {"request":{"method":"get","url":"http://parity.test/home?x=1","headers":[{"name":"Referer","value":"http://parity.test/"}]},"response":{"content":{"mimeType":"text/html"}}},
	  {"_resourceType":"image","request":{"method":"GET","url":"http://parity.test/logo.png"},"response":{"content":{"mimeType":"image/png"}}},
	  {"request":{"method":"POST","url":"http://parity.test/bin","postData":{"text":"AAE=","encoding":"base64"}},"response":{"content":{"mimeType":"application/json"}}}
	]}}`)
	exp, err := LoadExpectation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.Requests) != 2 || exp.Requests[0].Path != "/home" || *exp.Requests[0].Query != "x=1" ||
		exp.Requests[0].Headers["Referer"] != "http://parity.test/" || exp.Requests[0].Method != "GET" {
		t.Fatalf("requests from replay.har = %+v", exp.Requests)
	}
	if exp.Requests[1].BodyBase64 == nil || *exp.Requests[1].BodyBase64 != "AAE=" {
		t.Fatalf("binary post data = %+v", exp.Requests[1])
	}
}

func TestGateReasonCodesFindsNestedCodes(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{
		{Name: "a", Metadata: map[string]any{"import_review": map[string]any{"reason_codes": []string{"b_code", "a_code"}}}},
		{Name: "loop", Control: &dsl.ControlFlow{Steps: []dsl.Action{
			{Name: "inner", Metadata: map[string]any{"import_review": map[string]any{"reason_codes": []string{"a_code", "c_code"}}}},
		}}},
	}}
	got := strings.Join(GateReasonCodes(script), ",")
	if got != "a_code,b_code,c_code" {
		t.Fatalf("codes = %s", got)
	}
}
