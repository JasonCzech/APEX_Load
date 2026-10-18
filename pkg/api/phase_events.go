package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Phase event kinds: load-level transitions and the run's pause, resume and
// stop. Analysis uses them to tell load-driven changes from incidents.
const (
	PhaseRampStart    = "ramp_start"
	PhaseLevelReached = "level_reached"
	PhaseLevelChange  = "level_change"
	PhasePause        = "pause"
	PhaseResume       = "resume"
	PhaseStop         = "stop"
)

const (
	// maxPhaseEvents bounds one run's phase events.
	maxPhaseEvents = 500
	// maxPhaseDeliveries bounds the sink outcomes kept per event.
	maxPhaseDeliveries = 16

	phaseEventsFileName  = "phase_events.jsonl"
	phaseEventSchema     = "apexload.phase_event.v1"
	phaseEventRecordType = "phase_events_v1"
	envArchivePhaseMax   = "APEXLOAD_ARCHIVE_PHASE_EVENTS_MAX_BYTES"

	// notificationEventLoadPhase delivers level transitions to webhooks.
	// Pause, resume and stop keep their existing lifecycle events.
	notificationEventLoadPhase = "load_phase"
)

// Delivery outcomes for a phase event's sinks.
const (
	phaseDeliveryPending   = "pending"
	phaseDeliveryDelivered = "delivered"
	phaseDeliveryFailed    = "failed"
	phaseDeliveryDropped   = "dropped"
)

// PhaseEvent is one load-phase transition (apexload.phase_event.v1). Level is
// the target VUsers for level kinds; Target names the group, empty for
// run-wide pause, resume and stop. It is generated evidence, kept apart from
// analyst-authored run annotations.
type PhaseEvent struct {
	Schema     string               `json:"schema"`
	Kind       string               `json:"kind"`
	Level      *int                 `json:"level,omitempty"`
	Target     string               `json:"target,omitempty"`
	At         time.Time            `json:"at"`
	Deliveries []PhaseEventDelivery `json:"deliveries,omitempty"`
	// DeliveriesTruncated marks sink outcomes beyond maxPhaseDeliveries.
	DeliveriesTruncated bool `json:"deliveries_truncated,omitempty"`
}

// PhaseEventDelivery is one sink's outcome. Sink is the webhook's name, never
// its URL, which may embed a credential.
type PhaseEventDelivery struct {
	Sink   string `json:"sink"`
	Format string `json:"format"`
	Status string `json:"status"`
}

// phaseEventLog is one run's bounded phase events, deduplicated by kind,
// target, level and second.
type phaseEventLog struct {
	mu        sync.Mutex
	events    []PhaseEvent
	keys      map[string]bool
	stopped   bool
	truncated bool
}

func phaseEventKey(e PhaseEvent) string {
	level := ""
	if e.Level != nil {
		level = fmt.Sprint(*e.Level)
	}
	return strings.Join([]string{e.Kind, e.Target, level, e.At.UTC().Truncate(time.Second).Format(time.RFC3339)}, "\x00")
}

// record adds an event and returns its index, or false for a duplicate, a
// second stop, or a full log.
func (l *phaseEventLog) record(e PhaseEvent) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := phaseEventKey(e)
	if l.keys[key] || e.Kind == PhaseStop && l.stopped {
		return 0, false
	}
	if len(l.events) >= maxPhaseEvents {
		l.truncated = true
		return 0, false
	}
	if l.keys == nil {
		l.keys = map[string]bool{}
	}
	l.keys[key] = true
	l.stopped = l.stopped || e.Kind == PhaseStop
	l.events = append(l.events, e)
	return len(l.events) - 1, true
}

// deliver records one sink's outcome; a later outcome for the same sink
// replaces a pending one.
func (l *phaseEventLog) deliver(index int, delivery PhaseEventDelivery) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if index < 0 || index >= len(l.events) {
		return
	}
	event := &l.events[index]
	for i := range event.Deliveries {
		if event.Deliveries[i].Sink == delivery.Sink && event.Deliveries[i].Format == delivery.Format && event.Deliveries[i].Status == phaseDeliveryPending {
			event.Deliveries[i].Status = delivery.Status
			return
		}
	}
	if len(event.Deliveries) >= maxPhaseDeliveries {
		event.DeliveriesTruncated = true
		return
	}
	event.Deliveries = append(event.Deliveries, delivery)
}

func (l *phaseEventLog) snapshot() ([]PhaseEvent, bool) {
	if l == nil {
		return nil, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]PhaseEvent, len(l.events))
	for i, e := range l.events {
		e.Deliveries = append([]PhaseEventDelivery(nil), e.Deliveries...)
		if e.Level != nil {
			level := *e.Level
			e.Level = &level
		}
		out[i] = e
	}
	return out, l.truncated
}

// phaseLog returns the run's phase log, creating it on first use. It never
// takes tr.mu: lifecycle broadcasts call it, some while holding that lock.
func (tr *TestRunner) phaseLog(testID string) *phaseEventLog {
	if tr == nil || strings.TrimSpace(testID) == "" {
		return nil
	}
	log, _ := tr.phaseLogs.LoadOrStore(testID, &phaseEventLog{})
	return log.(*phaseEventLog)
}

