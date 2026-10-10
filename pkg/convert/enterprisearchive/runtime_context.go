package enterprisearchive

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Zero in the destination means its default, not an unlimited source timeout.
// Reject unrepresentable source values before multiplying, including overflow.
func sourceKeepAliveTimeoutMS(raw string) (int, bool) {
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || seconds <= 0 || seconds > dsl.MaxConfiguredProtocolTimeoutMS/1000 {
		return 0, false
	}
	return seconds * 1000, true
}

// Missing, malformed, or contradictory selectors do not prove inactivity.
func runtimeHTTPFeatureExplicitlyDisabled(sections map[string]map[string]string, feature string) bool {
	found := false
	for sectionName, keys := range sections {
		if runtimeConfigCanonicalSection(sectionName) != "http" {
			continue
		}
		for key, value := range keys {
			if !strings.EqualFold(strings.TrimSpace(key), feature) {
				continue
			}
			enabled, valid := parseBoolExplicit(value)
			if !valid || enabled {
				return false
			}
			found = true
		}
	}
	return found
}

// Refinement uses the selected profile identity and source corpus, not just an
// individual INI row. Never erase source evidence; explain why it is dormant.
func refineRuntimeSettingDispositions(dispositions map[string]map[string]map[string]RuntimeSettingDisposition, rc *RuntimeConfig, files map[string]string, protocol string) {
	defer func() {
		for _, sections := range dispositions {
			for section, keys := range sections {
				for key, row := range keys {
					keys[key] = explainRuntimeDisposition(row, section, key)
				}
			}
		}
	}()
	usp := ""
	if rc != nil {
		usp = rc.selectedRunLogicFile
	}
	streamAbsent, syncAbsent := runtimeSourceOperationsAbsent(files, protocol)
	wsAbsent, socketAbsent := runtimeSourceOperationsAbsentMatching(files, protocol, "web_websocket", "web_socket")
	for file, sections := range dispositions {
		_, source, _ := findRuntimeArchiveFile(files, file)
		authConflict, paceConflict := false, false
		conflicts := runtimeConfigCaseFoldConflicts(source)
		for _, conflict := range conflicts {
			authConflict = authConflict || (runtimeConfigCanonicalSection(conflict.section) == "http" && strings.EqualFold(conflict.key, "IntegratedAuthentication"))
			paceConflict = paceConflict || (runtimeConfigCanonicalSection(conflict.section) == "runlogicrunroot" && strings.EqualFold(conflict.key, "RunLogicPaceType"))
		}
		for section, keys := range sections {
			for key, row := range keys {
				if rc != nil && (row.ParityID == "runtime_replay_snapshots" || row.ParityID == "runtime_snapshot_resources") {
					for _, issue := range rc.RuntimeIssues {
						if issue.ID == row.ParityID {
							row.Status = RuntimeSettingDispositionCompatibilityOnly
							row.Reason = "unverified_semantics"
							row.Note = issue.Notes
							keys[key] = row
							break
						}
					}
				}
				if row.Status == RuntimeSettingDispositionNonApplicable {
					ambiguous := false
					parent := runtimeInactiveParentKey(section, key)
					for _, conflict := range conflicts {
						if parent != "" && compactRuntimeEnum(conflict.key) == parent {
							ambiguous = true
						}
					}
					if ambiguous {
						keys[key] = RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, Reason: "unverified_semantics", Note: "Conflicting " + parent + " selectors prevent proving this saved value inactive; review the selected source profile."}
						continue
					}
				}
				if authConflict && strings.HasPrefix(row.Note, "Saved authentication option is inactive") {
					keys[key] = RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, Note: "Conflicting IntegratedAuthentication selectors prevent determining whether this saved authentication option is active; review the source policy."}
					continue
				}
				if paceConflict && strings.HasPrefix(row.Note, "Saved pacing bound is inactive") {
					keys[key] = RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, ParityID: "pacing", Fields: []string{"config.pacing"}, Note: "Conflicting RunLogicPaceType selectors prevent determining the active pacing bounds."}
					continue
				}
				if (runtimeConfigCanonicalSection(section) == "general" && strings.EqualFold(key, "Encoding")) || (runtimeConfigCanonicalSection(section) == "http" && strings.EqualFold(key, "RecorderWinCodePage")) {
					selected := rcSections(files, rc)
					if strings.EqualFold(runtimeSectionValue(findSection(selected, "General"), "Encoding"), "ANSI") && runtimeSectionValue(findMergedSection(selected, "WEB", "Web"), "RecorderWinCodePage") == "1252" {
						keys[key] = RuntimeSettingDisposition{Status: RuntimeSettingDispositionMapped, Note: "Consumed during strict Windows-1252 text decoding; request body bytes and binary assets are preserved."}
						continue
					}
				}
				note := ""
				if nativeHTTPCompatibilityProtocol(protocol) && runtimeConfigCanonicalSection(section) == "http" {
					switch strings.ToLower(key) {
					case "resetcontext":
						note = "Recording/code-generation context option; does not reset native replay iterations."
					case "startrecordinggmt", "startrecordingisdst":
						note = "Recording provenance; not used by ordinary native HTTP replay."
					case "navigatorsystemlanguage", "screenavailwidth", "screenavailheight":
						note = "DOM emulation property; native HTTP replay has no browser DOM."
					case "proxyautoconfigscripturl":
						if rc != nil && runtimeHTTPFeatureExplicitlyDisabled(rcSections(files, rc), "ProxyUseAutoConfigScript") {
							note = "Inactive saved PAC URL: automatic proxy configuration is explicitly disabled."
						}
					case "websocketcallbacktimerintervalms":
						if wsAbsent && socketAbsent {
							note = "Inactive: no WebSocket operation or indirect reference exists in the inspected C source."
						}
					}
				}
				if row.ParityID == "browser_device_emulation" && !strings.EqualFold(strings.TrimSpace(protocol), "browser") {
					row.Status = RuntimeSettingDispositionCompatibilityOnly
					row.Note = "This control executes only in the browser runtime; this protocol has no browser DOM."
					if nativeHTTPCompatibilityProtocol(protocol) && runtimeConfigCanonicalSection(section) == "http" && (strings.EqualFold(key, "ScreenWidth") || strings.EqualFold(key, "ScreenHeight")) {
						row.Status = RuntimeSettingDispositionNonApplicable
						row.Note = "Recorded screen dimensions; native HTTP replay has no browser viewport."
					}
					keys[key] = row
				}
				if row.ParityID == "browser_selection" && rc != nil {
					if emulation := rc.ToBrowserEmulationConfig(); emulation != nil && emulation.Mode == "custom" {
						row.Status = RuntimeSettingDispositionNonApplicable
						row.Note = "Saved browser identity is dormant because the selected custom User-Agent is authoritative."
						keys[key] = row
					}
				}
				if nativeHTTPCompatibilityProtocol(protocol) && runtimeConfigCanonicalSection(section) == "http" && strings.EqualFold(key, "UseBrowserAgent") && runtimeHTTPFeatureExplicitlyDisabled(ParseConfigSections(source), "UseBrowserAgent") {
					note = "Recorded-browser User-Agent inheritance is explicitly disabled; the selected custom User-Agent or browser identity is authoritative."
				}
				if strings.EqualFold(key, "ProxyPasswordIsEncrypted") && runtimeConfigCanonicalSection(section) == "http" {
					keys[key] = RuntimeSettingDisposition{Status: RuntimeSettingDispositionMapped, ParityID: "proxy", Fields: []string{"config.http.proxy.password"}, Note: "Credential encoding marker consumed by the importer; provision the protected proxy password through Credentials."}
					continue
				}
				if strings.EqualFold(key, "AutoLogBufferSize") && !parseBool(runtimeSectionValue(ParseConfigSections(source)[section], "AutoLog")) {
					// Only a valid explicit false selector proves inactivity.
					if active, valid := parseBoolExplicit(runtimeSectionValue(ParseConfigSections(source)[section], "AutoLog")); valid && !active {
						note = "Error history capacity is inactive because AutoLog is disabled."
					}
				}
				if row.ParityID == "http_cache_eviction" && rc != nil && rc.BESimulateCache != nil && !*rc.BESimulateCache {
					note = "The source cache eviction policy is dormant because browser-cache simulation is disabled."
				}
				if usp != "" && !strings.EqualFold(file, usp) && (row.ParityID == "pacing" || row.ParityID == "run_logic_iterations") {
					note = fmt.Sprintf("Superseded by selected run-logic profile %s; cfg pacing and iteration values are not used, even when the selected profile requires repair.", boundedRuntimeReviewEvidence(usp))
				}
				canonicalSection, compactKey := runtimeConfigCanonicalSection(section), compactRuntimeEnum(key)
				if canonicalSection == "http" && compactKey == "prefetchprerendercallbacktimerintervalms" && rc != nil && rc.BESimulatePrefetch != nil && !*rc.BESimulatePrefetch {
					note = "Saved prefetch callback interval is inactive because prefetch/prerender simulation is disabled."
				}
				if canonicalSection == "streaming" && streamAbsent && (compactKey == "streamtimeout" || compactKey == "streamretrytime" || compactKey == "streamseekmethod") {
					note = "Saved streaming option is inactive: no web_stream operation is present in the inspected C source."
				}
				if canonicalSection == "http" && syncAbsent && (compactKey == "websyncretryintervalms" || compactKey == "websyncretrytimeoutms") {
					note = "Saved synchronization option is inactive: no web_sync operation is present in the inspected C source."
				}
				if note != "" {
					keys[key] = RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: note}
				}
			}
		}
	}
}

