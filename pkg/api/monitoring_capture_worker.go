package api

import (
	"context"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// startMonitoring starts at most one worker per provider. Publication only reads
// their cached snapshots; neither network latency nor provider outages can hold
// the metrics/control loop. All mutable run state is snapshotted before dispatch.
func (a *runArtifacts) startMonitoring(test *ManagedTest) {
	if a == nil || test == nil {
		return
	}
	a.monitorMu.Lock()
	defer a.monitorMu.Unlock()
	if a.monitorStarted || a.monitorStopped {
		return
	}
	if a.runner != nil {
		a.runner.mu.RLock()
		defer a.runner.mu.RUnlock()
	}
	if test.ELKCapture == nil && test.DynatraceCapture == nil {
		return
	}
	parent := test.executionCtx
	if parent == nil || parent.Err() != nil || test.remoteFinalizing || isTerminalTestStatus(test.Status) {
		return
	}
	snapshot := &ManagedTest{ID: test.ID, CreatedAt: test.CreatedAt, StartedAt: cloneTimePtr(test.StartedAt),
		ELKCapture: cloneELKCaptureConfig(test.ELKCapture), DynatraceCapture: cloneDynatraceCaptureConfig(test.DynatraceCapture)}
	ctx, cancel := context.WithCancel(parent)
	a.monitorCancel = cancel
	a.monitorStarted = true
	start := func(interval time.Duration, poll func(context.Context, *ManagedTest, *LiveMetrics) map[string]float64) {
		a.monitorWorkers.Add(1)
		go func() {
			defer a.monitorWorkers.Done()
			for ctx.Err() == nil {
				// Also bound the entire poll, not just individual HTTP requests.
				pollCtx, stop := context.WithTimeout(ctx, 30*time.Second)
				poll(pollCtx, snapshot, &LiveMetrics{Timestamp: time.Now().UTC()})
				stop()
				timer := time.NewTimer(interval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	if snapshot.ELKCapture != nil {
		a.elkMu.Lock()
		a.elkStatus = &ELKCaptureStatus{ConnectionID: snapshot.ELKCapture.ConnectionID, ConnectionName: snapshot.ELKCapture.ConnectionName, Status: "pending"}
		a.elkMu.Unlock()
		start(10*time.Second, a.collectELKMetrics)
	}
	if snapshot.DynatraceCapture != nil {
		a.dynatraceMu.Lock()
		a.dynatraceStatus = &DynatraceCaptureStatus{ConnectionID: snapshot.DynatraceCapture.ConnectionID, ConnectionName: snapshot.DynatraceCapture.ConnectionName, Status: "pending"}
		a.dynatraceMu.Unlock()
		interval := time.Duration(snapshot.DynatraceCapture.PollIntervalSeconds) * time.Second
		if interval < 10*time.Second {
			interval = 15 * time.Second
		}
		start(interval, a.collectDynatraceMetrics)
	}
}

// Call before closing the archive writer or taking distributed evidence. The
// cancellation interrupts pending HTTP I/O; waiting prevents late archive writes.
func (a *runArtifacts) stopMonitoring() {
	if a == nil {
		return
	}
	a.monitorMu.Lock()
	a.monitorStopped = true
	if a.monitorCancel != nil {
		a.monitorCancel()
	}
	a.monitorMu.Unlock()
	a.monitorWorkers.Wait()
}

// Distributed metrics carry observed W3C correlations even though no local
// VUser result sink exists. Feed that bounded evidence into the same collector.
func (a *runArtifacts) observeMonitoringTraces(live *LiveMetrics) {
	if a == nil || live == nil || a.test == nil || (a.test.ELKCapture == nil && a.test.DynatraceCapture == nil) {
		return
	}
	sink := elkTraceEvidenceSink{artifacts: a}
	for _, tx := range live.Transactions {
		if tx != nil {
			for _, correlation := range tx.CorrelationIDs {
				_ = sink.RecordResult(&protocol.Result{CorrelationID: correlation})
			}
		}
	}
}

// Caller holds dynatraceMu. Reserve half the evidence for each capability.
// Logs are immutable; problems are upserted so resolution is reflected live and
// in the final archive without an unbounded stream of duplicate summaries.
func (a *runArtifacts) retainDynatraceEvidence(logs []DynatraceLogSample, problems []DynatraceProblemSummary) []DynatraceEvidenceRecord {
	if a.dynatraceEvidenceIDs == nil {
		a.dynatraceEvidenceIDs = make(map[string]struct{})
	}
	var fresh []DynatraceEvidenceRecord
	for _, sample := range logs {
		record := DynatraceEvidenceRecord{Type: "log_sample", Log: &sample}
		key := dynatraceEvidenceKey(record)
		if _, exists := a.dynatraceEvidenceIDs[key]; exists || len(a.dynatraceLogs) >= maxDynatraceEvidence/2 {
			continue
		}
		a.dynatraceEvidenceIDs[key] = struct{}{}
		a.dynatraceLogs = append(a.dynatraceLogs, sample)
		fresh = append(fresh, record)
	}
	for _, problem := range problems {
		key := dynatraceEvidenceKey(DynatraceEvidenceRecord{Problem: &problem})
		index := -1
		for i := range a.dynatraceProblems {
			if dynatraceEvidenceKey(DynatraceEvidenceRecord{Problem: &a.dynatraceProblems[i]}) == key {
				index = i
				break
			}
		}
		if index >= 0 {
			a.dynatraceProblems[index] = problem
		} else if len(a.dynatraceProblems) < maxDynatraceEvidence/2 {
			a.dynatraceProblems = append(a.dynatraceProblems, problem)
		}
	}
	return fresh
}