// recordLoadPhase records a level transition for a group and delivers it to
// the load_phase webhooks. Sinks fail open: delivery never affects the run.
func (tr *TestRunner) recordLoadPhase(testID, kind, group string, level int, at time.Time) {
	log := tr.phaseLog(testID)
	if log == nil {
		return
	}
	levelCopy := level
	index, ok := log.record(PhaseEvent{Schema: phaseEventSchema, Kind: kind, Level: &levelCopy, Target: group, At: at.UTC()})
	if !ok || tr.notifier == nil {
		return
	}
	// The stage loop holds no lock here, so reading the test's names is safe.
	tr.mu.RLock()
	test := tr.tests[testID]
	event := NotificationEvent{Type: notificationEventLoadPhase, TestID: testID, Status: kind, Timestamp: at.UTC(),
		Summary:    fmt.Sprintf("%s: %s at %d VUsers", group, strings.ReplaceAll(kind, "_", " "), level),
		Conditions: []string{"kind=" + kind, "target=" + group, fmt.Sprintf("level=%d", level)}}
	if test != nil {
		event.ProjectID, event.TestName = normalizeProjectID(test.ProjectID), test.Name
	}
	tr.mu.RUnlock()
	tr.dispatchPhaseNotification(log, index, event)
}

// lifecyclePhaseKinds maps the run's lifecycle notifications to phase kinds.
var lifecyclePhaseKinds = map[string]string{
	"test_paused":   PhasePause,
	"test_resumed":  PhaseResume,
	"test_stopping": PhaseStop,
	"test_complete": PhaseStop,
	"test_failed":   PhaseStop,
}

// recordLifecyclePhase records a pause, resume or stop from its lifecycle
// event, returning the log index its deliveries attach to.
func (tr *TestRunner) recordLifecyclePhase(testID, eventType string, at time.Time) (*phaseEventLog, int, bool) {
	kind, ok := lifecyclePhaseKinds[eventType]
	if !ok {
		return nil, 0, false
	}
	log := tr.phaseLog(testID)
	if log == nil {
		return nil, 0, false
	}
	index, recorded := log.record(PhaseEvent{Schema: phaseEventSchema, Kind: kind, At: at.UTC()})
	return log, index, recorded
}

// dispatchPhaseNotification sends an event and records each webhook's
// outcome against the phase event.
func (tr *TestRunner) dispatchPhaseNotification(log *phaseEventLog, index int, event NotificationEvent) {
	tr.notifier.DispatchObserved(tr.currentNotificationSettings(), event, func(webhook NotificationWebhookSettings, status string) {
		sink := strings.TrimSpace(webhook.Name)
		if sink == "" {
			sink = webhook.Format
		}
		log.deliver(index, PhaseEventDelivery{Sink: sink, Format: webhook.Format, Status: status})
	})
}

// persistPhaseEventArchive writes phase_events.jsonl once after finalize,
// adding the run's stop when no lifecycle event recorded one yet. Finalize
// runs before the completion broadcast, so a run that held one load level
// has recorded nothing yet; its archive still records the stop rather than
// reading as missing phase evidence.
func (tr *TestRunner) persistPhaseEventArchive(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if test == nil || manifest.PhaseEventsFile != "" {
		return nil
	}
	log := &phaseEventLog{}
	if stored, ok := tr.phaseLogs.Load(test.ID); ok {
		log = stored.(*phaseEventLog)
	}
	// The run's end time may not be set on the run yet when finalize runs;
	// the archive's completion time is.
	if manifest.ExecutorCompletedAt != nil && !manifest.ExecutorCompletedAt.IsZero() {
		// A recovered archive may have a later durable row end. Its retained
		// executor time is the boundary of measured workload phases.
		log.record(PhaseEvent{Schema: phaseEventSchema, Kind: PhaseStop, At: archiveExecutionCompletedAt(manifest).UTC()})
	} else if ended := test.EndedAt; ended != nil {
		log.record(PhaseEvent{Schema: phaseEventSchema, Kind: PhaseStop, At: ended.UTC()})
	} else if !manifest.CompletedAt.IsZero() {
		log.record(PhaseEvent{Schema: phaseEventSchema, Kind: PhaseStop, At: manifest.CompletedAt.UTC()})
	}
	events, truncated := log.snapshot()
	budget, err := newEvidenceSidecarBudget(ctx, record, envArchivePhaseMax, 8<<20)
	if err != nil {
		return err
	}
	defer budget.close()
	var count int64
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, phaseEventsFileName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
		out := &archiveOutputFile{file: file, budget: budget}
		for _, event := range events {
			line, err := json.Marshal(event)
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
	manifest.PhaseEventsFile = phaseEventsFileName
	manifest.PhaseEventCount = count
	manifest.PhaseEventsTruncated = truncated
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: phaseEventsFileName, Kind: "evidence", Format: "jsonl", RecordType: phaseEventRecordType, Count: count})
}
