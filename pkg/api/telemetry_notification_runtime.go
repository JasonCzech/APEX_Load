package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const telemetryNotificationDigestKeySetting = "telemetry_notification_digest_key:v1"
const telemetryNotificationLease = time.Minute

type telemetryNotificationStore interface {
	AuthorizeTelemetryNotificationDelivery(context.Context, storage.TelemetryNotificationDelivery, time.Time) (bool, error)
	GetTelemetryNotification(context.Context, string) (*storage.TelemetryNotification, error)
	ListDueTelemetryNotificationDeliveries(context.Context, time.Time, int) ([]storage.TelemetryNotificationDelivery, error)
	ClaimTelemetryNotificationDelivery(context.Context, storage.TelemetryNotificationDelivery, time.Time, time.Duration) (*storage.TelemetryNotificationDelivery, error)
	FinishTelemetryNotificationDelivery(context.Context, storage.TelemetryNotificationDelivery, string, time.Time) (bool, error)
}

type telemetryArchiveFinalizationStore interface {
	CompleteArchiveFinalizationWithTelemetry(string, string, string, *storage.RunArchiveRecord, storage.ArchiveFinalizationRunIdentity, storage.TelemetryNotificationPublication) (bool, error)
}

func telemetryDestinationFingerprint(key []byte, webhook NotificationWebhookSettings) (string, error) {
	// The complete authorized configuration is pinned, including credentials.
	// Only the keyed fingerprint is persisted. Reordering filters is semantic-free.
	webhook.Events = slices.Clone(webhook.Events)
	slices.Sort(webhook.Events)
	webhook.Events = slices.Compact(webhook.Events)
	data, err := json.Marshal(webhook)
	if err != nil {
		return "", err
	}
	hash := hmac.New(sha256.New, key)
	_, _ = hash.Write([]byte("telemetry-notification-destination/v1\x00"))
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Server) telemetryNotificationDestinations(ctx context.Context, key []byte) (map[string]NotificationWebhookSettings, error) {
	if err := s.runner.refreshSystemSettingsFromStoreContext(ctx, true); err != nil {
		return nil, err
	}
	settings := s.runner.currentNotificationSettings()
	if err := validateNotificationsSettings(settings); err != nil {
		return nil, err
	}
	out := map[string]NotificationWebhookSettings{}
	for _, webhook := range settings.Webhooks {
		if !webhook.Enabled || !notificationWebhookMatchesEvent(webhook, "telemetry_acceptance") {
			continue
		}
		id, err := telemetryDestinationFingerprint(key, webhook)
		if err != nil {
			return nil, err
		}
		out[id] = webhook
	}
	return out, nil
}

