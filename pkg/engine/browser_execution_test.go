package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserExecutionDistinguishesRetriesAndInvocations(t *testing.T) {
	script := &dsl.Script{Protocol: "browser", Actions: []dsl.Action{{Name: "same", Type: "browser_click"}}}
	var got []*protocol.BrowserActionExecution
	spy := &replayTestProtocol{execute: func(a *protocol.Action) (*protocol.Result, error) {
		got = append(got, a.BrowserActionExecution)
		return &protocol.Result{Success: len(got) != 1, StartTime: time.Now()}, nil
	}}
	vu := &VUser{Script: script, Proto: spy, Variables: map[string]string{}}
	action := vu.resolveBrowserAuthoredAction(script.Actions[0], &script.Actions[0])
	cfg := dsl.ErrorHandlingConfig{MaxRetries: 1, RetryDelayMs: 1}
	for range 2 {
		if _, err := vu.executeWithRetry(context.Background(), action, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 3 || *got[0] != (protocol.BrowserActionExecution{Invocation: 1, Attempt: 1}) || *got[1] != (protocol.BrowserActionExecution{Invocation: 1, Attempt: 2}) || *got[2] != (protocol.BrowserActionExecution{Invocation: 2, Attempt: 1}) {
		t.Fatalf("bindings: %+v", got)
	}
	if action.BrowserActionExecution != nil {
		t.Fatal("caller action mutated")
	}
	vu.browserInvocation = 1<<53 - 1
	if _, err := vu.executeWithRetry(context.Background(), action, cfg); err != nil {
		t.Fatal(err)
	}
	if got[len(got)-1] != nil {
		t.Fatal("exhausted counter reused identity")
	}
}
