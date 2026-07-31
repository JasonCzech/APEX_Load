package api

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// systemSettingsService is the narrow settings domain boundary used by the HTTP
// handlers. The TestRunner adapter below preserves existing construction while
// keeping HA refresh, partial-update merging, and revision-fenced persistence out
// of the transport layer.
type systemSettingsService interface {
	Get() SystemSettings
	Update(requested SystemSettings, rawBody []byte, expectedRevision string, beforeCommit systemSettingsBeforeCommit) (systemSettingsUpdateResult, error)
}

type systemSettingsBeforeCommit func(systemSettingsUpdatePlan) error

type systemSettingsUpdatePlan struct {
	Previous                       SystemSettings
	Candidate                      SystemSettings
	PolicyChanged                  bool
	NotificationCredentialsChanged bool
	PreviousCredentialCount        int
	CandidateCredentialCount       int
}

type systemSettingsUpdateResult struct {
	systemSettingsUpdatePlan
	Updated SystemSettings
}

// systemSettingsUpdatePayload mirrors SystemSettings for strict request
// decoding, while admitting the response-only has_api_token projection emitted
// for notification webhooks. The projection is intentionally not part of
// NotificationWebhookSettings and is discarded before the domain service sees
// the request.
type systemSettingsUpdatePayload struct {
	SystemSettings
	Notifications systemSettingsUpdateNotifications `json:"notifications"`
	// Revision is a response projection accepted and ignored so a GET document
	// can be echoed without weakening strict unknown-field validation.
	Revision string `json:"revision,omitempty"`
	// ExpectedRevision is the body-form optimistic concurrency precondition.
	// HTTP clients should normally send the GET ETag in If-Match instead.
	ExpectedRevision string `json:"expected_revision,omitempty"`
}

type systemSettingsUpdateNotifications struct {
	Webhooks []systemSettingsUpdateWebhook `json:"webhooks,omitempty"`
}

type systemSettingsUpdateWebhook struct {
	NotificationWebhookSettings
	HasAPIToken bool `json:"has_api_token,omitempty"`
}

type decodedSystemSettingsUpdate struct {
	Settings         SystemSettings
	ExpectedRevision string
}

func decodeSystemSettingsUpdate(body []byte) (decodedSystemSettingsUpdate, error) {
	var payload systemSettingsUpdatePayload
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return decodedSystemSettingsUpdate{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return decodedSystemSettingsUpdate{}, fmt.Errorf("request body must contain a single JSON value")
		}
		return decodedSystemSettingsUpdate{}, err
	}

	settings := payload.SystemSettings
	// Never trust the internal persistence projection from a client. Every
	// accepted update receives a fresh cryptographically random revision.
	settings.Revision = ""
	settings.Notifications.Webhooks = make([]NotificationWebhookSettings, len(payload.Notifications.Webhooks))
	for i := range payload.Notifications.Webhooks {
		settings.Notifications.Webhooks[i] = payload.Notifications.Webhooks[i].NotificationWebhookSettings
	}
	return decodedSystemSettingsUpdate{
		Settings:         settings,
		ExpectedRevision: strings.TrimSpace(payload.ExpectedRevision),
	}, nil
}

const systemSettingsRevisionPrefix = "ss-"

