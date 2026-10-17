package enterprisearchive

import (
	"path"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

func parseRuntimeHTTPCompatibility(rc *RuntimeConfig, sections map[string]map[string]string) {
	section := findMergedSection(sections, "HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web")
	issue := func(id, key, note string) {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{ID: id, Area: "HTTP", sourceKey: strings.ToLower(key), Notes: note})
	}
	for _, setting := range []struct {
		key, id string
		target  *int
		max     int
	}{
		{"NetBufSize", "http_receive_buffer", &rc.httpCompatibility.ReceiveBufferBytes, 1048576},
		{"MaxSelfMetaRefreshCount", "http_meta_refresh", &rc.httpCompatibility.MaxMetaRefresh, 50},
		{"MaxErrorMatchesAsERRORS", "http_content_check_limit", &rc.httpCompatibility.ContentCheckErrorLimit, 1000},
	} {
		if raw, ok := sectionValueCaseInsensitive(section, setting.key); ok {
			n, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || n < 1 || n > setting.max {
				issue(setting.id, setting.key, "Source value is outside the supported positive integer range; choose an explicit native replacement.")
			} else {
				*setting.target = n
			}
		}
	}
	log := findSection(sections, "Log", "Logging")
	if raw, ok := sectionValueCaseInsensitive(log, "AutoLogBufferSize"); ok && parseBool(runtimeSectionValue(log, "AutoLog")) {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < 1 || n > 1024 {
			issue("runtime_error_log_buffer", "AutoLogBufferSize", "Error log history requires 1..1024 KiB; source value was not clamped.")
		} else {
			rc.runtimePolicies.Log.ErrorBufferBytes = n * 1024
		}
	}
	if raw, present := sectionValueCaseInsensitive(section, "PageDownloadTimeout"); present {
		if ms, ok := sourceKeepAliveTimeoutMS(raw); ok {
			rc.httpCompatibility.PageTimeoutMs = ms
		} else {
			issue("http_page_timeout", "PageDownloadTimeout", "PageDownloadTimeout requires positive whole seconds up to 3600; no deadline was applied or clamped. Choose a supported whole-page timeout.")
		}
	}
	if raw, present := sectionValueCaseInsensitive(section, "IPVersionPolicy"); present {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "ipv4":
			rc.httpCompatibility.IPVersion = "ipv4"
		case "ipv6":
			rc.httpCompatibility.IPVersion = "ipv6"
		case "automatic", "auto":
			rc.httpCompatibility.IPVersion = "auto"
		default:
			issue("http_ip_version", "IPVersionPolicy", "The source IPVersionPolicy encoding is not verified. Select IPv4, IPv6, or automatic explicitly; no numeric enum was guessed. The policy controls outgoing TCP connections, including connections to proxies.")
		}
	}
	if raw, present := sectionValueCaseInsensitive(section, "DeleteCacheUnreferencedIterations"); present {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil && value >= 0 && value <= httpprotocol.MaxCacheUnusedIterations {
			rc.httpCompatibility.CacheMaxUnusedIterations = value
		} else {
			issue("http_cache_eviction", "DeleteCacheUnreferencedIterations", "Cache eviction requires an integer from 0 through 1000000; zero disables iteration aging. The source value was not applied.")
		}
	}
	if raw, present := sectionValueCaseInsensitive(section, "ParseHtmlContentType"); present {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "html", "text/html", `text\html`:
			rc.httpCompatibility.HTMLParseMode = "html"
		case "text":
			rc.httpCompatibility.HTMLParseMode = "text"
		case "any":
			rc.httpCompatibility.HTMLParseMode = "any"
		default:
			issue("http_html_parse", "ParseHtmlContentType", "ParseHtmlContentType requires HTML, TEXT, or ANY. Native HTML/XHTML discovery remains in effect until an explicit policy is selected.")
		}
	}
}

func nativeHTTPCompatibilityProtocol(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "http", "http/1.1", "http/2", "http/3", "soap", "java_http":
		return true
	}
	return false
}

func nativeHTTPCompatibilityID(id string) bool {
	switch id {
	case "runtime_replay_snapshots", "runtime_snapshot_resources", "http_receive_buffer", "http_meta_refresh", "http_content_check_limit", "http_status_policy", "http_resource_policy", "http_resource_timeout_policy", "http_legacy_checks", "http_dns_cache", "http_auth_delay", "http_new_user_cache", "runtime_fail_error_message", "runtime_response_log_limit", "http_page_timeout", "http_ip_version", "http_cache_eviction", "http_html_parse":
		return true
	}
	return false
}

// Avoid claiming native HTTP capabilities for external JVM/browser execution.
func scopeHTTPCompatibilityParity(entries []RuntimeSettingParityEntry, protocol string) []RuntimeSettingParityEntry {
	if !strings.EqualFold(strings.TrimSpace(protocol), "browser") {
		for i := range entries {
			if entries[i].ID == "browser_device_emulation" {
				entries[i].Status = RuntimeSettingStatusCompatibilityOnly
				entries[i].Notes = "Browser engine, device, touch, orientation, metrics, slow motion, and viewport controls execute only in the browser runtime. This protocol does not create a browser DOM."
			}
		}
	}
	if nativeHTTPCompatibilityProtocol(protocol) {
		return entries
	}
	for i := range entries {
		if nativeHTTPCompatibilityID(entries[i].ID) {
			entries[i].Status = RuntimeSettingStatusCompatibilityOnly
			entries[i].Notes = "This setting executes in the native HTTP transport; this protocol's runtime does not implement that policy."
		}
	}
	return entries
}

