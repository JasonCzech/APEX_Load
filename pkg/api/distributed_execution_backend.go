package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"time"

	"github.com/jasonczech/stormrunner/pkg/distributed/generatorrpc"
	"github.com/jasonczech/stormrunner/pkg/engine"
)

type generatorDial func(ctx context.Context, target string) (generatorClient, error)

type generatorClient interface {
	DistributeScript(ctx context.Context, script generatorrpc.Script) (generatorrpc.ScriptResult, error)
	AssignWork(ctx context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error)
	AdjustVUsers(ctx context.Context, adjustment generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error)
	AdjustDuration(ctx context.Context, adjustment generatorrpc.DurationAdjustment) (generatorrpc.DurationResult, error)
	OpenMetricsStream(ctx context.Context) (*generatorrpc.MetricsStream, error)
	StopTest(ctx context.Context, testID string, graceful bool) (generatorrpc.StopResult, error)
	Close() error
}

type distributedExecutionBackend struct {
	targets []string
	timeout time.Duration
	dial    generatorDial
}

func newDistributedExecutionBackend(targets []string, timeout time.Duration, dial generatorDial) *distributedExecutionBackend {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if dial == nil {
		dial = func(ctx context.Context, target string) (generatorClient, error) {
			return generatorrpc.Dial(ctx, target)
		}
	}

	clonedTargets := append([]string(nil), targets...)
	return &distributedExecutionBackend{
		targets: clonedTargets,
		timeout: timeout,
		dial:    dial,
	}
}

func (b *distributedExecutionBackend) Run(tr *TestRunner, test *ManagedTest) {
	if test.ScriptShakeout {
		if err := tr.runScriptShakeout(test); err != nil {
			tr.failTestBeforeRun(test, err.Error())
			return
		}
	}
	if len(b.targets) == 0 {
		tr.failTestBeforeRun(test, "no generator targets configured")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	client, err := b.dial(ctx, b.targets[0])
	if err != nil {
		tr.failTestBeforeRun(test, fmt.Sprintf("connect generator: %v", err))
		return
	}

	distributedScripts := map[string]struct{}{}
	for _, group := range test.Groups {
		if group.script == nil {
			tr.failTestBeforeRun(test, fmt.Sprintf("group %q has no compiled script", group.Name))
			return
		}

		scriptContent, contentHash, err := marshalDistributedScript(group.script)
		if err != nil {
			tr.failTestBeforeRun(test, fmt.Sprintf("serialize script %q: %v", group.ScriptName, err))
			return
		}

		if _, ok := distributedScripts[group.ScriptID]; !ok {
			if _, err := client.DistributeScript(ctx, generatorrpc.Script{
				ID:          group.ScriptID,
				Content:     scriptContent,
				ContentHash: contentHash,
			}); err != nil {
				tr.failTestBeforeRun(test, fmt.Sprintf("distribute script %q: %v", group.ScriptName, err))
				return
			}
			distributedScripts[group.ScriptID] = struct{}{}
		}

		assignment, err := client.AssignWork(ctx, generatorrpc.Assignment{
			TestID:        test.ID,
			GroupName:     group.Name,
			ScriptID:      group.ScriptID,
			VUserCount:    int32(group.VUsers),
			RampUpSeconds: int32(group.rampUpDur / time.Second),
		})
		if err != nil {
			tr.failTestBeforeRun(test, fmt.Sprintf("assign work for group %q: %v", group.Name, err))
			return
		}
		if !assignment.Accepted {
			tr.failTestBeforeRun(test, fmt.Sprintf("generator rejected group %q: %s", group.Name, assignment.ErrorMessage))
			return
		}
	}

	stream, err := client.OpenMetricsStream(context.Background())
	if err != nil {
		_ = client.Close()
		tr.failTestBeforeRun(test, fmt.Sprintf("open generator metrics stream: %v", err))
		return
	}

	b.startDistributedTestRun(tr, test, client, stream)
}

func (b *distributedExecutionBackend) StopTest(tr *TestRunner, test *ManagedTest) error {
	client, err := b.clientForTest(test)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	stop, err := client.StopTest(ctx, test.ID, true)
	if err != nil {
		return fmt.Errorf("stop remote test: %w", err)
	}
	if !stop.Acknowledged {
		return fmt.Errorf("generator did not acknowledge stop for %s", test.ID)
	}
	b.closeRemoteResources(test)
	return nil
}

func (b *distributedExecutionBackend) ScaleVUsers(tr *TestRunner, test *ManagedTest, groupName string, targetVUsers int) error {
	groupExists := false
	for _, group := range test.Groups {
		if group.Name == groupName {
			groupExists = true
			break
		}
	}
	if !groupExists {
		return fmt.Errorf("test group not found: %s", groupName)
	}
	client, err := b.clientForTest(test)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
		TestID:       test.ID,
		GroupName:    groupName,
		TargetVUsers: int32(targetVUsers),
	})
	if err != nil {
		return fmt.Errorf("adjust remote vusers: %w", err)
	}
	if !adjustment.Accepted {
		return fmt.Errorf("generator did not accept vuser adjustment for %s", test.ID)
	}
	return nil
}

