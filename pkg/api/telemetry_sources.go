package api

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	telemetrySourcesFileName           = "telemetry_sources.jsonl"
	telemetrySourcesRecordType         = "telemetry_source_epochs_v1"
	telemetrySourceEpochSchema         = "apexload.telemetry_source_epoch.v1"
	envArchiveTelemetrySourcesMaxBytes = "APEXLOAD_ARCHIVE_TELEMETRY_SOURCES_MAX_BYTES"
	// maxTelemetrySourceEpochs bounds one run's ledger; further epochs are
	// counted as truncated rather than tracked.
	maxTelemetrySourceEpochs = 1024
	// maxTelemetrySourceRanges bounds the received-sequence ranges kept per
	// epoch. Past it, loss is reported as a lower bound.
	maxTelemetrySourceRanges = 128
)

// TelemetrySourceEpoch is the publication coverage of one generator process
// (source epoch) within one execution generation. Lost publications are
// sequence numbers missing between the first and last received. The count
// is exact only when the epoch was observed from its first publication to
// its terminal one and every received range was retained; otherwise it is a
// lower bound (an epoch replaced by a restarted process, or one whose stream
// ended early, never published its terminal record here). An unobserved
// record stands for an execution generation this controller never saw, such
// as the generations before an HA adoption.
type TelemetrySourceEpoch struct {
	Schema              string     `json:"schema"`
	Source              string     `json:"source"`
	ExecutionGeneration uint64     `json:"execution_generation"`
	Epoch               string     `json:"epoch,omitempty"`
	EpochStartedAt      *time.Time `json:"epoch_started_at,omitempty"`
	FirstSequence       uint64     `json:"first_sequence,omitempty"`
	LastSequence        uint64     `json:"last_sequence,omitempty"`
	Received            int64      `json:"received"`
	Duplicates          int64      `json:"duplicates,omitempty"`
	OutOfOrder          int64      `json:"out_of_order,omitempty"`
	Unsequenced         int64      `json:"unsequenced,omitempty"`
	TerminalSequence    uint64     `json:"terminal_sequence,omitempty"`
	LostPublications    int64      `json:"lost_publications"`
	LossExact           bool       `json:"loss_exact"`
	Unobserved          bool       `json:"unobserved,omitempty"`
	RangesTruncated     bool       `json:"ranges_truncated,omitempty"`
	FirstObservedAt     time.Time  `json:"first_observed_at"`
	LastObservedAt      time.Time  `json:"last_observed_at"`

	ranges [][2]uint64
}

// TelemetrySourceSummary is the live roll-up of a run's source epochs.
type TelemetrySourceSummary struct {
	Sources               int   `json:"sources"`
	Epochs                int   `json:"epochs"`
	LegacyEpochs          int   `json:"legacy_epochs,omitempty"`
	UnobservedGenerations int   `json:"unobserved_generations,omitempty"`
	LostPublications      int64 `json:"lost_publications"`
	LossExact             bool  `json:"loss_exact"`
	Truncated             bool  `json:"truncated,omitempty"`
}

type telemetrySourceKey struct {
	source     string
	generation uint64
	epoch      string
}

type telemetrySourceLedger struct {
	mu        sync.Mutex
	epochs    map[telemetrySourceKey]*TelemetrySourceEpoch
	truncated bool
}

func newTelemetrySourceLedger() *telemetrySourceLedger {
	return &telemetrySourceLedger{epochs: map[telemetrySourceKey]*TelemetrySourceEpoch{}}
}

// observe records one received publication, including duplicates and
// late arrivals that the live view rejects.
func (l *telemetrySourceLedger) observe(source string, batch generatorrpc.MetricsBatch, at time.Time) {
	if l == nil || source == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := telemetrySourceKey{source: source, generation: batch.ExecutionGeneration, epoch: batch.SourceEpoch}
	record := l.epochs[key]
	if record == nil {
		if len(l.epochs) >= maxTelemetrySourceEpochs {
			l.truncated = true
			return
		}
		record = &TelemetrySourceEpoch{Schema: telemetrySourceEpochSchema, Source: source, ExecutionGeneration: batch.ExecutionGeneration, Epoch: batch.SourceEpoch, FirstObservedAt: at.UTC()}
		if batch.SourceEpochStartedUnixMs > 0 {
			started := time.UnixMilli(batch.SourceEpochStartedUnixMs).UTC()
			record.EpochStartedAt = &started
		}
		l.epochs[key] = record
	}
	record.LastObservedAt = at.UTC()
	record.Received++
	sequence := batch.BatchSequence
	if sequence == 0 {
		record.Unsequenced++
		return
	}
	if isTerminalRemoteLifecycle(batch.LifecycleState) && sequence > record.TerminalSequence {
		record.TerminalSequence = sequence
	}
	if record.covers(sequence) {
		record.Duplicates++
		return
	}
	if record.LastSequence != 0 && sequence < record.LastSequence {
		record.OutOfOrder++
	}
	record.add(sequence)
}

