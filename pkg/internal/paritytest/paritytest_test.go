package paritytest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func strPtr(value string) *string { return &value }

func TestCompareWaitsForFinalWebSocketMessage(t *testing.T) {
	server := NewServer([]Response{{Method: "GET", Path: "/chat", WebSocket: &WebSocketBehavior{}}})
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(server.Expand(WebSocketOrigin)+"/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(gorillawebsocket.TextMessage, []byte("leave")); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if problems := server.Compare([]ExpectedRequest{{Method: "GET", Path: "/chat", Messages: []string{"leave"}}}); len(problems) != 0 {
		t.Fatalf("final message was not recorded: %v", problems)
	}
	if problems := server.Compare([]ExpectedRequest{{Method: "GET", Path: "/chat", Messages: []string{"leave", "missing"}}}); len(problems) == 0 {
		t.Fatal("waiting for completion must not hide a missing message")
	}
}

func TestWebSocketFixtureWaitHonorsDeadline(t *testing.T) {
	server := NewServer([]Response{{Method: "GET", Path: "/chat", WebSocket: &WebSocketBehavior{}}})
	defer server.Close()
	conn, _, err := gorillawebsocket.DefaultDialer.Dial(server.Expand(WebSocketOrigin)+"/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := server.waitForWebSockets(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished socket wait = %v, want deadline", err)
	}
}

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
	write("expected.json", `{"description":"d","outcome":"review","source":"s"}`)
	if _, err := LoadExpectation(dir); err == nil || !strings.Contains(err.Error(), "reason codes") {
		t.Fatalf("a review case without reason codes must be rejected, got %v", err)
	}
	write("expected.json", `{"description":"d","outcome":"maybe","source":"s"}`)
	if _, err := LoadExpectation(dir); err == nil {
		t.Fatal("an unknown outcome must be rejected")
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
	script.Actions = append(script.Actions, dsl.Action{Name: "review", Metadata: map[string]any{"review_gate": "r_code", "review_reasons": []string{"r_code", "s_code"}, "execution_blocker": "x_code"}})
	if got := strings.Join(GateReasonCodes(script), ","); got != "a_code,b_code,c_code,r_code,s_code,x_code" {
		t.Fatalf("all codes = %s", got)
	}
	if got := strings.Join(ExecutionBlockerCodes(script), ","); got != "a_code,b_code,c_code,x_code" {
		t.Fatalf("execution codes = %s", got)
	}
	if got := strings.Join(ReviewReasonCodes(script), ","); got != "r_code,s_code" {
		t.Fatalf("review codes = %s", got)
	}
}

