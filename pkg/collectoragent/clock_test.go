package collectoragent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func clockHeaders(received, sent time.Time) http.Header {
	header := http.Header{}
	header.Set(ClockReceivedHeader, strconv.FormatInt(received.UnixNano(), 10))
	header.Set(ClockSentHeader, strconv.FormatInt(sent.UnixNano(), 10))
	return header
}

func TestCollectorClockMeasuresSkewFromFourTimestamps(t *testing.T) {
	sent := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		skew          time.Duration // collector minus controller
		forward, back time.Duration
	}{
		{2 * time.Second, 40 * time.Millisecond, 5 * time.Millisecond},
		{-2 * time.Second, 5 * time.Millisecond, 120 * time.Millisecond},
	} {
		var clock collectorClock
		controllerReceived := sent.Add(-tc.skew).Add(tc.forward)
		controllerSent := controllerReceived.Add(3 * time.Millisecond)
		received := controllerSent.Add(tc.skew).Add(tc.back)
		clock.observe(sent, received, clockHeaders(controllerReceived, controllerSent))
		timing := clock.timing(received)
		if timing.OffsetStatus != analysis.OffsetMeasured || timing.Validate() != nil {
			t.Fatalf("%+v: timing %+v", tc, timing)
		}
		offset := time.Duration(*timing.ClockOffsetMs * float64(time.Millisecond))
		uncertainty := time.Duration(*timing.ClockUncertaintyMs * float64(time.Millisecond))
		if uncertainty != (tc.forward+tc.back)/2 {
			t.Fatalf("%+v: uncertainty %s", tc, uncertainty)
		}
		if err := offset - tc.skew; err < -uncertainty || err > uncertainty {
			t.Fatalf("%+v: offset %s misses the skew by more than %s", tc, offset, uncertainty)
		}
		// A measurement older than its useful age is no longer claimed.
		if stale := clock.timing(received.Add(clockEstimateMaxAge + time.Second)); stale.OffsetStatus != analysis.OffsetUnknown {
			t.Fatalf("%+v: stale timing %+v", tc, stale)
		}
	}
	var unset collectorClock
	unset.observe(sent, sent.Add(time.Millisecond), http.Header{})
	if timing := unset.timing(sent); timing.OffsetStatus != analysis.OffsetUnknown || timing.ClockOffsetMs != nil {
		t.Fatalf("an older controller without clock headers produced %+v", timing)
	}
}

func TestHeartbeatMeasuresTheControllerClock(t *testing.T) {
	// The controller's clock runs two seconds behind this collector's.
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received := time.Now().Add(-2 * time.Second)
		w.Header().Set(ClockReceivedHeader, strconv.FormatInt(received.UnixNano(), 10))
		w.Header().Set(ClockSentHeader, strconv.FormatInt(time.Now().Add(-2*time.Second).UnixNano(), 10))
		w.WriteHeader(http.StatusOK)
	}))
	defer controller.Close()
	agent, err := New(Config{ControllerURL: controller.URL, CollectorID: "c", Token: "t", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.pushHeartbeat(context.Background(), heartbeat{Health: "healthy"}); err != nil {
		t.Fatal(err)
	}
	timing := agent.clock.timing(time.Now())
	if timing.OffsetStatus != analysis.OffsetMeasured {
		t.Fatalf("timing %+v", timing)
	}
	if offset, uncertainty := *timing.ClockOffsetMs, *timing.ClockUncertaintyMs; offset < 2000-uncertainty-5 || offset > 2000+uncertainty+5 {
		t.Fatalf("offset %vms ± %vms, want about +2000ms", offset, uncertainty)
	}
}

func TestRunPointsCarryTheCollectorClockOnlyWhenItTimedThem(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	sampleAt := time.Now().Add(-10 * time.Second).UnixMilli()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// One sample is timed by the scrape, one brings the target's own time.
		fmt.Fprintf(w, "# TYPE queue_depth gauge\nqueue_depth 4\n# TYPE lag_seconds gauge\nlag_seconds 2 %d\n", sampleAt)
	}))
	defer target.Close()
	var jobs []SignedRunJob
	var received []RunSampleBatch
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "heartbeat"):
			// The controller's clock runs two seconds behind the collector's.
			w.Header().Set(ClockReceivedHeader, strconv.FormatInt(time.Now().Add(-2*time.Second).UnixNano(), 10))
			w.Header().Set(ClockSentHeader, strconv.FormatInt(time.Now().Add(-2*time.Second).UnixNano(), 10))
			w.WriteHeader(200)
		case strings.HasSuffix(r.URL.Path, "/jobs"):
			json.NewEncoder(w).Encode(jobs)
		case strings.HasSuffix(r.URL.Path, "/samples"):
			var b RunSampleBatch
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				t.Error(err)
			}
			received = append(received, b)
			if len(received) == 1 {
				w.WriteHeader(503) // force a retry of the same batch
				return
			}
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer controller.Close()
	a, err := New(Config{ControllerURL: controller.URL, CollectorID: "c", Token: "token", ControllerPublicKey: pub, Packs: []string{"prometheus"}, PrometheusURL: target.URL})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	signed, _ := SignRunJob(RunJob{ID: "job", ProjectID: "p", RunID: "r", CollectorID: "c", SourceID: "s", SourceRevision: 1, IntervalSeconds: 1, Pack: "prometheus", Destination: target.URL, StartedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute)}, key)
	jobs = []SignedRunJob{signed}
	a.tick(context.Background())
	a.tick(context.Background())
	if len(received) != 2 {
		t.Fatalf("batches = %d, want a failed send and its retry", len(received))
	}
	byName := map[string]analysis.MetricPoint{}
	for _, p := range received[1].Points {
		byName[p.MetricName] = p
	}
	scraped, timed := byName["queue_depth"], byName["lag_seconds"]
	if scraped.Timing == nil || scraped.Timing.OffsetStatus != analysis.OffsetMeasured || *scraped.Timing.ClockOffsetMs < 1900 || *scraped.Timing.ClockOffsetMs > 2100 {
		t.Fatalf("scrape-timed point timing %+v", scraped.Timing)
	}
	if timed.MetricName == "" || timed.Timing != nil {
		t.Fatalf("a sample with its own timestamp claimed the collector clock: %+v", timed)
	}
	first, _ := json.Marshal(received[0].Points)
	second, _ := json.Marshal(received[1].Points)
	if string(first) != string(second) {
		t.Fatal("the retry restamped its timing")
	}
}