func (r *TelemetrySourceEpoch) covers(sequence uint64) bool {
	for _, span := range r.ranges {
		if sequence >= span[0] && sequence <= span[1] {
			return true
		}
	}
	return false
}

func (r *TelemetrySourceEpoch) add(sequence uint64) {
	if r.FirstSequence == 0 || sequence < r.FirstSequence {
		r.FirstSequence = sequence
	}
	if sequence > r.LastSequence {
		r.LastSequence = sequence
	}
	r.ranges = append(r.ranges, [2]uint64{sequence, sequence})
	sort.Slice(r.ranges, func(i, j int) bool { return r.ranges[i][0] < r.ranges[j][0] })
	merged := r.ranges[:1]
	for _, span := range r.ranges[1:] {
		last := &merged[len(merged)-1]
		if span[0] <= last[1]+1 {
			last[1] = max(last[1], span[1])
			continue
		}
		merged = append(merged, span)
	}
	r.ranges = merged
	for len(r.ranges) > maxTelemetrySourceRanges {
		// Fold the oldest gap into its neighbour. That gap's loss is no
		// longer counted, so the total becomes a lower bound.
		r.ranges[1][0] = r.ranges[0][0]
		r.ranges = r.ranges[1:]
		r.RangesTruncated = true
	}
}

// settle computes the loss fields from the retained ranges.
func (r *TelemetrySourceEpoch) settle() {
	r.LostPublications, r.LossExact = 0, false
	if r.LastSequence == 0 {
		return
	}
	var held uint64
	for _, span := range r.ranges {
		held += span[1] - span[0] + 1
	}
	span := r.LastSequence - r.FirstSequence + 1
	if span > held {
		r.LostPublications = int64(min(span-held, uint64(1<<62)))
	}
	// Publications before the first one received are unknown unless the
	// first one received is the epoch's first, and those after the last one
	// received are unknown unless the epoch's terminal publication arrived.
	r.LossExact = r.Epoch != "" && r.FirstSequence == 1 && r.TerminalSequence != 0 && r.TerminalSequence == r.LastSequence && !r.RangesTruncated
}

// snapshot returns settled copies, ordered by source, generation and first
// observation, plus an unobserved record for every earlier execution
// generation (from the run's failovers) that this ledger never saw.
func (l *telemetrySourceLedger) snapshot(failovers ...RunFailover) ([]TelemetrySourceEpoch, bool) {
	if l == nil {
		return unobservedTelemetryGenerations(nil, failovers), false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]TelemetrySourceEpoch, 0, len(l.epochs)+len(failovers))
	for _, record := range l.epochs {
		copied := *record
		copied.ranges = append([][2]uint64(nil), record.ranges...)
		copied.settle()
		out = append(out, copied)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.ExecutionGeneration != b.ExecutionGeneration {
			return a.ExecutionGeneration < b.ExecutionGeneration
		}
		if !a.FirstObservedAt.Equal(b.FirstObservedAt) {
			return a.FirstObservedAt.Before(b.FirstObservedAt)
		}
		return a.Epoch < b.Epoch
	})
	return unobservedTelemetryGenerations(out, failovers), l.truncated
}

// unobservedTelemetryGenerations appends a record for each prior execution
// generation of an adopted run that no observed epoch covers: its coverage is
// unknown, never exact.
func unobservedTelemetryGenerations(epochs []TelemetrySourceEpoch, failovers []RunFailover) []TelemetrySourceEpoch {
	observed := map[uint64]bool{}
	for _, epoch := range epochs {
		observed[epoch.ExecutionGeneration] = true
	}
	for _, failover := range failovers {
		if failover.PriorGeneration == 0 || observed[failover.PriorGeneration] {
			continue
		}
		observed[failover.PriorGeneration] = true
		epochs = append(epochs, TelemetrySourceEpoch{Schema: telemetrySourceEpochSchema, Source: "*", ExecutionGeneration: failover.PriorGeneration, Unobserved: true, FirstObservedAt: failover.AdoptedAt.UTC(), LastObservedAt: failover.AdoptedAt.UTC()})
	}
	return epochs
}

