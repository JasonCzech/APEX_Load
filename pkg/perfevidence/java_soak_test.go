package perfevidence

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
)

func idealJavaSoak(users int) *JavaSoakEvidence {
	s := &JavaSoakEvidence{Schedule: javaSoakSchedule, RequestedSeconds: 3600, WarmupSeconds: 40, ActiveSeconds: 3639.99, Complete: true, Iterations: int64(users) * 10, Transactions: int64(users) * 10, MemoryBytes: []float64{100, 100}}
	for _, l := range javaSoakLifetimes(users, 40*time.Second, time.Hour) {
		s.Users = append(s.Users, JavaSoakUser{StartSeconds: l.StartAfter.Seconds(), StopSeconds: l.StopAfter.Seconds(), InitializedSeconds: l.StartAfter.Seconds() + .01, ClosedSeconds: l.StopAfter.Seconds(), CleanupConfirmed: true})
	}
	return s
}

func TestJavaSoakScheduleAndChecks(t *testing.T) {
	s := idealJavaSoak(25)
	if !s.passed(25) {
		t.Fatal(s)
	}
	s.Users[0].CleanupConfirmed = false
	if s.passed(25) {
		t.Fatal("unconfirmed cleanup passed")
	}
	s.Users[0].CleanupConfirmed = true
	s.Users[1].InitializedSeconds = s.Users[1].StartSeconds + 3
	if s.passed(25) {
		t.Fatal("distorted ramp passed")
	}
	s = idealJavaSoak(25)
	s.Users[0].ClosedSeconds = s.Users[0].StopSeconds - 1
	if s.passed(25) {
		t.Fatal("early exit passed")
	}
	s = idealJavaSoak(25)
	s.DroppedResults = 1
	if s.passed(25) {
		t.Fatal("lost results passed")
	}
	s = idealJavaSoak(25)
	s.ResourceFailures = 1
	if s.passed(25) {
		t.Fatal("resource limit failure passed")
	}
}

func TestJavaSoakCollectorRealEngine(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer target.Close()
	script := &dsl.Script{ID: "soak", Name: "soak", Protocol: "http", Config: dsl.ScriptConfig{Pacing: dsl.PacingConfig{Type: "fixed_interval", IntervalMs: 20}}, Actions: []dsl.Action{{Name: "request", Type: "http_request", Method: "GET", Target: target.URL}}}
	cfg := JavaRunConfig{VUsers: 3, Warmup: 100 * time.Millisecond, Soak: 600 * time.Millisecond}
	read := func() (javaMemoryReading, error) { return javaMemoryReading{pss: 100, cgroup: 200}, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := collectJavaSoak(ctx, cfg, script, nil, read)
	if err != nil || !s.passed(3) || s.RequestedSeconds >= 3600 {
		t.Fatalf("accelerated collector fixture failed: %+v %v", s, err)
	}
	if len(s.Users) != 3 || s.Users[1].ClosedSeconds >= s.Users[0].ClosedSeconds {
		t.Fatal("permanent early closure was not observed", s.Users)
	}
}
