package appium

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestAppiumWaitsForTextWithinOneDeadline(t *testing.T) {
	for _, tc := range []struct {
		name      string
		readyAt   int32
		staleAt   int32
		timeoutMS int
		success   bool
	}{
		{name: "eventual text", readyAt: 2, timeoutMS: 1500, success: true},
		{name: "rerender before text", readyAt: 4, staleAt: 2, timeoutMS: 1500, success: true},
		{name: "text remains different", readyAt: 100, timeoutMS: 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var value any
				switch r.URL.Path {
				case "/session/s1/element":
					value = map[string]string{webdriverElementKey: "e1"}
				case "/session/s1/element/e1/text":
					read := reads.Add(1)
					value = "Loading"
					if read == tc.staleAt {
						w.WriteHeader(http.StatusNotFound)
						value = map[string]string{"error": "stale element reference"}
					} else if read >= tc.readyAt {
						value = "Ready"
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
			}))
			defer server.Close()
			driver, err := newWebDriver(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			device := &Appium{driver: driver, session: "s1", elementTimeout: time.Second}
			result, err := device.Execute(context.Background(), &protocol.Action{Type: "appium_wait", Target: "Status", Params: map[string]any{"text": "Ready", "timeout_ms": tc.timeoutMS}})
			if err != nil || result.Success != tc.success {
				t.Fatalf("reads=%d, result=%+v, err=%v", reads.Load(), result, err)
			}
			if tc.success && (reads.Load() < tc.readyAt || result.ResponseBody != "Ready") {
				t.Fatalf("wait ended before expected text: reads=%d, result=%+v", reads.Load(), result)
			}
			if !tc.success && (result.Duration < 60*time.Millisecond || !strings.Contains(result.ErrorMessage, `shows "Loading", not "Ready"`)) {
				t.Fatalf("wait did not retain the mismatch until timeout: %+v", result)
			}
		})
	}
}

func TestAppiumWaitTextHonorsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := any(map[string]string{webdriverElementKey: "e1"})
		if strings.HasSuffix(r.URL.Path, "/text") {
			value = "Loading"
			cancel()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
	}))
	defer server.Close()
	driver, err := newWebDriver(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	device := &Appium{driver: driver, session: "s1", elementTimeout: time.Minute}
	result, err := device.Execute(ctx, &protocol.Action{Type: "appium_wait", Target: "Status", Params: map[string]any{"text": "Ready"}})
	if err != nil || result.Success || !strings.Contains(result.ErrorMessage, context.Canceled.Error()) || result.Duration > time.Second {
		t.Fatalf("cancellation did not end the text wait: result=%+v err=%v", result, err)
	}
}
