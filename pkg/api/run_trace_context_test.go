package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestNormalizeRunTraceContext(t *testing.T) {
	tests := []struct {
		name    string
		in      *RunTraceContextSettings
		want    *RunTraceContextSettings
		wantErr string
	}{
		{name: "absent", in: nil},
		{name: "nothing enabled", in: &RunTraceContextSettings{Origins: []string{"https://a.test"}}},
		{name: "enabled without origins", in: &RunTraceContextSettings{Enabled: true}, wantErr: "at least one approved origin"},
		{name: "origin with a path", in: &RunTraceContextSettings{Enabled: true, Origins: []string{"https://a.test/api"}}, wantErr: "origin"},
		{name: "credentials in origin", in: &RunTraceContextSettings{TransactionMarker: true, Origins: []string{"https://user:pw@a.test"}}, wantErr: "origin"},
		{
			name: "canonical form",
			in:   &RunTraceContextSettings{Enabled: true, TransactionMarker: true, Origins: []string{" https://b.test/ ", "https://a.test", "https://b.test", ""}},
			want: &RunTraceContextSettings{Enabled: true, TransactionMarker: true, Origins: []string{"https://a.test", "https://b.test"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeRunTraceContext(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalized = %#v, %v; want %#v", got, err, tt.want)
			}
		})
	}
}

func TestCombineRunCorrelation(t *testing.T) {
	trace := &RunTraceContextSettings{Enabled: true, TransactionMarker: true, Origins: []string{"https://shop.test"}}
	tests := []struct {
		name      string
		runHeader []string
		trace     *RunTraceContextSettings
		want      runCorrelation
		wantV2    bool
	}{
		{name: "nothing", want: runCorrelation{TestName: "Peak"}},
		{
			// Older generators keep receiving the carrier-free form.
			name: "run header only", runHeader: []string{"https://appd.test"},
			want: runCorrelation{Origins: []string{"https://appd.test"}, TestName: "Peak"},
		},
		{
			name: "trace context only", trace: trace,
			want:   runCorrelation{Origins: []string{"https://shop.test"}, Carriers: []string{engine.CorrelationCarrierTraceparent, engine.CorrelationCarrierTransactionMarker}, TestName: "Peak"},
			wantV2: true,
		},
		{
			name: "both share origins", runHeader: []string{"https://appd.test"}, trace: trace,
			want:   runCorrelation{Origins: []string{"https://appd.test", "https://shop.test"}, Carriers: []string{engine.CorrelationCarrierRunMarker, engine.CorrelationCarrierTraceparent, engine.CorrelationCarrierTransactionMarker}, TestName: "Peak"},
			wantV2: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := combineRunCorrelation(tt.runHeader, tt.trace, " Peak ")
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("correlation = %#v, want %#v", got, tt.want)
			}
			if got.requiresPolicyV2() != tt.wantV2 {
				t.Fatalf("requiresPolicyV2 = %v, want %v", got.requiresPolicyV2(), tt.wantV2)
			}
		})
	}
}

func TestRunTraceContextPersistsWithTheRun(t *testing.T) {
	settings := &RunTraceContextSettings{Enabled: true, Origins: []string{"https://shop.test"}}
	record := buildManagedTestRecord(&ManagedTest{ID: "trace-run", ProjectID: "p", Name: "Peak", TraceContext: settings})
	// Storage round-trips the blob as generic JSON.
	encoded, err := json.Marshal(record.TraceContext)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		t.Fatal(err)
	}
	record.TraceContext = generic
	restored := managedTestFromRecord(record)
	if !reflect.DeepEqual(restored.TraceContext, settings) {
		t.Fatalf("restored trace context = %#v, want %#v", restored.TraceContext, settings)
	}
	if managedTestFromRecord(buildManagedTestRecord(&ManagedTest{ID: "plain", ProjectID: "p"})).TraceContext != nil {
		t.Fatal("a run without trace context gained one through persistence")
	}
	// A stored value that no longer validates is dropped, never guessed.
	if decodeStoredRunTraceContext(map[string]any{"enabled": true}) != nil {
		t.Fatal("an origin-less stored setting was accepted")
	}
}
