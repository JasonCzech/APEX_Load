package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestNativeAnalysisWindowReportsCanonicalBoundsWithoutChangingStart(t *testing.T) {
	for _, nanos := range []int64{1, 999, 1001, -1, -999, -1001} {
		from := time.Unix(0, nanos).In(time.FixedZone("source", 3600))
		to := from.Add(2 * time.Second)
		window := nativeAnalysisTimeWindow(from, to)
		if !window.RequestedFrom.Equal(from) || !window.RequestedTo.Equal(to) || window.From.Nanosecond()%1000 != 0 || window.To.Nanosecond()%1000 != 0 || from.Sub(window.From) < 0 || from.Sub(window.From) >= time.Microsecond || window.To.Sub(window.From) != 2*time.Second || window.Resolution != "microsecond" {
			t.Fatal("incorrect explicit window", window)
		}
		q := nativeEvidenceQuery("archive")
		q.OverlaySelection = &storage.NativeOverlaySelection{DatasetID: "dataset", Metric: "latency", Aggregation: "avg"}
		out := nativeEvidenceEnvelope(from, q)
		if !out.Start.Equal(from) || out.TimeWindow == nil || !out.From.Equal(window.From) || !out.TimeWindow.RequestedFrom.Equal(from) || len(out.Warnings) == 0 {
			t.Fatal("dataset overlay changed actual execution start or concealed precision", out)
		}
		q.OverlaySelection = nil
		ordinary := nativeEvidenceEnvelope(from, q)
		if ordinary.TimeWindow != nil || !ordinary.From.Equal(from) {
			t.Fatal("unrelated run query changed window", ordinary)
		}
	}
}

