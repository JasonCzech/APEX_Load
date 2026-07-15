package engine

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestEngineConstructorsInitializeDataProviders(t *testing.T) {
	source := dsl.DataSource{
		Name:           "users",
		Type:           "csv",
		InlineData:     "name\nalice\nbob\n",
		FirstRowHeader: true,
		Strategy:       "sequential",
	}
	script := &dsl.Script{Name: "data", Protocol: "stub", DataSources: []dsl.DataSource{source}}

	engine, err := New(Config{Script: script, VUsers: 1, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if engine.data == nil || !engine.data.HasSources() {
		t.Fatal("New did not initialize the script data provider")
	}

	multi, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups: []GroupConfig{
			{Name: "one", Script: script, VUsers: 1},
			{Name: "two", Script: script, VUsers: 1},
		},
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	if multi.groups[0].dataProvider == nil || multi.groups[1].dataProvider == nil {
		t.Fatal("NewMultiGroup did not initialize every group data provider")
	}
	if multi.groups[0].dataProvider == multi.groups[1].dataProvider {
		t.Fatal("groups unexpectedly share mutable data-source cursors")
	}

	invalid := &dsl.Script{Name: "invalid", Protocol: "stub", DataSources: []dsl.DataSource{{Name: "missing", Type: "csv"}}}
	if _, err := New(Config{Script: invalid, VUsers: 1, Duration: time.Second}); err == nil || !strings.Contains(err.Error(), "initialize data sources") {
		t.Fatalf("New invalid data source error = %v", err)
	}
	if _, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, Groups: []GroupConfig{{Name: "bad", Script: invalid, VUsers: 1}}}); err == nil || !strings.Contains(err.Error(), "initialize data sources") {
		t.Fatalf("NewMultiGroup invalid data source error = %v", err)
	}
}

func TestEngineConstructorsRejectUnboundedVUserAndGroupAdmission(t *testing.T) {
	script := &dsl.Script{Name: "bounded", Protocol: "stub"}
	if _, err := New(Config{Script: script, VUsers: MaxEngineVUsers + 1, Duration: time.Second}); err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("New oversized VUsers error = %v", err)
	}

	groups := make([]GroupConfig, MaxEngineGroups+1)
	for index := range groups {
		groups[index] = GroupConfig{Name: fmt.Sprintf("g-%d", index), Script: script, VUsers: 1}
	}
	if _, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, Groups: groups}); err == nil || !strings.Contains(err.Error(), "group count") {
		t.Fatalf("NewMultiGroup oversized group count error = %v", err)
	}

	if _, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, Groups: []GroupConfig{
		{Name: "one", Script: script, VUsers: MaxEngineVUsers},
		{Name: "two", Script: script, VUsers: 1},
	}}); err == nil || !strings.Contains(err.Error(), "aggregate vusers") {
		t.Fatalf("NewMultiGroup oversized aggregate error = %v", err)
	}
}