func (s *Server) telemetryNotificationPublication(ctx context.Context, testID string, manifest *RunArchiveManifest) (storage.TelemetryNotificationPublication, error) {
	rejected := storage.TelemetryNotificationPublication{Decision: "projection_rejected"}
	if manifest == nil {
		return rejected, nil
	}
	verdict := telemetryAcceptanceForManifest(manifest)
	if verdict == nil || verdict.Outcome == TelemetryAcceptancePass || verdict.Mode == telemetryAcceptanceModeUnknown {
		return storage.TelemetryNotificationPublication{Decision: "not_applicable"}, nil
	}
	if manifest.Provenance.RunIncarnation == "" || manifest.CompletedAt.IsZero() {
		return storage.TelemetryNotificationPublication{Decision: "identity_unavailable"}, nil
	}
	if verdict.Policy == nil {
		return rejected, nil
	}
	var rules []engine.RedactionRule
	if manifest.Provenance.RuntimeSettings != nil {
		rules = manifest.Provenance.RuntimeSettings.RedactionRules
	}
	redactor, err := engine.NewRedactionConfig(rules)
	if err != nil {
		return rejected, nil
	}
	clean := func(value string, limit int) string {
		value = redactor.ScrubText(scrub.Value(value, scrub.Policy{}))
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, value)
		return boundedtext.TruncateUTF8(value, limit)
	}
	// New controller identifiers are opaque. Unsafe legacy identifiers cannot
	// be copied into an immutable envelope or external payload as cleartext.
	for _, id := range []string{testID, manifest.Provenance.ProjectID, manifest.Provenance.RunIncarnation, verdict.Policy.ID} {
		if clean(id, 256) != id {
			return rejected, nil
		}
	}
	hash := sha256.New()
	assessment := struct {
		Version  string                       `json:"version"`
		Verdict  *TelemetryAcceptanceVerdict  `json:"verdict"`
		Coverage *TelemetryCoverageRecord     `json:"coverage"`
		Loss     *TelemetryLossAccounting     `json:"loss"`
		Stages   []TelemetryStageDistribution `json:"stages"`
	}{"telemetry-notification-assessment/v1", verdict, manifest.TelemetryCoverage, manifest.TelemetryLoss, manifest.TelemetryStageDistributions}
	if err := json.NewEncoder(hash).Encode(assessment); err != nil {
		return rejected, nil
	}
	conditions := []string{"run:" + testID, "mode:" + verdict.Mode, fmt.Sprintf("policy:%s@%d", verdict.Policy.ID, verdict.Policy.Revision)}
	for _, criterion := range verdict.Criteria {
		if criterion.Outcome != TelemetryAcceptancePass && !criterion.Advisory && len(conditions) < maxTelemetryAcceptanceEventConditions {
			conditions = append(conditions, "criterion:"+criterion.Criterion+" "+criterion.Outcome)
		}
	}
	if manifest.TelemetryCoverage != nil {
		for _, condition := range telemetryAcceptanceSourceConditions(manifest.TelemetryCoverage.Coverage) {
			if len(conditions) >= maxTelemetryAcceptanceEventConditions {
				break
			}
			conditions = append(conditions, condition)
		}
	}
	for i := range conditions {
		conditions[i] = clean(conditions[i], 512)
	}
	summary := "telemetry acceptance " + verdict.Outcome
	if len(verdict.Reasons) > 0 {
		summary += ": " + verdict.Reasons[0]
	}
	name := manifest.TestName
	if strings.TrimSpace(name) == "" {
		name = testID
	}
	payload := NotificationEvent{Type: "telemetry_acceptance", TestID: testID, ProjectID: manifest.Provenance.ProjectID, TestName: clean(name, 256), Status: verdict.Outcome, Severity: telemetryAcceptanceEventSeverity(verdict.Outcome), Summary: clean(summary, 1024), Conditions: conditions, Timestamp: manifest.CompletedAt.UTC()}
	raw, err := json.Marshal(payload)
	if err != nil {
		return rejected, nil
	}
	event := storage.TelemetryNotification{TestID: testID, ProjectID: manifest.Provenance.ProjectID, RunIncarnation: manifest.Provenance.RunIncarnation, PolicyID: verdict.Policy.ID, PolicyRevision: verdict.Policy.Revision, AssessmentRevision: hex.EncodeToString(hash.Sum(nil)), Outcome: verdict.Outcome, CompletedAt: manifest.CompletedAt, Payload: raw, Destinations: []string{}}
	id, err := storage.TelemetryNotificationID(event)
	if err != nil {
		return rejected, nil
	}
	store, ok := s.runner.archiveStore.(telemetryNotificationStore)
	if !ok {
		return rejected, errors.New("durable telemetry notifications unavailable")
	}
	if prior, err := store.GetTelemetryNotification(ctx, id); err == nil {
		// Every retry uses the originally committed payload and recipients. A later
		// configuration edit may revoke recipients but cannot reroute this outcome.
		return storage.TelemetryNotificationPublication{Event: prior}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return rejected, err
	}
	key, err := s.persistentDigestKeyContext(ctx, telemetryNotificationDigestKeySetting, "telemetry notification")
	if err != nil {
		return rejected, err
	}
	defer clear(key)
	destinations, err := s.telemetryNotificationDestinations(ctx, key)
	if err != nil {
		return rejected, err
	}
	for destination := range destinations {
		event.Destinations = append(event.Destinations, destination)
	}
	slices.Sort(event.Destinations)
	if _, err := storage.TelemetryNotificationID(event); err != nil {
		return rejected, nil
	}
	return storage.TelemetryNotificationPublication{Event: &event}, nil
}

