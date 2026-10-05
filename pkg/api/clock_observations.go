package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	envArchiveClockObservationsMaxBytes = "APEXLOAD_ARCHIVE_CLOCK_OBSERVATIONS_MAX_BYTES"

	clockObservationsFileName  = "clock_observations.jsonl"
	clockObservationSchema     = "apexload.clock_observation.v1"
	clockObservationRecordType = "clock_observations_v1"
	// maxClockObservations bounds the observations one run keeps.
	maxClockObservations = 10000
)

// defaultClockObservationSpacing keeps at most one observation per source per
// minute; generators are re-measured at the same rate while a run is live.
const defaultClockObservationSpacing = time.Minute

// ClockObservation is one measurement of a source's clock against the
// controller's (apexload.clock_observation.v1). The offset is the source's
// clock minus the controller's; the uncertainty is half the round trip.
// Controller names the HA execution owner that measured it when known; a
// collector's offset is against whichever replica answered its heartbeat.
type ClockObservation struct {
	Schema        string    `json:"schema"`
	Source        string    `json:"source"`
	SourceID      string    `json:"source_id,omitempty"`
	Controller    string    `json:"controller,omitempty"`
	ClockSource   string    `json:"clock_source"`
	ObservedAt    time.Time `json:"observed_at"`
	OffsetMs      float64   `json:"offset_ms"`
	RoundTripMs   float64   `json:"round_trip_ms"`
	UncertaintyMs float64   `json:"uncertainty_ms"`
	OffsetStatus  string    `json:"offset_status"`
}

func newClockObservation(source, sourceID, clockSource string, estimate analysis.ClockEstimate, observedAt time.Time) ClockObservation {
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	return ClockObservation{
		Schema: clockObservationSchema, Source: source, SourceID: sourceID, ClockSource: clockSource, ObservedAt: observedAt.UTC(),
		OffsetMs: ms(estimate.Offset), RoundTripMs: ms(estimate.RoundTrip), UncertaintyMs: ms(estimate.Uncertainty), OffsetStatus: analysis.OffsetMeasured,
	}
}

// clockObservationLog is one run's bounded record: at most one observation
// per source per window and maxClockObservations in all.
type clockObservationLog struct {
	mu        sync.Mutex
	spacing   time.Duration
	records   []ClockObservation
	windows   map[string]time.Time
	truncated bool
}

func (l *clockObservationLog) record(o ClockObservation) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	spacing := l.spacing
	if spacing <= 0 {
		spacing = defaultClockObservationSpacing
	}
	window := o.ObservedAt.Truncate(spacing)
	if last, ok := l.windows[o.Source]; ok && !window.After(last) {
		return false
	}
	if len(l.records) >= maxClockObservations {
		l.truncated = true
		return false
	}
	if l.windows == nil {
		l.windows = map[string]time.Time{}
	}
	l.windows[o.Source] = window
	l.records = append(l.records, o)
	return true
}

func (l *clockObservationLog) snapshot() ([]ClockObservation, bool) {
	if l == nil {
		return nil, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ClockObservation(nil), l.records...), l.truncated
}

// recordClockObservation adds an observation to a run this controller holds.
// Runs held elsewhere, or no longer held, drop it.
func (tr *TestRunner) recordClockObservation(testID string, o ClockObservation) {
	if tr == nil {
		return
	}
	// Collector batches arrive often; only the first observation of a run
	// takes the runner's write lock, to create its log.
	tr.mu.RLock()
	test := tr.tests[testID]
	var log *clockObservationLog
	if test != nil {
		log = test.clockObservations
	}
	tr.mu.RUnlock()
	if test == nil {
		return
	}
	if log == nil {
		tr.mu.Lock()
		if tr.tests[testID] == test {
			if test.clockObservations == nil {
				test.clockObservations = &clockObservationLog{spacing: tr.clockObservationSpacing}
			}
			log = test.clockObservations
		}
		tr.mu.Unlock()
	}
	if log != nil {
		log.record(o)
	}
}

