package api

import "fmt"

// systemSettingsService is the narrow settings domain boundary used by the HTTP
// handlers. The TestRunner adapter below preserves existing construction while
// keeping HA refresh, partial-update merging, and revision-fenced persistence out
// of the transport layer.
type systemSettingsService interface {
	Get() SystemSettings
	Update(requested SystemSettings, rawBody []byte, beforeCommit systemSettingsBeforeCommit) (systemSettingsUpdateResult, error)
}

type systemSettingsBeforeCommit func(systemSettingsUpdatePlan) error

type systemSettingsUpdatePlan struct {
	Previous      SystemSettings
	Candidate     SystemSettings
	PolicyChanged bool
}

type systemSettingsUpdateResult struct {
	systemSettingsUpdatePlan
	Updated SystemSettings
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

func (s *testRunnerSystemSettingsService) Update(requested SystemSettings, rawBody []byte, beforeCommit systemSettingsBeforeCommit) (systemSettingsUpdateResult, error) {
	// Settings PUT is a read-modify-write operation. Force the durable refresh so
	// a stale HA replica cannot merge against, then overwrite, a newer snapshot.
	s.runner.refreshSystemSettingsFromStore(true)
	current, expectedRevision := s.runner.systemSettingsSnapshot()
	candidate := mergeOmittedSystemSettingsSections(requested, current, rawBody)
	candidate = preserveWriteOnlySystemSettingsSecrets(candidate, current)

	result := systemSettingsUpdateResult{systemSettingsUpdatePlan: systemSettingsUpdatePlan{
		Previous:      current,
		Candidate:     candidate,
		PolicyChanged: current.ScriptImports.AllowSourceSecretMigration != candidate.ScriptImports.AllowSourceSecretMigration,
	}}
	if beforeCommit != nil {
		if err := beforeCommit(result.systemSettingsUpdatePlan); err != nil {
			return result, &systemSettingsBeforeCommitError{err: err}
		}
	}

	updated, err := s.runner.updateSystemSettings(candidate, expectedRevision, true)
	if err != nil {
		return result, err
	}
	result.Updated = updated
	return result, nil
}

func mergeOmittedSystemSettingsSections(requested SystemSettings, current SystemSettings, rawBody []byte) SystemSettings {
	merged := cloneSystemSettings(requested)
	raw := string(rawBody)
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