func (b *distributedExecutionBackend) AdjustDuration(tr *TestRunner, test *ManagedTest, newDuration time.Duration) error {
	client, err := b.clientForTest(test)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	result, err := client.AdjustDuration(ctx, generatorrpc.DurationAdjustment{
		TestID:                test.ID,
		TargetDurationSeconds: int64(newDuration / time.Second),
	})
	if err != nil {
		return fmt.Errorf("adjust remote duration: %w", err)
	}
	if !result.Accepted {
		return fmt.Errorf("generator did not accept duration adjustment for %s", test.ID)
	}
	return nil
}

func (b *distributedExecutionBackend) PauseTest(tr *TestRunner, test *ManagedTest) error {
	if test.remoteMetricsStream == nil {
		return fmt.Errorf("distributed metrics stream is not initialized")
	}
	if err := test.remoteMetricsStream.SendPause(generatorrpc.PauseControl{TestID: test.ID, Pause: true}); err != nil {
		return fmt.Errorf("send remote pause control: %w", err)
	}
	return nil
}

func (b *distributedExecutionBackend) ResumeTest(tr *TestRunner, test *ManagedTest) error {
	if test.remoteMetricsStream == nil {
		return fmt.Errorf("distributed metrics stream is not initialized")
	}
	if err := test.remoteMetricsStream.SendPause(generatorrpc.PauseControl{TestID: test.ID, Pause: false}); err != nil {
		return fmt.Errorf("send remote pause control: %w", err)
	}
	return nil
}

func marshalDistributedScript(script any) ([]byte, string, error) {
	content, err := json.Marshal(script)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(content)
	return content, hex.EncodeToString(hash[:]), nil
}

func (b *distributedExecutionBackend) startDistributedTestRun(tr *TestRunner, test *ManagedTest, client generatorClient, stream *generatorrpc.MetricsStream) {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()

	tr.mu.Lock()
	test.cancel = cancel
	test.multiEngine = nil
	test.executionBackend = b
	test.remoteClient = client
	test.remoteMetricsStream = stream
	test.remoteTarget = ""
	if len(b.targets) > 0 {
		test.remoteTarget = b.targets[0]
	}
	test.StartedAt = &now
	test.EndedAt = nil
	test.Status = "RUNNING"
	tr.mu.Unlock()

	tr.broadcastEvent("test_started", test)
	go b.consumeMetrics(ctx, tr, test, stream)
}

