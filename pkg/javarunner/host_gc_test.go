package javarunner

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestGCPauseWireBoundsAndIdentity(t *testing.T) {
	valid := map[string]string{"gc_pause_status": "available", "gc_pause_events": "1,1000,25\n2,1100,0\n", "gc_pause_evicted": "0"}
	s, err := parseGCPauses(valid)
	if err != nil || len(s.Events) != 2 || s.Events[0].DurationNano != 25 {
		t.Fatal(s, err)
	}
	if s, err := parseGCPauses(nil); err != nil || s != nil {
		t.Fatal("old pilot was not recognized")
	}
	for _, change := range []map[string]string{
		{"gc_pause_status": ""}, {"gc_pause_status": "unsupported"}, {"gc_pause_evicted": "-1"},
		{"gc_pause_events": "2,1000,25\n"}, {"gc_pause_events": "1,0,1\n"},
		{"gc_pause_events": "1,1000,-1\n"}, {"gc_pause_events": "1,1000,25"},
		{"gc_pause_events": fmt.Sprintf("1,%d,1\n", int64(math.MaxInt64))},
		{"gc_pause_events": strings.Repeat("x", 8193)}, {"gc_pause_evicted": "1"},
	} {
		fields := map[string]string{}
		for k, v := range valid {
			fields[k] = v
		}
		for k, v := range change {
			fields[k] = v
		}
		if _, err := parseGCPauses(fields); err == nil {
			t.Fatal("invalid GC record accepted", change)
		}
	}
	delete(valid, "gc_pause_evicted")
	if _, err := parseGCPauses(valid); err == nil {
		t.Fatal("partial GC contract")
	}
	var body strings.Builder
	for i := 2; i <= 129; i++ {
		fmt.Fprintf(&body, "%d,1000,1\n", i)
	}
	s, err = parseGCPauses(map[string]string{"gc_pause_status": "available", "gc_pause_events": body.String(), "gc_pause_evicted": "1"})
	if err != nil || len(s.Events) != 128 || s.Events[0].Sequence != 2 {
		t.Fatal(s, err)
	}
	if ValidHostDiagnostics(HostDiagnostics{GCPauses: &GCPauseSnapshot{Status: "available", Events: []GCPauseEvent{{Sequence: 2, StartUnixNano: 1}}}}) {
		t.Fatal("invalid diagnostic GC snapshot")
	}
}