func (l *telemetrySourceLedger) summary(failovers ...RunFailover) *TelemetrySourceSummary {
	epochs, truncated := l.snapshot(failovers...)
	if len(epochs) == 0 && !truncated {
		return nil
	}
	summary := &TelemetrySourceSummary{LossExact: !truncated, Truncated: truncated}
	sources := map[string]bool{}
	for _, epoch := range epochs {
		summary.LossExact = summary.LossExact && epoch.LossExact
		if epoch.Unobserved {
			summary.UnobservedGenerations++
			continue
		}
		summary.Epochs++
		sources[epoch.Source] = true
		if epoch.Epoch == "" {
			summary.LegacyEpochs++
		}
		summary.LostPublications += epoch.LostPublications
	}
	summary.Sources = len(sources)
	return summary
}

// telemetrySourceLedgerLocked returns the run's ledger, creating it on first
// use. It lives as long as the run's ManagedTest, so a finalization retry
// still finds it and a run that is never finalized here releases it with
// the test. The caller holds tr.mu.
func telemetrySourceLedgerLocked(test *ManagedTest) *telemetrySourceLedger {
	if test == nil {
		return nil
	}
	if test.telemetrySources == nil {
		test.telemetrySources = newTelemetrySourceLedger()
	}
	return test.telemetrySources
}

// observeTelemetryPublicationLocked counts one received publication in the
// run's source ledger while this stream still owns the target. Ownership, not
// the topology generation, decides: a topology change elsewhere re-fences the
// consumer's store loop, but the publication still arrived and is still
// stored, so it must still be counted.
func observeTelemetryPublicationLocked(tr *TestRunner, test *ManagedTest, target string, fence distributedMetricsConsumerFence, terminal bool, batch generatorrpc.MetricsBatch, now time.Time) bool {
	if !distributedMetricsConsumerStillOwnedLocked(tr, test, target, fence, terminal) {
		return false
	}
	telemetrySourceLedgerLocked(test).observe(target, batch, now)
	return true
}

// telemetrySourceSummaryLocked is the run's live source summary; generations
// adopted from a failed-over controller count as unobserved.
func telemetrySourceSummaryLocked(test *ManagedTest) *TelemetrySourceSummary {
	return test.telemetrySources.summary(test.continuityFailovers...)
}

// persistTelemetrySourceArchive writes telemetry_sources.jsonl after
// finalize, with its own byte allowance. The ledger stays with the run, so a
// finalization retry rewrites the same sidecar; a manifest that already
// declares it is left alone.
func (tr *TestRunner) persistTelemetrySourceArchive(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if test == nil || manifest.TelemetrySourcesFile != "" {
		return nil
	}
	tr.mu.RLock()
	ledger, failovers := test.telemetrySources, append([]RunFailover(nil), test.continuityFailovers...)
	tr.mu.RUnlock()
	epochs, truncated := ledger.snapshot(failovers...)
	if len(epochs) == 0 {
		return nil
	}
	budget, err := newEvidenceSidecarBudget(ctx, record, envArchiveTelemetrySourcesMaxBytes, 4<<20)
	if err != nil {
		return err
	}
	defer budget.close()
	var count int64
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, telemetrySourcesFileName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
		out := &archiveOutputFile{file: file, budget: budget}
		for _, epoch := range epochs {
			line, err := json.Marshal(epoch)
			if err != nil {
				return 0, err
			}
			line = append(line, '\n')
			if !budget.admits(len(line)) {
				truncated = true
				return 0, nil
			}
			if _, err := out.Write(line); err != nil {
				return 0, err
			}
			count++
		}
		return 0, nil
	}, ctx)
	if err != nil {
		return err
	}
	manifest.TelemetrySourcesFile = telemetrySourcesFileName
	manifest.TelemetrySourceEpochCount = count
	manifest.TelemetrySourcesTruncated = truncated
	// The ledger stays with the run: a later finalization step that fails
	// is retried from a reloaded manifest and rewrites this sidecar.
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: telemetrySourcesFileName, Kind: "evidence", Format: "jsonl", RecordType: telemetrySourcesRecordType, Count: count})
}
