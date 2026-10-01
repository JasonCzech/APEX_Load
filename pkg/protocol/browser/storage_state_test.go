package browser

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestParseStorageState(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"empty object", `{}`, true},
		{"snapshot", `{"cookies":[{"name":"sid","value":"secret-cookie","domain":"example.test","path":"/"}],"origins":[{"origin":"https://example.test","localStorage":[{"name":"token","value":"secret-local"}]}]}`, true},
		{"null", `null`, false}, {"invalid", `secret-value`, false},
		{"unknown state", `{"origins":[{"origin":"https://example.test","indexedDB":[]}]}`, false},
		{"bad origin", `{"origins":[{"origin":"file:///tmp/state"}]}`, false},
		{"cookie scope", `{"cookies":[{"name":"sid","value":"secret-cookie"}]}`, false},
		{"oversize", strings.Repeat("x", MaxStorageStateBytes+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseStorageState(tc.raw)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret-value") {
				t.Fatal("secret leaked")
			}
		})
	}
	b := &Browser{}
	b.allowedTargetHosts = []string{"example.test"}
	err := b.configureStorageState(map[string]any{"browser_storage_state": `{"origins":[{"origin":"https://other.test"}]}`})
	if err == nil {
		t.Fatal("cross-target state accepted")
	}
}

func TestBrowserStorageStateSeedsFreshContexts(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<div id="state"></div><script>document.querySelector('#state').textContent=document.cookie+'|'+localStorage.getItem('token')</script>`)
	}))
	defer server.Close()
	raw := fmt.Sprintf(`{"cookies":[{"name":"sid","value":"cookie-value","domain":"127.0.0.1","path":"/"}],"origins":[{"origin":%q,"localStorage":[{"name":"token","value":"local-value"}]}]}`, server.URL)
	for i := 0; i < 2; i++ {
		b := &Browser{}
		if err := b.Init(context.Background(), map[string]any{"browser_storage_state": raw}); err != nil {
			requireBrowserRuntime(t, "", err)
		}
		result, err := b.Execute(context.Background(), &protocol.Action{Type: "browser_navigate", Target: server.URL})
		if err != nil || !result.Success {
			t.Fatalf("navigate: %+v %v", result, err)
		}
		value, err := b.currentPage().Locator("#state").TextContent()
		if err != nil || value != "sid=cookie-value|local-value" {
			t.Fatalf("state = %q %v", value, err)
		}
		b.Teardown(context.Background())
	}
}

func TestBrowserFillTypeEmitsKeyboardEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<input id="search" onkeydown="window.keys=(window.keys||'')+event.key">`)
	}))
	defer server.Close()
	b := &Browser{}
	if err := b.Init(context.Background(), nil); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer b.Teardown(context.Background())
	for _, action := range []*protocol.Action{
		{Type: "browser_navigate", Target: server.URL},
		{Type: "browser_fill", Selector: "#search", Value: "abc", Params: map[string]any{"input_mode": "type", "typing_delay_ms": 5}},
	} {
		result, err := b.Execute(context.Background(), action)
		if err != nil || !result.Success {
			t.Fatalf("action: %s %v", result.ErrorMessage, err)
		}
	}
	keys, err := b.currentPage().Evaluate("window.keys")
	if err != nil || keys != "Deleteabc" {
		t.Fatalf("keys = %v %v", keys, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.fillAction(ctx, &protocol.Action{Params: map[string]any{"input_mode": "type"}}, "#search", "overwrite"); err == nil {
		t.Fatal("cancelled typing reported success")
	}
	value, err := b.currentPage().Locator("#search").InputValue()
	if err != nil || value != "abc" {
		t.Fatalf("cancelled typing mutated the input: %q %v", value, err)
	}
}

func TestBrowserFillRejectsInvalidOptionsBeforeMutation(t *testing.T) {
	b := &Browser{}
	for _, params := range []map[string]any{
		{"input_mode": true}, {"input_mode": ""}, {"input_mode": "unknown"},
		{"input_mode": "type", "typing_delay_ms": -0.5},
		{"input_mode": "type", "typing_delay_ms": 0.5},
		{"input_mode": "type", "typing_delay_ms": 1001},
		{"input_mode": "type", "typing_delay_ms": math.NaN()},
		{"input_mode": "type", "typing_delay_ms": math.Inf(1)},
		{"typing_delay_ms": "50"},
	} {
		if err := b.fillAction(context.Background(), &protocol.Action{Params: params}, "#input", "new value"); err == nil {
			t.Fatalf("invalid options accepted: %v", params)
		}
	}
}

func TestBrowserWaitForElementTextRetriesAndStaysFrameScoped(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/frame" {
			fmt.Fprint(w, `<div id="status">loading</div><script>setTimeout(()=>document.querySelector('#status').textContent='Saved Alice',350)</script>`)
			return
		}
		fmt.Fprint(w, `<div id="status">Wrong frame</div><iframe id="child" src="/frame"></iframe>`)
	}))
	defer server.Close()
	b := &Browser{}
	if err := b.Init(context.Background(), nil); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer b.Teardown(context.Background())
	b.Execute(context.Background(), &protocol.Action{Type: "browser_navigate", Target: server.URL})
	action := &protocol.Action{Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "element_text", Value: "#status", TimeoutMs: 2000}, Params: map[string]any{"text": "Saved Alice"}, Metadata: map[string]any{"frame_selector": "#child"}}
	result, err := b.Execute(context.Background(), action)
	if err != nil || !result.Success {
		t.Fatalf("frame text: %v %s", err, result.ErrorMessage)
	}
	action.Metadata = nil
	action.WaitFor.TimeoutMs = 150
	result, err = b.Execute(context.Background(), action)
	if err != nil || result.Success {
		t.Fatalf("wrong frame should fail: %v success=%v", err, result.Success)
	}
}