func (b *distributedExecutionBackend) clientForTest(test *ManagedTest) (generatorClient, error) {
	if test.remoteClient != nil {
		return test.remoteClient, nil
	}
	if test.remoteTarget == "" {
		return nil, fmt.Errorf("distributed generator target is not recorded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	client, err := b.dial(ctx, test.remoteTarget)
	if err != nil {
		return nil, fmt.Errorf("connect generator: %w", err)
	}
	return client, nil
}

func (b *distributedExecutionBackend) closeRemoteResources(test *ManagedTest) {
	if test.remoteMetricsStream != nil {
		_ = test.remoteMetricsStream.CloseSend()
		test.remoteMetricsStream = nil
	}
	if test.remoteClient != nil {
		_ = test.remoteClient.Close()
		test.remoteClient = nil
	}
}

func (b *distributedExecutionBackend) consumeMetrics(ctx context.Context, tr *TestRunner, test *ManagedTest, stream *generatorrpc.MetricsStream) {
	for {
		batch, err := stream.Receive()
		if err != nil {
			if ctx.Err() != nil || err == io.EOF {
				return
			}
			slog.Warn("distributed metrics stream stopped", "test_id", test.ID, "error", err)
			return
		}
		if batch.TestID != test.ID {
			continue
		}
		tr.publishLiveMetrics(test, buildLiveMetricsFromDistributedBatch(test, batch), nil)
	}
}

func buildLiveMetricsFromDistributedBatch(test *ManagedTest, batch generatorrpc.MetricsBatch) *LiveMetrics {
	timestamp := time.UnixMilli(batch.TimestampUnixMs)
	if batch.TimestampUnixMs == 0 {
		timestamp = time.Now()
	}

	live := &LiveMetrics{
		Timestamp:     timestamp,
		ActiveVUsers:  int64(batch.Stats.ActiveVUsers),
		Transactions:  make(map[string]*TxMetrics),
		GroupMetrics:  make(map[string]*GroupLiveMetrics),
		Resources: &engine.SystemResources{
			CPUProcessPct: batch.Stats.CPUPercent,
			MemProcessMb:  float64(batch.Stats.MemoryBytes) / (1024 * 1024),
		},
	}

	durationsByName := make(map[string][]float64)
	for _, tx := range batch.Transactions {
		live.TotalTx++
		if !tx.Success {
			live.TotalErrors++
		}
		live.AvgResponseMs += tx.DurationMs
		durationsByName[tx.Name] = append(durationsByName[tx.Name], tx.DurationMs)
		metrics := live.Transactions[tx.Name]
		if metrics == nil {
			metrics = &TxMetrics{Name: tx.Name}
			live.Transactions[tx.Name] = metrics
		}
		metrics.Count++
		if !tx.Success {
			metrics.Errors++
		}
		metrics.AvgMs += tx.DurationMs
	}

	if live.TotalTx > 0 {
		live.ErrorPct = float64(live.TotalErrors) / float64(live.TotalTx) * 100
		live.AvgResponseMs /= float64(live.TotalTx)
	}

	names := make([]string, 0, len(durationsByName))
	allDurations := make([]float64, 0, len(batch.Transactions))
	for name, durations := range durationsByName {
		names = append(names, name)
		sort.Float64s(durations)
		allDurations = append(allDurations, durations...)
		metrics := live.Transactions[name]
		metrics.AvgMs /= float64(metrics.Count)
		metrics.P50Ms = percentileValue(durations, 0.50)
		metrics.P90Ms = percentileValue(durations, 0.90)
		metrics.P99Ms = percentileValue(durations, 0.99)
	}
	sort.Strings(names)
	if len(allDurations) > 0 {
		sort.Float64s(allDurations)
		live.P50Ms = percentileValue(allDurations, 0.50)
		live.P90Ms = percentileValue(allDurations, 0.90)
		live.P95Ms = percentileValue(allDurations, 0.95)
		live.P99Ms = percentileValue(allDurations, 0.99)
	}

	if len(batch.Groups) > 0 {
		for _, group := range batch.Groups {
			live.GroupMetrics[group.Name] = &GroupLiveMetrics{
				Name:          group.Name,
				VUsers:        int(group.ActiveVUsers),
				Transactions:  0,
				Errors:        0,
				TPS:           0,
				AvgResponseMs: 0,
				P50Ms:         0,
				P90Ms:         0,
				P99Ms:         0,
			}
		}
	} else if len(test.Groups) == 1 {
		groupName := test.Groups[0].Name
		live.GroupMetrics[groupName] = &GroupLiveMetrics{
			Name:          groupName,
			VUsers:        int(batch.Stats.ActiveVUsers),
			Transactions:  live.TotalTx,
			Errors:        live.TotalErrors,
			TPS:           live.TPS,
			AvgResponseMs: live.AvgResponseMs,
			P50Ms:         live.P50Ms,
			P90Ms:         live.P90Ms,
			P99Ms:         live.P99Ms,
		}
	}

	return live
}

func percentileValue(sorted []float64, percentile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if percentile <= 0 {
		return sorted[0]
	}
	if percentile >= 1 {
		return sorted[len(sorted)-1]
	}
	index := int(float64(len(sorted)-1) * percentile)
	return sorted[index]
}