func TestScaleVUsersRejectsUnboundedTargetBeforeSpawning(t *testing.T) {
	script := &dsl.Script{Name: "bounded-scale", Protocol: "stub"}
	multi, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, Groups: []GroupConfig{
		{Name: "large", Script: script, VUsers: MaxEngineVUsers - 1},
		{Name: "small", Script: script, VUsers: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	multi.baseCtx = ctx
	multi.loopCtx = ctx
	multi.wg = &wg
	multi.controlsReady = true
	multi.scalingOpen = true

	if err := multi.ScaleVUsers("small", 2); err == nil || !strings.Contains(err.Error(), "aggregate target") {
		t.Fatalf("aggregate scale error = %v", err)
	}
	if err := multi.ScaleVUsers("small", MaxEngineVUsers+1); err == nil || !strings.Contains(err.Error(), "target vusers") {
		t.Fatalf("single-group scale error = %v", err)
	}
}

func TestParameterRuntimeReusesProviderColumnStorage(t *testing.T) {
	provider, err := NewDataProvider([]dsl.DataSource{{
		Name:           "users",
		Type:           "csv",
		InlineData:     "name\nalice\nbob\n",
		FirstRowHeader: true,
	}})
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}
	providerValues, ok := provider.GetColumnValues("users", "name")
	if !ok || len(providerValues) != 2 {
		t.Fatalf("provider values = %#v, %v", providerValues, ok)
	}
	parameter := dsl.Parameter{Name: "name", Type: "file", DataSource: "users", Column: "name"}
	runtime := NewParameterRuntime([]dsl.Parameter{parameter}, provider, 0)
	runtime.Prime()
	runtimeValues := runtime.states["name"].values
	if len(runtimeValues) != len(providerValues) || &runtimeValues[0] != &providerValues[0] {
		t.Fatal("parameter runtime cloned the full provider column")
	}
}

func TestNumericRuntimeGuardsCoverFullIntegerRange(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1

	if got, ok := randomIntInclusive(minInt, maxInt, func() uint64 { return 0 }); !ok || got != minInt {
		t.Fatalf("full-range minimum = %d, %v; want %d", got, ok, minInt)
	}
	if got, ok := randomIntInclusive(minInt, maxInt, func() uint64 { return ^uint64(0) }); !ok || got != maxInt {
		t.Fatalf("full-range maximum = %d, %v; want %d", got, ok, maxInt)
	}
	if got := saturatingAddInt(maxInt, 1); got != maxInt {
		t.Fatalf("positive saturation = %d, want %d", got, maxInt)
	}
	if got := saturatingAddInt(minInt, -1); got != minInt {
		t.Fatalf("negative saturation = %d, want %d", got, minInt)
	}
	if got := generateRandomString(rand.New(rand.NewSource(1)), dsl.MaxGeneratedParameterBytes+1, ""); got != "" {
		t.Fatalf("oversized random string length = %d, want rejection", len(got))
	}
	if _, ok := resolveBuiltinFunction(fmt.Sprintf("fn.random_string(%d)", dsl.MaxGeneratedParameterBytes+1), time.Now, nil); ok {
		t.Fatal("oversized fn.random_string was accepted")
	}
	if got, bounded := openWorkloadArrivalLimit(maxInt, 2); !bounded || got != maxInt {
		t.Fatalf("overflowing arrival limit = %d, %v; want saturated %d", got, bounded, maxInt)
	}
}

func TestMetricsResultsAreCompactedAndDetached(t *testing.T) {
	original := &protocol.Result{
		ActionName:       strings.Repeat("a", maxMetricLabelBytes+100),
		ErrorMessage:     strings.Repeat("e", maxMetricErrorBytes+100),
		ResponseBody:     strings.Repeat("b", 1<<20),
		HTML:             strings.Repeat("h", 1<<20),
		ScreenshotBase64: strings.Repeat("s", 1<<20),
		Extractions:      map[string]string{"soap_operation": "CreateOrder", "payload": strings.Repeat("x", 1<<20)},
		ProtocolEvidence: map[string]any{"payload": strings.Repeat("x", 1<<20)},
		Snapshot:         &protocol.ErrorSnapshot{ResponseBody: strings.Repeat("x", 1<<20)},
		ClientMetrics:    &protocol.ClientMetrics{DeviceProfile: "desktop", FCPMs: 12},
		ServerTelemetry:  &protocol.ServerTelemetry{PartitionID: "0", MessagesSent: 1},
	}

	metric := compactMetricsResult(original)
	if metric == nil {
		t.Fatal("compactMetricsResult returned nil")
	}
	if metric.ResponseBody != "" || metric.HTML != "" || metric.ScreenshotBase64 != "" || metric.ProtocolEvidence != nil || metric.Snapshot != nil {
		t.Fatalf("metric retained rich evidence: %#v", metric)
	}
	if len(metric.ActionName) != maxMetricLabelBytes || len(metric.ErrorMessage) != maxMetricErrorBytes {
		t.Fatalf("metric string bounds = action %d error %d", len(metric.ActionName), len(metric.ErrorMessage))
	}
	if len(metric.Extractions) != 1 || metric.Extractions["soap_operation"] != "CreateOrder" {
		t.Fatalf("metric extractions = %#v", metric.Extractions)
	}
	original.ClientMetrics.DeviceProfile = "mutated"
	original.ServerTelemetry.PartitionID = "mutated"
	if metric.ClientMetrics.DeviceProfile != "desktop" || metric.ServerTelemetry.PartitionID != "0" {
		t.Fatal("metric telemetry aliases the caller's mutable structs")
	}
}

func TestMetricsCardinalityAndSnapshotRetentionAreBounded(t *testing.T) {
	collector := NewMetricsCollector(1)
	collector.resCollector = nil
	results := make([]*protocol.Result, maxMetricDimensionEntries+100)
	longError := strings.Repeat("e", maxMetricErrorBytes)
	for i := range results {
		results[i] = &protocol.Result{
			ActionName:   fmt.Sprintf("%04d-%s", i, strings.Repeat("a", maxMetricLabelBytes-5)),
			Duration:     time.Millisecond,
			Success:      false,
			ErrorMessage: longError,
		}
	}

	snapshot := collector.computeSnapshot(results, time.Second)
	if got := len(snapshot.ByAction); got > maxMetricDimensionEntries {
		t.Fatalf("action cardinality = %d, limit %d", got, maxMetricDimensionEntries)
	}
	if _, ok := snapshot.ByAction[metricOverflowLabel]; !ok {
		t.Fatalf("overflow bucket missing from %d action dimensions", len(snapshot.ByAction))
	}

	for i := 0; i < 40; i++ {
		collector.publishSnapshot(results, time.Second)
	}
	if collector.snapshotBytes > maxMetricsSnapshotBytes {
		t.Fatalf("retained snapshot estimate = %d, limit %d", collector.snapshotBytes, maxMetricsSnapshotBytes)
	}
	if len(collector.snapshots) >= 40 {
		t.Fatalf("byte retention did not evict snapshots: retained %d of 40", len(collector.snapshots))
	}
	if len(collector.snapshots) != len(collector.snapshotSizes) {
		t.Fatalf("snapshot metadata lengths differ: %d vs %d", len(collector.snapshots), len(collector.snapshotSizes))
	}
}

func TestMetricsShutdownDrainPublishesHardCappedBatches(t *testing.T) {
	collector := NewMetricsCollector(maxMetricsBatchSize * 2)
	collector.resCollector = nil
	for i := 0; i < maxMetricsBatchSize*2; i++ {
		collector.Record(&protocol.Result{ActionName: "request", Duration: time.Millisecond, Success: true})
	}
	collector.Start(time.Hour)
	collector.Stop()

	if len(collector.snapshots) != 2 {
		t.Fatalf("shutdown snapshots = %d, want two capped batches", len(collector.snapshots))
	}
	var samples int64
	for _, snapshot := range collector.snapshots {
		count := snapshot.ByAction["request"].Count
		if count > maxMetricsBatchSize {
			t.Fatalf("snapshot batch count = %d, limit %d", count, maxMetricsBatchSize)
		}
		samples += count
	}
	if samples != int64(maxMetricsBatchSize*2) {
		t.Fatalf("published samples = %d, want %d", samples, maxMetricsBatchSize*2)
	}
}

func TestResultEvidenceIsBoundedBeforeRetention(t *testing.T) {
	result := &protocol.Result{
		ActionName:       strings.Repeat("a", maxResultMetadataBytes+100),
		CorrelationID:    strings.Repeat("c", maxResultMetadataBytes+100),
		DependencyTarget: strings.Repeat("d", maxResultMetadataBytes+100),
		ErrorMessage:     strings.Repeat("e", maxResultErrorBytes+100),
		ResponseBody:     strings.Repeat("r", maxRetainedResultEvidenceBytes*2),
		HTML:             strings.Repeat("h", maxRetainedResultEvidenceBytes*2),
		Extractions:      make(map[string]string),
		ProtocolEvidence: map[string]any{"large": strings.Repeat("p", 1<<20)},
		Snapshot:         &protocol.ErrorSnapshot{ResponseBody: strings.Repeat("s", defaultErrorSnapshotMaxBytes*2)},
		ScreenshotBase64: strings.Repeat("i", maxRetainedResultEvidenceBytes*2),
		ClientMetrics:    &protocol.ClientMetrics{DeviceProfile: strings.Repeat("p", maxResultMetadataBytes+100)},
		ServerTelemetry:  &protocol.ServerTelemetry{PartitionID: strings.Repeat("q", maxResultMetadataBytes+100)},
	}
	for i := 0; i < maxResultExtractions+100; i++ {
		result.Extractions[fmt.Sprintf("key-%d", i)] = strings.Repeat("v", 1<<10)
	}

	boundResultEvidence(result)
	if len(result.ErrorMessage) > maxResultErrorBytes {
		t.Fatalf("error message length = %d", len(result.ErrorMessage))
	}
	for name, value := range map[string]string{
		"action": result.ActionName, "correlation": result.CorrelationID,
		"dependency": result.DependencyTarget, "client": result.ClientMetrics.DeviceProfile,
		"server": result.ServerTelemetry.PartitionID,
	} {
		if len(value) > maxResultMetadataBytes {
			t.Fatalf("%s metadata length = %d, limit %d", name, len(value), maxResultMetadataBytes)
		}
	}
	if len(result.Extractions) > maxResultExtractions {
		t.Fatalf("extraction count = %d", len(result.Extractions))
	}
	if large, _ := result.ProtocolEvidence["large"].(string); len(large) > 8<<10 {
		t.Fatalf("protocol evidence string length = %d", len(large))
	}
	if result.Snapshot == nil || len(result.Snapshot.ResponseBody) > defaultErrorSnapshotMaxBytes {
		t.Fatalf("bounded snapshot = %#v", result.Snapshot)
	}
	retainedBytes := len(result.ResponseBody) + len(result.HTML) + len(result.ScreenshotBase64)
	if retainedBytes > maxRetainedResultEvidenceBytes {
		t.Fatalf("top-level retained evidence = %d, limit %d", retainedBytes, maxRetainedResultEvidenceBytes)
	}
}