// measureGeneratorClock runs one heartbeat exchange with a generator and
// returns its clock sample and generator ID.
func measureGeneratorClock(ctx context.Context, client generatorClient) (generatorrpc.ClockSample, string, error) {
	if client == nil {
		return generatorrpc.ClockSample{}, "", errors.New("generator client unavailable")
	}
	heartbeat, err := client.OpenHeartbeat(ctx)
	if err != nil {
		return generatorrpc.ClockSample{}, "", err
	}
	if heartbeat == nil {
		return generatorrpc.ClockSample{}, "", errors.New("generator heartbeat unavailable")
	}
	defer heartbeat.CloseSend()
	// A zero ping timestamp keeps legacy generators from echoing the
	// controller's clock back as their own.
	if err := heartbeat.Send(generatorrpc.Heartbeat{GeneratorID: generatorrpc.ClockProbeGeneratorID}); err != nil {
		return generatorrpc.ClockSample{}, "", err
	}
	response, err := heartbeat.Receive()
	if err != nil {
		return generatorrpc.ClockSample{}, "", err
	}
	sample, ok := heartbeat.ClockSample()
	if !ok {
		return generatorrpc.ClockSample{}, "", errors.New("generator heartbeat carried no clock")
	}
	return sample, response.GeneratorID, nil
}

// observeGeneratorClocks re-measures every live generator's clock once per
// interval until the run stops. A failed probe records nothing; the run is
// never affected by it.
func (b *distributedExecutionBackend) observeGeneratorClocks(ctx context.Context, tr *TestRunner, test *ManagedTest) {
	if ctx == nil || tr == nil || test == nil {
		return
	}
	interval := b.clockProbeInterval
	if interval <= 0 {
		interval = defaultClockObservationSpacing
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		tr.mu.RLock()
		current := tr.tests[test.ID] == test
		clients := cloneClientMap(test.remoteClientsByTarget)
		tr.mu.RUnlock()
		if !current || ctx.Err() != nil {
			return
		}
		targets := make([]string, 0, len(clients))
		for target := range clients {
			targets = append(targets, target)
		}
		sort.Strings(targets)
		term, _ := tr.captureExecutionLeaseTerm()
		for _, target := range targets {
			probeCtx, cancel := context.WithTimeout(ctx, min(b.timeout, 5*time.Second))
			sample, generatorID, err := measureGeneratorClock(probeCtx, clients[target])
			cancel()
			if err != nil {
				continue
			}
			estimate := analysis.ClockEstimate{Offset: sample.Offset, RoundTrip: sample.RoundTrip, Uncertainty: sample.Uncertainty}
			observation := newClockObservation(target, generatorID, analysis.ClockSourceGenerator, estimate, sample.ObservedAt)
			observation.Controller = term.ownerID
			tr.recordClockObservation(test.ID, observation)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// persistClockObservationArchive writes clock_observations.jsonl once after
// finalize, when the run measured any clocks, with its own byte allowance.
func (tr *TestRunner) persistClockObservationArchive(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if test == nil || manifest.ClockObservationsFile != "" {
		return nil
	}
	tr.mu.RLock()
	log := test.clockObservations
	tr.mu.RUnlock()
	observations, truncated := log.snapshot()
	if len(observations) == 0 {
		return nil
	}
	sort.SliceStable(observations, func(i, j int) bool {
		if !observations[i].ObservedAt.Equal(observations[j].ObservedAt) {
			return observations[i].ObservedAt.Before(observations[j].ObservedAt)
		}
		return observations[i].Source < observations[j].Source
	})
	budget, err := newEvidenceSidecarBudget(ctx, record, envArchiveClockObservationsMaxBytes, 8<<20)
	if err != nil {
		return err
	}
	defer budget.close()
	var count int64
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, clockObservationsFileName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
		out := &archiveOutputFile{file: file, budget: budget}
		for _, observation := range observations {
			line, err := json.Marshal(observation)
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
	manifest.ClockObservationsFile = clockObservationsFileName
	manifest.ClockObservationCount = count
	manifest.ClockObservationsTruncated = truncated
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: clockObservationsFileName, Kind: "evidence", Format: "jsonl", RecordType: clockObservationRecordType, Count: count})
}
