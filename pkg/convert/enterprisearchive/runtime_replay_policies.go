package enterprisearchive

import (
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func replayPolicyDefinitions() []runtimeSettingSupportDefinition {
	http := []string{"HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web"}
	makeDefinition := func(id, area, concept, field, note string, sections, keys []string) runtimeSettingSupportDefinition {
		return runtimeSettingSupportDefinition{entry: RuntimeSettingSupportEntry{ID: id, Area: area, Concept: concept, Fields: []string{field}, Status: RuntimeSettingStatusExecuted, Notes: note}, matchers: []runtimeSettingMatcher{matcher(sections, keys)}}
	}
	definitions := []runtimeSettingSupportDefinition{
		makeDefinition("http_status_policy", "HTTP", "HTTP error policy", "config.http.http_errors_as_warnings", "Explicit HTTP status policy; authored assertions still execute.", http, []string{"HttpErrorsAsWarnings"}),
		makeDefinition("http_resource_policy", "HTTP", "Non-critical resource failure policy", "config.http.fail_noncritical_resources", "Controls embedded-resource failure propagation; warnings remain in protocol evidence.", http, []string{"FailNonCriticalItem"}),
		makeDefinition("http_resource_timeout_policy", "HTTP", "Resource timeout policy", "config.http.resource_timeout_as_warning", "Only resource-stage timeouts may become warnings; cancellation and main-request timeouts remain errors.", http, []string{"ResourcePageTimeoutIsWarning"}),
		makeDefinition("http_legacy_checks", "HTTP", "Legacy HTML checks", "config.http.legacy_html_checks", "Controls imported web_find and web_image_check execution; registered searches and authored assertions remain enabled.", http, []string{"EnableChecks"}),
		makeDefinition("http_dns_cache", "HTTP", "Per-VUser DNS cache", "config.http.dns_cache", "Optional per-VUser address cache: 256 hosts, 16 addresses per host, 60-second lifetime. OS and upstream caches remain independent.", http, []string{"EnableIPCache"}),
		makeDefinition("http_auth_delay", "HTTP", "Authentication challenge delay", "config.http.challenge_delay_ms", "Delay before native Digest challenge retry; no delay for unrelated 401 responses. Source units require explicit review for nonzero values.", http, []string{"Retry401ThinkTime"}),
		makeDefinition("runtime_fail_error_message", "Error handling", "Fail open transactions on error message", "config.error_handling.fail_transaction_on_error_message", "Marks open source transactions failed without interrupting control flow.", []string{"General"}, []string{"FailTransOnErrorMsg"}),
		makeDefinition("http_new_user_cache", "Browser emulation", "Clear cache for a new user", "config.browser_emulation.cache.clear_cache_for_new_user", "Separates cache clearing from cookie reset when new-user simulation is enabled.", http, []string{"ClearCacheForSimulateNewUser"}),
		makeDefinition("runtime_response_log_limit", "Logging", "Response-log byte limit", "config.log.max_response_bytes", "Limits redacted response log output within engine evidence bounds; zero uses the engine bound.", http, []string{"LogEnableResponseLimit", "LogMaxResponseSize"}),
	}
	for _, row := range []struct{ id, concept, field, key string }{
		{"runtime_replay_snapshots", "Replay snapshots", "config.results.message_detail", "EnableSnapshotsInReplay"},
		{"runtime_snapshot_resources", "Snapshot resources", "config.results.message_detail.include_resources", "SaveSnapshotResources"},
	} {
		d := makeDefinition(row.id, "Diagnostics", row.concept, row.field, "Bounded APEX HTTP exchange evidence for replay and load tests; not LoadRunner snapshot files or visual rendering. Resource evidence shares the parent retention budget.", http, []string{row.key})
		d.entry.Status = RuntimeSettingStatusPartial
		definitions = append(definitions, d)
	}
	return definitions
}

func parseRuntimeReplayPolicies(rc *RuntimeConfig, sections map[string]map[string]string) {
	http := findMergedSection(sections, "HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web")
	general := findSection(sections, "General")
	issue := func(id, key, note string) {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{ID: id, Area: "Runtime", sourceKey: strings.ToLower(key), Notes: note})
	}
	boolean := func(section map[string]string, key, id string) *bool {
		raw, present := sectionValueCaseInsensitive(section, key)
		if !present {
			return nil
		}
		value, valid := parseBoolExplicit(raw)
		if !valid {
			issue(id, key, "Invalid boolean source setting; choose an explicit enabled or disabled value.")
			return nil
		}
		return &value
	}
	capture := boolean(http, "EnableSnapshotsInReplay", "runtime_replay_snapshots")
	resources := boolean(http, "SaveSnapshotResources", "runtime_snapshot_resources")
	if capture != nil {
		policy := &dsl.MessageDetailConfig{Mode: "errors", IncludeResources: resources}
		if *capture {
			policy.Mode, policy.SampleRate = "sampled", 1
			policy.MaxItems, policy.MaxItemBytes, policy.MaxTotalBytes = 128, 1<<20, 16<<20
		}
		rc.runtimePolicies.Results.MessageDetail = policy
	} else if resources != nil {
		issue("runtime_snapshot_resources", "SaveSnapshotResources", "Snapshot resource preference was preserved but no valid EnableSnapshotsInReplay selector was supplied; capture was not enabled.")
	}
	rc.httpCompatibility.HTTPErrorsAsWarnings = boolean(http, "HttpErrorsAsWarnings", "http_status_policy")
	rc.httpCompatibility.FailNonCriticalResources = boolean(http, "FailNonCriticalItem", "http_resource_policy")
	rc.httpCompatibility.ResourceTimeoutAsWarning = boolean(http, "ResourcePageTimeoutIsWarning", "http_resource_timeout_policy")
	rc.httpCompatibility.LegacyHTMLChecks = boolean(http, "EnableChecks", "http_legacy_checks")
	rc.httpCompatibility.DNSCache = boolean(http, "EnableIPCache", "http_dns_cache")
	rc.runtimePolicies.ErrorHandling.FailTransactionOnErrorMessage = boolean(general, "FailTransOnErrorMsg", "runtime_fail_error_message")
	cache := boolean(http, "ClearCacheForSimulateNewUser", "http_new_user_cache")
	if cache != nil {
		rc.runtimePolicies.BrowserEmulation = &dsl.BrowserEmulationConfig{Cache: &dsl.BrowserCacheSimulationConfig{ClearCacheForNewUser: cache}}
	}
	if raw, present := sectionValueCaseInsensitive(http, "Retry401ThinkTime"); present {
		if v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err != nil || v != 0 {
			issue("http_auth_delay", "Retry401ThinkTime", "Nonzero Retry401ThinkTime units are not verified. Select the native Digest challenge delay in milliseconds; no source duration was guessed.")
		}
	}
	enabled := boolean(http, "LogEnableResponseLimit", "runtime_response_log_limit")
	if enabled != nil && *enabled {
		raw, _ := sectionValueCaseInsensitive(http, "LogMaxResponseSize")
		// Source versions differ in display units; preserve nonzero limits for review.
		issue("runtime_response_log_limit", "LogMaxResponseSize", "Source response-log size units are not verified. Select a byte limit from 1 through 1048576; source value "+boundedRuntimeReviewEvidence(raw)+" was not applied.")
	}
}