func currentSequentialProof(script *dsl.Script) bool {
	if script == nil || !nativeHTTPCompatibilityProtocol(script.Protocol) || script.Metadata == nil || script.Metadata.Import == nil || script.Config.RunLogic.BlockScheduling != "sequential" {
		return false
	}
	proof, _ := script.Metadata.Import.Preserved["native_sequential_run_logic"].(string)
	return proof != "" && proof == strings.Join(script.Config.RunLogic.ActionSequence, ",")
}

func promoteSequentialParity(entries []RuntimeSettingParityEntry) {
	for i := range entries {
		if entries[i].ID == "run_logic_action_sequence" || entries[i].ID == "run_logic_block_scheduling" {
			entries[i].Status = RuntimeSettingStatusExecuted
			entries[i].Notes = "Verified source profile matches the compiled native init/action/teardown lifecycle and its sequential action order. Custom block scheduling is not implied."
		}
	}
}

// Only certify the exact, flat lifecycle the C importer actually compiles.
// Missing, nested, ambiguous, or custom profile structures remain review-only.
func verifiedNativeSequentialProfile(rc *RuntimeConfig, project *ProjectConfig, files map[string]string, protocol string) bool {
	if rc == nil || !nativeHTTPCompatibilityProtocol(protocol) || rc.BlockScheduling != "sequential" || strings.Join(rc.ActionSequence, ",") != "vuser_init,action,vuser_end" {
		return false
	}
	for _, issue := range rc.RuntimeIssues {
		if strings.HasPrefix(issue.ID, "run_logic") && issue.Status != RuntimeSettingStatusNonApplicable {
			return false
		}
	}
	name, content, ambiguous := findSourceArchiveFile(files, rc.selectedRunLogicFile)
	if name == "" || len(ambiguous) > 0 || len(runtimeConfigCaseFoldConflicts(content)) > 0 {
		return false
	}
	sections := ParseConfigSections(content)
	names := splitSourceActionNames(runtimeSectionValue(findSection(sections, "Profile Actions"), "Profile Actions name"))
	declarations := orderedSourceActionFileDeclarations(project, files)
	if len(names) != 3 || len(declarations) != 3 {
		return false
	}
	roots := []string{"RunLogicInitRoot", "RunLogicRunRoot", "RunLogicEndRoot"}
	phases := []string{"init", "action", "teardown"}
	for i, declaration := range declarations {
		resolved, _, ambiguous := findSourceArchiveFile(files, declaration.Name)
		function := declaration.Function
		if function == "" {
			function = strings.TrimSuffix(path.Base(declaration.Name), path.Ext(declaration.Name))
		}
		if resolved == "" || len(ambiguous) > 0 || declaration.Phase != phases[i] || !strings.EqualFold(function, names[i]) {
			return false
		}
		root := findSection(sections, roots[i])
		for key := range root {
			switch strings.ToLower(key) {
			case "mercinitreefather", "mercinitreesectionname", "mercinitreesons", "name", "runlogicactionorder", "runlogicactiontype", "runlogicobjectkind", "runlogicrunmode", "runlogicnumofiterations":
			case "runlogicpacetype", "runlogicpaceconsttime", "runlogicpaceconstaftertime", "runlogicrandompacemin", "runlogicrandompacemax", "runlogicafterpacemin", "runlogicafterpacemax":
				if i != 1 {
					return false
				}
			default:
				return false
			}
		}
		value := func(key string) string { return strings.Trim(strings.TrimSpace(runtimeSectionValue(root, key)), `"`) }
		if !strings.EqualFold(value("RunLogicRunMode"), "Sequential") || !strings.EqualFold(value("RunLogicObjectKind"), "Group") || !strings.EqualFold(value("RunLogicActionOrder"), names[i]) || !strings.EqualFold(value("MercIniTreeSons"), names[i]) {
			return false
		}
		if i != 1 && value("RunLogicNumOfIterations") != "1" {
			return false
		}
		leafName := roots[i] + ":" + names[i]
		leaf := findSection(sections, leafName)
		for key := range leaf {
			switch strings.ToLower(key) {
			case "mercinitreefather", "mercinitreesectionname", "name", "runlogicactiontype", "runlogicobjectkind":
			default:
				return false
			}
		}
		if !strings.EqualFold(strings.Trim(runtimeSectionValue(leaf, "RunLogicObjectKind"), `"`), "Action") || strings.Trim(runtimeSectionValue(leaf, "MercIniTreeFather"), `"`) != roots[i] {
			return false
		}
		for section := range sections {
			if strings.HasPrefix(strings.ToLower(section), strings.ToLower(roots[i]+":")) && !strings.EqualFold(section, leafName) {
				return false
			}
		}
	}
	return true
}