func TestStorageStateNestedSecretsAndStructuralLimits(t *testing.T) {
	values, err := storageValueLeaves(`{"auth":{"token":"nested-secret"},"tokens":["other-secret",123]}`)
	if err != nil || len(values) != 3 {
		t.Fatalf("nested secrets: %v %v", values, err)
	}
	if _, err := storageValueLeaves(strings.Repeat("[", 40) + `"secret"` + strings.Repeat("]", 40)); err == nil {
		t.Fatal("deep storage JSON accepted")
	}
	if values, err := storageValueLeaves("plain-text"); err != nil || len(values) > 0 {
		t.Fatalf("plain value: %v %v", values, err)
	}
	for _, count := range []int{1024, 1025} {
		raw := "[" + strings.TrimSuffix(strings.Repeat(`"token",`, count), ",") + "]"
		_, err := storageValueLeaves(raw)
		if (err == nil) != (count == 1024) {
			t.Fatalf("%d leaves: %v", count, err)
		}
	}
}

func TestElementTextWaitRequiresVisibilityAndHonorsCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	b := &Browser{}
	if err := b.Init(context.Background(), nil); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer b.Teardown(context.Background())
	if err := b.currentPage().SetContent(`<div id="result" style="display:none">Saved</div>`); err != nil {
		t.Fatal(err)
	}
	action := &protocol.Action{Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "element_text", Value: "#result", TimeoutMs: 100}, Params: map[string]any{"text": "Saved"}}
	if err := b.runWait(context.Background(), action); err == nil {
		t.Fatal("hidden text satisfied readiness")
	}
	if _, err := b.currentPage().Evaluate(`setTimeout(() => document.querySelector('#result').style.display='block', 150)`); err != nil {
		t.Fatal(err)
	}
	action.WaitFor.TimeoutMs = 2000
	start := time.Now()
	if err := b.runWait(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("wait returned before the element became visible")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.runWait(ctx, action); err == nil {
		t.Fatal("cancelled wait reported success")
	}
}
