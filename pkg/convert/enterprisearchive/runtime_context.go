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
	usp := ""
	if rc != nil {
		usp = rc.selectedRunLogicFile
	}
	streamAbsent, syncAbsent := runtimeSourceOperationsAbsent(files, protocol)
	for file, sections := range dispositions {
		_, source, _ := findRuntimeArchiveFile(files, file)
		authConflict, paceConflict := false, false
		for _, conflict := range runtimeConfigCaseFoldConflicts(source) {
			authConflict = authConflict || (runtimeConfigCanonicalSection(conflict.section) == "http" && strings.EqualFold(conflict.key, "IntegratedAuthentication"))
			paceConflict = paceConflict || (runtimeConfigCanonicalSection(conflict.section) == "runlogicrunroot" && strings.EqualFold(conflict.key, "RunLogicPaceType"))
		}
		for section, keys := range sections {
			for key, row := range keys {
				if authConflict && strings.HasPrefix(row.Note, "Saved authentication option is inactive") {
					keys[key] = RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, Note: "Conflicting IntegratedAuthentication selectors prevent determining whether this saved authentication option is active; review the source policy."}
					continue
				}
				if paceConflict && strings.HasPrefix(row.Note, "Saved pacing bound is inactive") {
					keys[key] = RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, ParityID: "pacing", Fields: []string{"config.pacing"}, Note: "Conflicting RunLogicPaceType selectors prevent determining the active pacing bounds."}
					continue
				}
				note := ""
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
	if protocol != "http" && protocol != "http/1.1" && protocol != "http/2" && protocol != "soap" {
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
					hasStream = hasStream || strings.HasPrefix(strings.ToLower(call.Name), "web_stream")
					hasSync = hasSync || strings.EqualFold(call.Name, "web_sync")
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
				hasStream = hasStream || strings.HasPrefix(strings.ToLower(token.text), "web_stream")
				hasSync = hasSync || strings.EqualFold(token.text, "web_sync")
			}
			inComment := false
			for line := range strings.SplitSeq(content, "\n") {
				code := strings.ToLower(stripCComments(line, &inComment))
				if strings.HasPrefix(strings.TrimSpace(code), "#") {
					hasStream = hasStream || strings.Contains(code, "web_stream")
					hasSync = hasSync || strings.Contains(code, "web_sync")
					if strings.Contains(code, "##") || strings.HasSuffix(strings.TrimSpace(code), "\\") {
						complete = false
					}
				}
			}
		}
	}
	return seen && complete && !hasStream, seen && complete && !hasSync
}