func (tr *TestRunner) hasAtomicTelemetryNotifications() bool {
	if tr == nil || tr.telemetryNotificationPublication == nil {
		return false
	}
	_, complete := tr.archiveStore.(telemetryArchiveFinalizationStore)
	_, delivery := tr.archiveStore.(telemetryNotificationStore)
	return complete && delivery
}

func (s *Server) startTelemetryNotificationWorker() {
	if s == nil || s.runner == nil || s.runner.notifier == nil || s.telemetryNotificationCancel != nil {
		return
	}
	if _, ok := s.runner.archiveStore.(telemetryNotificationStore); !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.telemetryNotificationCancel = cancel
	if !s.launchBackground(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			if s.isExecutionLeader() {
				s.reconcileTelemetryNotifications(ctx)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}) {
		cancel()
	}
}

func (s *Server) reconcileTelemetryNotifications(ctx context.Context) {
	store, ok := s.runner.archiveStore.(telemetryNotificationStore)
	if !ok {
		return
	}
	scan, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	due, err := store.ListDueTelemetryNotificationDeliveries(scan, time.Now().UTC(), maxNotificationDeliveries)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("telemetry notification scan failed")
		}
		return
	}
	s.telemetryNotificationSlotsOnce.Do(func() {
		s.telemetryNotificationSlots = make(chan struct{}, maxNotificationDeliveries)
	})
	for _, candidate := range due {
		if ctx.Err() != nil {
			return
		}
		// Bound preparation and storage observers too: a stalled settings read
		// must not accumulate another batch of goroutines on every scan.
		select {
		case s.telemetryNotificationSlots <- struct{}{}:
		default:
			return
		}
		claim, err := store.ClaimTelemetryNotificationDelivery(scan, candidate, time.Now().UTC(), telemetryNotificationLease)
		if err != nil || claim == nil {
			<-s.telemetryNotificationSlots
			continue
		}
		// The owning Server waits for every delivery observer. The transport callback
		// only signals this channel; it never writes storage after Shutdown returns.
		if !s.launchBackground(func() {
			defer func() { <-s.telemetryNotificationSlots }()
			s.deliverTelemetryNotification(ctx, store, *claim)
		}) {
			<-s.telemetryNotificationSlots
			return // The bounded lease remains recoverable after shutdown.
		}
	}
}

func (s *Server) deliverTelemetryNotification(ctx context.Context, store telemetryNotificationStore, claim storage.TelemetryNotificationDelivery) {
	outcome := "retry"
	defer func() {
		finish, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := store.FinishTelemetryNotificationDelivery(finish, claim, outcome, time.Now().UTC()); err != nil {
			slog.Warn("telemetry notification completion remains retryable")
		}
	}()
	if ctx.Err() != nil {
		return
	}
	work, cancel := context.WithTimeout(ctx, telemetryNotificationLease-10*time.Second)
	defer cancel()
	event, err := store.GetTelemetryNotification(work, claim.EventID)
	if err != nil {
		return
	}
	key, err := s.persistentDigestKeyContext(work, telemetryNotificationDigestKeySetting, "telemetry notification")
	if err != nil {
		return
	}
	defer clear(key)
	destinations, err := s.telemetryNotificationDestinations(work, key)
	if err != nil {
		return
	}
	webhook, enabled := destinations[claim.Destination]
	if !enabled {
		outcome = "revoked"
		return
	}
	var payload NotificationEvent
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		outcome = "revoked"
		return
	}
	payload.durableIdentity = claim.EventID
	body, err := buildNotificationPayloadForWebhook(webhook, payload)
	if err != nil {
		outcome = "revoked"
		return
	}
	if work.Err() != nil {
		return
	}
	if allowed, err := store.AuthorizeTelemetryNotificationDelivery(work, claim, time.Now().UTC()); err != nil || !allowed {
		return
	}
	result := make(chan string, 1)
	if !s.runner.notifier.tryDispatch(webhook, body, notificationDedupeKey(webhook, payload), func(_ NotificationWebhookSettings, status string) {
		select {
		case result <- status:
		default:
		}
	}) {
		return
	}
	select {
	case status := <-result:
		if status == "delivered" {
			outcome = "delivered"
		}
	case <-work.Done():
	}
}