// This is a conservative absence check, not a control-flow reachability proof.
// Inspect all C/header files, including helpers. Unparseable or mixed-language
// source leaves the warning intact. Macro and function-pointer references keep it, while
// generated SDK function declarations are not mistaken for operations.
func runtimeSourceOperationsAbsent(files map[string]string, protocol string) (stream, sync bool) {
	return runtimeSourceOperationsAbsentMatching(files, protocol, "web_stream", "web_sync")
}
func rcSections(files map[string]string, rc *RuntimeConfig) map[string]map[string]string {
	// Resolve the selected cfg rather than whichever profile happens to appear first.
	if rc == nil {
		return nil
	}
	for name, content := range files {
		if strings.HasSuffix(strings.ToLower(name), ".cfg") {
			for _, selected := range rc.RuntimeFiles {
				if strings.EqualFold(name, selected) {
					return ParseConfigSections(content)
				}
			}
		}
	}
	return nil
}
func runtimeSourceOperationsAbsentMatching(files map[string]string, protocol, firstName, secondName string) (stream, sync bool) {
	if protocol != "http" && protocol != "http/1.1" && protocol != "http/2" && protocol != "http/3" && protocol != "soap" {
		return false, false
	}
	seen, complete, hasStream, hasSync := false, true, false, false
	for file, content := range files {
		switch strings.ToLower(path.Ext(file)) {
		case ".java", ".js", ".dll", ".so":
			complete = false
		case ".c", ".h":
			seen = true
			parsed, err := ParseActionFile(file, content)
			if err != nil || parsed == nil || len(parsed.ParseFailures) > 0 {
				complete = false
			}
			if parsed != nil {
				for _, call := range parsed.Functions {
					hasStream = hasStream || strings.HasPrefix(strings.ToLower(call.Name), firstName)
					hasSync = hasSync || strings.HasPrefix(strings.ToLower(call.Name), secondName)
				}
			}
			// A function can be passed or assigned without being called by name.
			// Keep those references reviewable without counting SDK prototypes:
			// their identifier is followed by an argument list. Direct calls are
			// covered by ParseActionFile above; literals/comments are not tokens.
			tokens := lexCTransactionTokens(content, true)
			for i, token := range tokens {
				if token.kind != cTransactionIdentifier || (i+1 < len(tokens) && tokens[i+1].text == "(") {
					continue
				}
				hasStream = hasStream || strings.HasPrefix(strings.ToLower(token.text), firstName)
				hasSync = hasSync || strings.HasPrefix(strings.ToLower(token.text), secondName)
			}
			inComment := false
			for line := range strings.SplitSeq(content, "\n") {
				code := strings.ToLower(stripCComments(line, &inComment))
				if strings.HasPrefix(strings.TrimSpace(code), "#") {
					hasStream = hasStream || strings.Contains(code, firstName)
					hasSync = hasSync || strings.Contains(code, secondName)
					if strings.Contains(code, "##") || strings.HasSuffix(strings.TrimSpace(code), "\\") {
						complete = false
					}
				}
			}
		}
	}
	return seen && complete && !hasStream, seen && complete && !hasSync
}