// systemSettingsRevision returns the nonce persisted atomically with the
// settings document. Legacy documents predate that field, so they receive a
// deterministic hash of the redacted representation until their first write
// persists a random nonce. No revision is ever derived from secret material.
func systemSettingsRevision(settings SystemSettings) string {
	if validSystemSettingsRevision(settings.Revision) {
		return settings.Revision
	}
	encoded, err := json.Marshal(redactedSystemSettingsResponse(settings))
	if err != nil {
		// SystemSettings contains no unsupported JSON values. Keep a stable,
		// disclosure-safe fallback if that invariant is ever broken.
		return systemSettingsRevisionPrefix + fmt.Sprintf("%x", sha256.Sum256(nil))
	}
	return systemSettingsRevisionPrefix + fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func newSystemSettingsRevision() (string, error) {
	var random [sha256.Size]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return systemSettingsRevisionPrefix + hex.EncodeToString(random[:]), nil
}

func systemSettingsETag(revision string) string {
	return strconv.Quote(strings.TrimSpace(revision))
}

func parseSystemSettingsIfMatch(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if value == "*" || strings.HasPrefix(value, "W/") || strings.Contains(value, ",") {
		return "", fmt.Errorf("If-Match must contain one strong system-settings ETag")
	}
	revision, err := strconv.Unquote(value)
	if err != nil || !validSystemSettingsRevision(revision) {
		return "", fmt.Errorf("If-Match must contain the quoted ETag returned by GET /api/v1/system/settings")
	}
	return revision, nil
}

func validSystemSettingsRevision(revision string) bool {
	revision = strings.TrimSpace(revision)
	if len(revision) != len(systemSettingsRevisionPrefix)+sha256.Size*2 ||
		!strings.HasPrefix(revision, systemSettingsRevisionPrefix) {
		return false
	}
	for _, char := range revision[len(systemSettingsRevisionPrefix):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// systemSettingsBeforeCommitError distinguishes an audit/precondition failure
// from validation, persistence, and optimistic-concurrency failures returned by
// TestRunner.
type systemSettingsBeforeCommitError struct {
	err error
}

func (e *systemSettingsBeforeCommitError) Error() string {
	return fmt.Sprintf("system settings pre-commit check: %v", e.err)
}

func (e *systemSettingsBeforeCommitError) Unwrap() error {
	return e.err
}

type testRunnerSystemSettingsService struct {
	runner *TestRunner
}

func newTestRunnerSystemSettingsService(runner *TestRunner) systemSettingsService {
	return &testRunnerSystemSettingsService{runner: runner}
}

func (s *testRunnerSystemSettingsService) Get() SystemSettings {
	return s.runner.GetSystemSettings()
}

func (s *testRunnerSystemSettingsService) Update(requested SystemSettings, rawBody []byte, expectedRevision string, beforeCommit systemSettingsBeforeCommit) (systemSettingsUpdateResult, error) {
	// Settings PUT is a read-modify-write operation. Force the durable refresh so
	// a stale HA replica cannot merge against, then overwrite, a newer snapshot.
	s.runner.refreshSystemSettingsFromStore(true)
	current, internalRevision := s.runner.systemSettingsSnapshot()
	if systemSettingsRevision(current) != strings.TrimSpace(expectedRevision) {
		return systemSettingsUpdateResult{}, errSystemSettingsConflict
	}
	candidate := mergeOmittedSystemSettingsSections(requested, current, rawBody)
	candidate = preserveWriteOnlySystemSettingsSecrets(candidate, current)
	previousCredentialCount := systemSettingsNotificationCredentialCount(current.Notifications)
	candidateCredentialCount := systemSettingsNotificationCredentialCount(candidate.Notifications)

	result := systemSettingsUpdateResult{systemSettingsUpdatePlan: systemSettingsUpdatePlan{
		Previous:                       current,
		Candidate:                      candidate,
		PolicyChanged:                  current.ScriptImports.AllowSourceSecretMigration != candidate.ScriptImports.AllowSourceSecretMigration,
		NotificationCredentialsChanged: systemSettingsNotificationCredentialsChanged(current.Notifications, candidate.Notifications),
		PreviousCredentialCount:        previousCredentialCount,
		CandidateCredentialCount:       candidateCredentialCount,
	}}
	if beforeCommit != nil {
		if err := beforeCommit(result.systemSettingsUpdatePlan); err != nil {
			return result, &systemSettingsBeforeCommitError{err: err}
		}
	}

	updated, err := s.runner.updateSystemSettings(candidate, internalRevision, true)
	if err != nil {
		return result, err
	}
	result.Updated = updated
	return result, nil
}

func systemSettingsNotificationCredentialCount(settings NotificationsSettings) int {
	count := 0
	for _, webhook := range settings.Webhooks {
		if strings.TrimSpace(webhook.APIToken) != "" {
			count++
		}
	}
	return count
}

// systemSettingsNotificationCredentialsChanged compares credentials as an
// order-independent multiset. Only a one-way token digest is retained in the
// temporary comparison key; neither the update plan nor any audit detail ever
// contains credential material.
func systemSettingsNotificationCredentialsChanged(previous, candidate NotificationsSettings) bool {
	fingerprint := func(settings NotificationsSettings) map[string]int {
		out := make(map[string]int)
		for _, webhook := range settings.Webhooks {
			if strings.TrimSpace(webhook.APIToken) == "" {
				continue
			}
			tokenDigest := sha256.Sum256([]byte(webhook.APIToken))
			key := notificationWebhookStableIdentity(webhook) + "\x00" + hex.EncodeToString(tokenDigest[:])
			out[key]++
		}
		return out
	}
	previousFingerprint := fingerprint(previous)
	candidateFingerprint := fingerprint(candidate)
	if len(previousFingerprint) != len(candidateFingerprint) {
		return true
	}
	for key, count := range previousFingerprint {
		if candidateFingerprint[key] != count {
			return true
		}
	}
	return false
}

// mergeOmittedSystemSettingsSections gives the monolithic PUT
// "replace the sections you sent" semantics: every top-level section the raw
// body omits is carried over from the stored settings. A client that omits a
// section (e.g. one predating the feature) must not zero out its stored
// configuration — for several sections the zero values normalize to defaults
// and validate clean, so without these merges the wipe would be silent
// (omitted thresholds discarded the SLA/fleet thresholds and disabled the
// error-rate auto-stop; omitted notifications deleted every webhook). To reset
// a section, send it explicitly (e.g. "thresholds": {}).
// TestMergeOmittedSystemSettingsSectionsIsComplete fails when a new
// SystemSettings section is added without a merge entry here.
func mergeOmittedSystemSettingsSections(requested SystemSettings, current SystemSettings, rawBody []byte) SystemSettings {
	merged := cloneSystemSettings(requested)
	raw := string(rawBody)
	if !rawJSONHasKey(raw, "general") {
		merged.General = current.General
	}
	if !rawJSONHasKey(raw, "thresholds") {
		merged.Thresholds = current.Thresholds
	}
	if !rawJSONHasKey(raw, "capacity_planning") {
		merged.CapacityPlanning = current.CapacityPlanning
	}
	if !rawJSONHasKey(raw, "archive_retention") {
		merged.ArchiveRetention = current.ArchiveRetention
	}
	if !rawJSONHasKey(raw, "schedule_defaults") {
		merged.ScheduleDefaults = current.ScheduleDefaults
	}
	if !rawJSONHasKey(raw, "notifications") {
		merged.Notifications = current.Notifications
	}
	if !rawJSONHasKey(raw, "auth") {
		merged.Auth = current.Auth
	}
	if !rawJSONHasKey(raw, "github") {
		merged.GitHub = current.GitHub
	}
	if !rawJSONHasKey(raw, "prometheus") {
		merged.Prometheus = current.Prometheus
	}
	if !rawJSONHasKey(raw, "client_app") {
		merged.ClientApp = current.ClientApp
	}
	if !rawJSONHasKey(raw, "client_downloads") {
		merged.ClientDownloads = current.ClientDownloads
	}
	if !rawJSONHasKey(raw, "script_imports") {
		merged.ScriptImports = current.ScriptImports
	}
	if !rawJSONHasKey(raw, "powerbi") {
		merged.PowerBI = current.PowerBI
	}
	if !rawJSONHasKey(raw, "grafana") {
		merged.Grafana = current.Grafana
	}
	return merged
}

func (s *Server) systemSettingsDomainService() systemSettingsService {
	if s.settingsService != nil {
		if adapter, ok := s.settingsService.(*testRunnerSystemSettingsService); ok && adapter.runner != s.runner {
			// A few focused internal tests historically replace Server.runner after
			// construction. Keep that compatibility without changing production
			// constructor behavior or overriding an explicitly injected test service.
			return newTestRunnerSystemSettingsService(s.runner)
		}
		return s.settingsService
	}
	// Preserve compatibility for focused tests and internal callers that build a
	// Server literal with only the historical runner field.
	return newTestRunnerSystemSettingsService(s.runner)
}
