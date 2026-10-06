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
// is exact only when the epoch was observed from its first publication and
// every received range was retained; otherwise it is a lower bound.
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
	LostPublications    int64      `json:"lost_publications"`
	LossExact           bool       `json:"loss_exact"`
	RangesTruncated     bool       `json:"ranges_truncated,omitempty"`
	FirstObservedAt     time.Time  `json:"first_observed_at"`
	LastObservedAt      time.Time  `json:"last_observed_at"`

	ranges [][2]uint64
}

// TelemetrySourceSummary is the live roll-up of a run's source epochs.
type TelemetrySourceSummary struct {
	Sources          int   `json:"sources"`
	Epochs           int   `json:"epochs"`
	LegacyEpochs     int   `json:"legacy_epochs,omitempty"`
	LostPublications int64 `json:"lost_publications"`
	LossExact        bool  `json:"loss_exact"`
	Truncated        bool  `json:"truncated,omitempty"`
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
	// first one received is the epoch's first.
	r.LossExact = r.Epoch != "" && r.FirstSequence == 1 && !r.RangesTruncated
}

// snapshot returns settled copies, ordered by source, generation and epoch
// start.
func (l *telemetrySourceLedger) snapshot() ([]TelemetrySourceEpoch, bool) {
	if l == nil {
		return nil, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]TelemetrySourceEpoch, 0, len(l.epochs))
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
	return out, l.truncated
}

func (l *telemetrySourceLedger) summary() *TelemetrySourceSummary {
	epochs, truncated := l.snapshot()
	if len(epochs) == 0 && !truncated {
		return nil
	}
	summary := &TelemetrySourceSummary{Epochs: len(epochs), LossExact: !truncated, Truncated: truncated}
	sources := map[string]bool{}
	for _, epoch := range epochs {
		sources[epoch.Source] = true
		if epoch.Epoch == "" {
			summary.LegacyEpochs++
		}
		summary.LostPublications += epoch.LostPublications
		summary.LossExact = summary.LossExact && epoch.LossExact
	}
	summary.Sources = len(sources)
	return summary
}

// telemetrySourceLedgerFor returns the run's ledger, creating it on first use.
func (tr *TestRunner) telemetrySourceLedgerFor(testID string) *telemetrySourceLedger {
	if tr == nil || testID == "" {
		return nil
	}
	ledger, _ := tr.telemetrySources.LoadOrStore(testID, newTelemetrySourceLedger())
	return ledger.(*telemetrySourceLedger)
}

func (tr *TestRunner) existingTelemetrySourceLedger(testID string) *telemetrySourceLedger {
	if tr == nil {
		return nil
	}
	ledger, ok := tr.telemetrySources.Load(testID)
	if !ok {
		return nil
	}
	return ledger.(*telemetrySourceLedger)
}

// persistTelemetrySourceArchive writes telemetry_sources.jsonl once after
// finalize, with its own byte allowance, then releases the run's ledger.
func (tr *TestRunner) persistTelemetrySourceArchive(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if test == nil || manifest.TelemetrySourcesFile != "" {
		return nil
	}
	epochs, truncated := tr.existingTelemetrySourceLedger(test.ID).snapshot()
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
	if err := updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: telemetrySourcesFileName, Kind: "evidence", Format: "jsonl", RecordType: telemetrySourcesRecordType, Count: count}); err != nil {
		return err
	}
	tr.telemetrySources.Delete(test.ID)
	return nil
}
