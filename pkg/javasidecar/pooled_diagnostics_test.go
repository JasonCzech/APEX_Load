package javasidecar

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func TestPooledHostDiagnosticsWireAndIdentity(t *testing.T) {
	want := javarunner.HostDiagnostics{Stdout: javarunner.DiagnosticCapture{Bytes: []byte{0xff, 0, 1}, Dropped: 7, Truncated: true}, Java: javarunner.DiagnosticCapture{Bytes: []byte("shared executor output")}}
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }, diagnostics: func(context.Context) (javarunner.HostDiagnostics, error) { return want, nil }}
	_, client, allocation, prepare := preparationControlFixture(t, h)
	if _, err := client.PooledControl(t.Context(), prepare); err != nil {
		t.Fatal(err)
	}
	q := controlRequest(allocation, "diagnostics", "diagnostics-1")
	// Allocation responses do not return the claim token; use the prepared
	// request's exact run token, as a real managed client does.
	q.RunToken = prepare.RunToken
	q.HostID = allocation.Placements[0].HostID
	r, err := client.PooledControl(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if r.Diagnostics == nil || string(r.Diagnostics.Stdout.Bytes) != string(want.Stdout.Bytes) || r.Diagnostics.Stdout.Dropped != 7 || string(r.Diagnostics.Java.Bytes) != string(want.Java.Bytes) {
		t.Fatalf("invalid snapshot: %+v", r.Diagnostics)
	}
	for _, mutate := range []func(*PooledControlResponse){
		func(r *PooledControlResponse) { r.HostID = "foreign" },
		func(r *PooledControlResponse) { r.SlotID = "slot" },
		func(r *PooledControlResponse) { r.Diagnostics = nil },
		func(r *PooledControlResponse) {
			r.Diagnostics = &javarunner.HostDiagnostics{Stdout: javarunner.DiagnosticCapture{Bytes: make([]byte, javarunner.HostDiagnosticCaptureBytes+1)}}
		},
	} {
		bad := r
		mutate(&bad)
		if validPooledControlResponse(q, bad) {
			t.Fatal("invalid diagnostics response accepted")
		}
	}
	q.HostID = "foreign"
	_, err = client.PooledControl(t.Context(), q)
	requireControlError(t, err, "pooled_host_state_invalid")
	q.HostID = allocation.Placements[0].HostID
	q.RunToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	_, err = client.PooledControl(t.Context(), q)
	requireControlError(t, err, "pooled_run_fenced")
}