func TestMultiValueResponseHeadersAndRequestHeaderOccurrences(t *testing.T) {
	server := NewServer([]Response{
		{
			Method: "GET",
			Path:   "/login",
			HeaderList: dsl.HeaderList{
				{Name: "Set-Cookie", Value: "SESSIONID=sess-1; Path=/"},
				{Name: "Set-Cookie", Value: "XSRF=tok-2; Path=/"},
			},
			HeaderOccurrences: map[string][]string{
				"X-Server-Trace": {"hop-1", "hop-2 " + Origin},
			},
			Body: "ok",
		},
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	gotCookies := resp.Header.Values("Set-Cookie")
	if len(gotCookies) != 2 || gotCookies[0] != "SESSIONID=sess-1; Path=/" || gotCookies[1] != "XSRF=tok-2; Path=/" {
		t.Fatalf("Set-Cookie occurrences = %#v, want 2 distinct headers", gotCookies)
	}
	gotTraces := resp.Header.Values("X-Server-Trace")
	if len(gotTraces) != 2 || gotTraces[0] != "hop-1" || gotTraces[1] != "hop-2 "+server.URL {
		t.Fatalf("X-Server-Trace occurrences = %#v", gotTraces)
	}

	// Send a request with two separate X-Multi header occurrences and a single comma-joined X-Joined header.
	req, err := http.NewRequest("POST", server.URL+"/submit", strings.NewReader("ok"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Add("X-Multi", "alpha")
	req.Header.Add("X-Multi", "beta "+server.URL)
	req.Header.Set("X-Joined", "alpha, beta")
	postResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()

	// Exact occurrence match succeeds via both HeaderOccurrences and HeaderList.
	if problems := server.Compare([]ExpectedRequest{
		{Method: "GET", Path: "/login"},
		{
			Method: "POST",
			Path:   "/submit",
			HeaderOccurrences: map[string][]string{
				"X-Multi": {"alpha", "beta " + Origin},
			},
			HeaderList: dsl.HeaderList{
				{Name: "X-Multi", Value: "alpha"},
				{Name: "X-Multi", Value: "beta " + Origin},
			},
		},
	}); len(problems) != 0 {
		t.Fatalf("unexpected problems for matching header occurrences: %v", problems)
	}

	// Distinguishes two separate occurrences from a single comma-joined occurrence,
	// and catches swapped occurrence order.
	mismatchProblems := server.Compare([]ExpectedRequest{
		{Method: "GET", Path: "/login"},
		{
			Method: "POST",
			Path:   "/submit",
			HeaderOccurrences: map[string][]string{
				"X-Joined": {"alpha", "beta"},
				"X-Multi":  {"beta " + Origin, "alpha"},
			},
		},
	})
	joined := strings.Join(mismatchProblems, "\n")
	if !strings.Contains(joined, "header X-Joined occurrences") {
		t.Fatalf("expected X-Joined comma-joined vs separate occurrence mismatch, got:\n%s", joined)
	}
	if !strings.Contains(joined, "header X-Multi occurrences") {
		t.Fatalf("expected X-Multi occurrence order mismatch, got:\n%s", joined)
	}
}

func TestServerBarrierAndDependencyAssertions(t *testing.T) {
	server := NewServer([]Response{
		{Method: "GET", Path: "/page", Body: "main"},
		{Method: "GET", Path: "/asset/a.css", Barrier: "assets", BarrierCount: 2, Body: "css"},
		{Method: "GET", Path: "/asset/b.js", Barrier: "assets", BarrierCount: 2, Body: "js"},
		{Method: "POST", Path: "/after", Body: "done"},
	})
	defer server.Close()

	// 1. Prerequisite request completes before concurrent asset wave starts.
	resp, err := http.Get(server.URL + "/page")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// 2. Two concurrent asset requests rendezvous at the "assets" barrier.
	errCh := make(chan error, 2)
	for _, path := range []string{"/asset/a.css", "/asset/b.js"} {
		p := path
		go func() {
			r, err := http.Get(server.URL + p)
			if err == nil {
				r.Body.Close()
			}
			errCh <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}

	// 3. Dependent request runs after the asset wave completes.
	postResp, err := http.Post(server.URL+"/after", "text/plain", strings.NewReader("go"))
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()

	if got := server.MaxInFlight(); got < 2 {
		t.Fatalf("MaxInFlight() = %d, want >= 2", got)
	}

	problems := server.CheckDependencies(
		DependencyRule{
			Prerequisite:   "GET /page",
			Dependents:     []string{"GET /asset/a.css", "GET /asset/b.js"},
			OverlapGroup:   []string{"GET /asset/a.css", "GET /asset/b.js"},
			MinMaxInFlight: 2,
		},
		DependencyRule{
			Prerequisite: "GET /asset/a.css",
			Dependents:   []string{"POST /after"},
		},
		DependencyRule{
			Prerequisite: "GET /asset/b.js",
			Dependents:   []string{"POST /after"},
		},
	)
	if len(problems) != 0 {
		t.Fatalf("expected dependency and overlap rules to pass, got: %v", problems)
	}

	// Verify negative detection when sequential requests are asserted to overlap
	// or when prerequisite order is inverted.
	negProblems := server.CheckDependencies(
		DependencyRule{
			Prerequisite: "POST /after",
			Dependents:   []string{"GET /page"},
		},
		DependencyRule{
			OverlapGroup:   []string{"GET /page", "POST /after"},
			MinMaxInFlight: 2,
		},
	)
	negJoined := strings.Join(negProblems, "\n")
	if !strings.Contains(negJoined, "dependency violation") {
		t.Fatalf("expected dependency violation, got:\n%s", negJoined)
	}
	if !strings.Contains(negJoined, "concurrency violation") {
		t.Fatalf("expected concurrency violation, got:\n%s", negJoined)
	}
}

func TestBarrierTimeoutDecrementsWaitingForSubsequentWave(t *testing.T) {
	server := NewServer([]Response{
		{Method: "GET", Path: "/wave/a", Barrier: "wave_barrier", BarrierCount: 2, BarrierTimeoutMs: 40, Body: "a"},
		{Method: "GET", Path: "/wave/b", Barrier: "wave_barrier", BarrierCount: 2, BarrierTimeoutMs: 500, Body: "b"},
	})
	defer server.Close()
	server.SetBarrier("wave_barrier", 2, 40*time.Millisecond)

	// Wave 1: send only 1 request; it times out at the barrier after ~40ms.
	resp1, err := http.Get(server.URL + "/wave/a")
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()

	// Reconfigure with a longer timeout for wave 2 and verify a single waiter
	// does NOT release early from a stale waiting count.
	server.mu.Lock()
	if server.barriers["wave_barrier"].waiting != 0 {
		w := server.barriers["wave_barrier"].waiting
		server.mu.Unlock()
		t.Fatalf("barrier waiting after timeout = %d, want 0", w)
	}
	server.barriers["wave_barrier"].timeout = 500 * time.Millisecond
	server.mu.Unlock()

	errCh := make(chan error, 2)
	go func() {
		r, err := http.Get(server.URL + "/wave/a")
		if err == nil {
			r.Body.Close()
		}
		errCh <- err
	}()
	go func() {
		r, err := http.Get(server.URL + "/wave/b")
		if err == nil {
			r.Body.Close()
		}
		errCh <- err
	}()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}

	recorded := server.Recorded()
	if len(recorded) != 3 {
		t.Fatalf("recorded = %d, want 3", len(recorded))
	}
	// The second wave's two requests must have overlapped in flight (at least one saw InFlightAtArrival >= 2).
	if recorded[1].InFlightAtArrival < 2 && recorded[2].InFlightAtArrival < 2 {
		t.Fatalf("wave 2 requests did not overlap in flight: r1.InFlightAtArrival=%d r2.InFlightAtArrival=%d",
			recorded[1].InFlightAtArrival, recorded[2].InFlightAtArrival)
	}
}

func TestCheckDependenciesDetectsMismatchedCountSecondWaveOvertake(t *testing.T) {
	server := NewServer([]Response{
		// Wave 1 prereq completes immediately.
		{Method: "GET", Path: "/prereq", Body: "p1"},
		// Wave 2 prereq is held at a barrier until wave 2 dependent arrives, so the
		// dependent overtakes wave 2's prerequisite before it completes.
		{Method: "GET", Path: "/prereq", Barrier: "overtake", BarrierCount: 2, Body: "p2"},
		{Method: "GET", Path: "/dep", Body: "d1"},
		{Method: "GET", Path: "/dep", Barrier: "overtake", BarrierCount: 2, Body: "d2-overtook"},
		{Method: "GET", Path: "/dep", Body: "d2-extra"},
	})
	defer server.Close()

	// Wave 1: prereq1 completes, then dep1 completes.
	for _, path := range []string{"/prereq", "/dep"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	// Wave 2: prereq2 starts first, then dep2 arrives while prereq2 is still in flight
	// (held at the "overtake" barrier), followed by a 3rd /dep so len(prereqs)=2 != len(deps)=3.
	prereq2Done := make(chan error, 1)
	go func() {
		r, err := http.Get(server.URL + "/prereq")
		if err == nil {
			r.Body.Close()
		}
		prereq2Done <- err
	}()
	// Wait until prereq2 is recorded at the server before sending dep2.
	deadline := time.Now().Add(2 * time.Second)
	for len(server.Recorded()) < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	respDep2, err := http.Get(server.URL + "/dep")
	if err != nil {
		t.Fatal(err)
	}
	respDep2.Body.Close()
	if err := <-prereq2Done; err != nil {
		t.Fatal(err)
	}
	respDep3, err := http.Get(server.URL + "/dep")
	if err != nil {
		t.Fatal(err)
	}
	respDep3.Body.Close()

	problems := server.CheckDependencies(DependencyRule{
		Prerequisite: "GET /prereq",
		Dependents:   []string{"GET /dep"},
	})
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "dependency violation") || !strings.Contains(joined, "seq 3") {
		t.Fatalf("expected dependency violation on wave-2 prerequisite (seq 3) overtaken by dependent (seq 4), got:\n%s", joined)
	}
}
