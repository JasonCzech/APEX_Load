package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserExecutionOwnedAndBounded(t *testing.T) {
	for _, e := range []*BrowserActionExecution{nil, {}, {Invocation: 1, Attempt: 0}, {Invocation: 1, Attempt: 12}, {Invocation: 1 << 53, Attempt: 1}} {
		if e.Valid() || e.Clone() != nil {
			t.Fatal("invalid accepted")
		}
	}
	e := &BrowserActionExecution{Invocation: 1, Attempt: 11}
	c := e.Clone()
	c.Attempt = 1
	if e.Attempt != 11 {
		t.Fatal("clone alias")
	}
	a := Action{BrowserActionExecution: e}
	raw, err := json.Marshal(a)
	if err != nil || strings.Contains(string(raw), "invocation") {
		t.Fatalf("binding exported %s %v", raw, err)
	}
	if err := json.Unmarshal([]byte(`{"BrowserActionExecution":{"invocation":7,"attempt":1}}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.BrowserActionExecution.Invocation != 1 {
		t.Fatal("binding injected")
	}
}
