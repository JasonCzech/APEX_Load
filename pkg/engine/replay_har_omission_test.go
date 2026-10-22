package engine

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type replayOmissionProtocol struct {
	replayTestProtocol
	counts          map[string]int64
	tornDown, takes int
	panics          bool
}

func (p *replayOmissionProtocol) Teardown(context.Context) error { p.tornDown++; return nil }
func (p *replayOmissionProtocol) TakeBrowserHARCaptureOmissions() map[string]int64 {
	p.takes++
	if p.tornDown != 1 {
		panic("taken before teardown")
	}
	if p.panics {
		panic("private provider detail")
	}
	return p.counts
}

func TestReplayRetainsCaptureOmissionsAfterTeardown(t *testing.T) {
	for _, tc := range []struct {
		name           string
		counts, want   map[string]int64
		panics, failed bool
	}{
		{name: "success", counts: map[string]int64{protocol.BrowserHAROmittedCaptureFailed: 2}, want: map[string]int64{protocol.BrowserHAROmittedCaptureFailed: 2}},
		{name: "failed action", failed: true, counts: map[string]int64{protocol.BrowserHAROmittedOwnerUnretired: 1}, want: map[string]int64{protocol.BrowserHAROmittedOwnerUnretired: 1}},
		{name: "invalid reason", counts: map[string]int64{"private provider detail": 1}, want: map[string]int64{protocol.BrowserHAROmittedCaptureStatus: 1}},
		{name: "panic", panics: true, want: map[string]int64{protocol.BrowserHAROmittedCaptureStatus: 1}},
		{name: "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &replayOmissionProtocol{counts: tc.counts, panics: tc.panics}
			p.execute = func(*protocol.Action) (*protocol.Result, error) { return &protocol.Result{Success: !tc.failed}, nil }
			name := "omission-" + strings.ReplaceAll(t.Name(), "/", "-")
			protocol.Register(name, func() protocol.Protocol { return p })
			report, err := ReplayScript(context.Background(), &dsl.Script{Name: "omissions", Protocol: name, Actions: []dsl.Action{{Name: "step", Type: "noop"}}}, ReplayOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if p.tornDown != 1 || p.takes != 1 || report.Success == tc.failed || !reflect.DeepEqual(report.NativeHARCaptureOmissions, tc.want) {
				t.Fatalf("report=%+v teardown=%d takes=%d", report, p.tornDown, p.takes)
			}
			raw, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "private provider detail") {
				t.Fatal("provider detail leaked")
			}
			if len(tc.want) == 0 && strings.Contains(string(raw), "native_har_capture_omissions") {
				t.Fatal("empty evidence serialized")
			}
			copy := cloneReplayReport(report)
			if len(tc.want) > 0 {
				for key := range copy.NativeHARCaptureOmissions {
					copy.NativeHARCaptureOmissions[key] = 99
				}
				if !reflect.DeepEqual(report.NativeHARCaptureOmissions, tc.want) {
					t.Fatal("clone aliases omissions")
				}
			}
		})
	}
}