func TestNativeArtifactSelectionRefusesExpiredAndCancelledContextsEvenWithoutRecorder(t *testing.T) {
	in, q := analysisPartitionPlanningFixture(t)
	q.Host = "absent"
	parts, size, err := selectAnalysisDatasetParts(t.Context(), in, q)
	if err != nil || len(parts) != 0 || size != 0 {
		t.Fatal(parts, size, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{cancelled, nativeQueryDetachedDeadline{Context: t.Context(), deadline: time.Now().Add(-time.Second)}, withAnalysisArtifactSelections(cancelled)} {
		if err := recordAnalysisArtifactSelection(ctx, in, parts, size); err == nil {
			t.Fatal("expired empty selection reported success")
		}
		if len(completedAnalysisArtifactSelections(ctx)) != 0 {
			t.Fatal("cancelled geometry recorded")
		}
	}
}

func TestNativeDatasetSeriesHandlerRefusesLateInitialMetadataReply(t *testing.T) {
	var owner *nativeQueryMetadataBarrier
	server, _, in := nativeQueryAuthorityFixture(t, func(store *storage.SQLiteStore) storage.Store {
		owner = &nativeQueryMetadataBarrier{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{}), blockDataset: true}
		return sqliteOverlay(owner)
	})
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", in.ProjectID)
	owner.armed.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"metric": "value", "from": time.Now().UTC(), "to": time.Now().UTC().Add(time.Second), "bucket_seconds": 1, "aggregation": "avg"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/analysis/datasets/"+in.ID+"/series", strings.NewReader(string(body))).WithContext(ctx)
	req.SetPathValue("id", in.ID)
	bindAuthorizedProjectToRequest(req, in.ProjectID)
	reply := httptest.NewRecorder()
	done := make(chan struct{})
	var release sync.Once
	t.Cleanup(func() {
		release.Do(func() { close(owner.release) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("owned handler not joined")
		}
	})
	go func() { server.handleAnalysisDatasetSeries(reply, req); close(done) }()
	select {
	case <-owner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("metadata barrier not reached")
	}
	<-ctx.Done()
	release.Do(func() { close(owner.release) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("late metadata did not join")
	}
	if reply.Code != http.StatusConflict || !strings.Contains(reply.Body.String(), "expired") {
		t.Fatal("late metadata admitted to native work", reply.Code, reply.Body.String())
	}
}

func TestNativeOverlayWriterRetainsSnapshotThroughLateAnnotationAndMetadataReads(t *testing.T) {
	for _, mutation := range []string{"revision", "delete", "reassociate", "deadline"} {
		t.Run(mutation, func(t *testing.T) {
			var owner *nativeQueryMetadataBarrier
			server, store, in := nativeQueryAuthorityFixture(t, func(store *storage.SQLiteStore) storage.Store {
				owner = &nativeQueryMetadataBarrier{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{}), blockDataset: true}
				return sqliteOverlay(owner)
			})
			if err := store.SaveTest(&storage.TestRecord{ID: "other", Name: "other", ProjectID: in.ProjectID, Status: "COMPLETE"}); err != nil {
				t.Fatal(err)
			}
			ctx := withAnalysisArtifactSelections(t.Context())
			// This fixture exercises final authority only; geometry is not
			// claimed as a physical native execution or pruning acceptance.
			if err := recordAnalysisArtifactSelection(ctx, in, nil, 0); err != nil {
				t.Fatal(err)
			}
			if mutation == "deadline" {
				ctx = nativeQueryDetachedDeadline{Context: ctx, deadline: time.Now().Add(100 * time.Millisecond)}
			}
			snapshots := completedAnalysisQuerySnapshots(ctx)
			snapshots[0].Revision++
			if completedAnalysisQuerySnapshots(ctx)[0].Revision != in.Revision {
				t.Fatal("authority snapshot escapes mutable")
			}
			owner.armed.Store(true)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/run/native-series", nil).WithContext(ctx)
			bindAuthorizedProjectToRequest(req, in.ProjectID)
			reply := httptest.NewRecorder()
			done := make(chan bool, 1)
			var release sync.Once
			joined := false
			t.Cleanup(func() {
				release.Do(func() { close(owner.release) })
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("overlay writer not joined")
					}
				}
			})
			go func() {
				done <- server.writeNativeEvidenceSeries(reply, req, ctx, in.ProjectID, NativeEvidenceResponse{Mode: "archive"})
			}()
			select {
			case <-owner.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("final overlay metadata barrier not reached")
			}
			if mutation == "deadline" {
				deadline, _ := ctx.Deadline()
				timer := time.NewTimer(time.Until(deadline) + time.Millisecond)
				<-timer.C
			} else {
				current := *in
				switch mutation {
				case "revision":
					current.Name = "changed"
				case "delete":
					current.State = "deleting"
				case "reassociate":
					current.RunID = "other"
				}
				if _, err := store.SaveAnalysisImport(current, current.Revision); err != nil {
					t.Fatal(err)
				}
			}
			release.Do(func() { close(owner.release) })
			select {
			case accepted := <-done:
				joined = true
				if accepted || reply.Code != http.StatusConflict {
					t.Fatal("revoked overlay served", reply.Code, reply.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("overlay metadata read not joined")
			}
		})
	}
}

func TestNativeOverlayMicrosecondWindowPreservesWholePublicationRateSafety(t *testing.T) {
	start := time.Unix(1700000000, 1).UTC()
	q := nativeEvidenceQuery("live")
	q.LiveBucketMode = "merged"
	q.BucketSeconds = 1
	q.EndSeconds = 3
	q.Metric = "observation_rate"
	q.Aggregation = "rate"
	q.OverlaySelection = &storage.NativeOverlaySelection{DatasetID: "external", Metric: "gauge", Aggregation: "avg"}
	publication := &LiveMetrics{Timestamp: start.Add(time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Version: 2, WindowSeconds: 1, Dimensions: map[string]map[string]storage.NativeIntervalMetric{"action": {"checkout": nativeBucketMetric(10, 20)}}}}
	out, err := buildNativeLiveEvidence(t.Context(), start, []*LiveMetrics{publication}, q)
	if err != nil {
		t.Fatal(err)
	}
	if out.TimeWindow == nil || !out.Start.Equal(start) || len(out.Series) != 1 || out.Series[0].Points[1].Value != nil || out.Series[0].Points[1].Coverage == nil || *out.Series[0].Points[1].Coverage >= 1 {
		t.Fatal("boundary-straddling whole publication falsely qualified as a rate", out)
	}
	q.Metric, q.Aggregation = "response_time", "avg"
	distribution, err := buildNativeLiveEvidence(t.Context(), start, []*LiveMetrics{publication}, q)
	if err != nil || len(distribution.Series) != 1 || distribution.Series[0].Points[1].Count != 2 || distribution.Series[0].Points[1].Value == nil {
		t.Fatal("whole completion cohort lost its qualified distribution", distribution, err)
	}
	if !publication.Timestamp.Equal(start.Add(time.Second)) || publication.NativeEvidence.WindowSeconds != 1 {
		t.Fatal("source interval rounded or mutated")
	}
}
