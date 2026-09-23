package enterprisearchive

import "strings"

func validThinkTimeMode(mode string) bool {
	switch mode {
	case "NOTHINK", "RECORDED", "MULTIPLY", "RANDOM":
		return true
	}
	return false
}

// Only documented, valid selectors prove inactivity. In particular an arbitrary
// zero, "default", or a missing parent is not evidence that a setting is unused.
func runtimeCompatibilityInactive(section, key, value string, sections map[string]map[string]string) (bool, string) {
	parent := ""
	switch key {
	case "automaticnestedtransactions", "automatictransactionsperfunc":
		parent = "AutomaticTransactions"
	case "clearcacheforsimulatenewuser":
		parent = "SimulateNewUser"
	case "jsfortransportruntimesize", "jsfortransportstacksize":
		parent = "EnableJsForTransport"
	case "logmaxresponsesize":
		parent = "LogEnableResponseLimit"
	case "autologbuffersize":
		parent = "AutoLog"
	case "prefetchprerendercallbacktimerintervalms":
		parent = "SimulatePrefetchPrerender"
	}
	if parent != "" {
		found := false
		for name, rows := range sections {
			if runtimeConfigCanonicalSection(name) != section {
				continue
			}
			for k, v := range rows {
				if compactRuntimeEnum(k) != compactRuntimeEnum(parent) {
					continue
				}
				enabled, valid := parseBoolExplicit(v)
				if !valid || enabled {
					return false, ""
				}
				found = true
			}
		}
		return found, "Inactive because " + parent + " is explicitly disabled."
	}
	// These positive enable switches have known off semantics. Do not extend
	// this list by interpreting a key's spelling or a numeric enum.
	switch key {
	case "accessvtsportbyquerystring", "loadchilengine", "integratedauthentication", "utf8inputoutput", "enabledynatrace", "usedataformatextensions", "enablejsfortransport", "wininetreplay", "logfilewrite", "logfilewritetracetofile":
		enabled, valid := parseBoolExplicit(value)
		return valid && !enabled, "Explicitly disabled in the source (" + key + "=" + value + ")."
	case "supportcharset":
		if strings.EqualFold(strings.TrimSpace(value), "None") {
			return true, "SupportCharset=None requests no charset conversion."
		}
	case "filtertype":
		rows := findMergedSection(sections, "FILTERS", "Download Filters", "DownloadFilters")
		if runtimeSectionValue(rows, "IncludeFiltersInList") == "" && runtimeSectionValue(rows, "ExcludeFiltersInList") == "" {
			return true, "Both source download-filter lists are empty."
		}
	}
	return false, ""
}

// Reasons are additive metadata; old saved dispositions remain untouched until
// a fresh import. The explanatory note retains the exact source context.
func explainRuntimeDisposition(row RuntimeSettingDisposition, section, key string) RuntimeSettingDisposition {
	if row.Reason != "" {
		return row
	}
	if row.Status == RuntimeSettingDispositionCompatibilityOnly {
		row.Reason = "unverified_semantics"
		return row
	}
	if row.Status != RuntimeSettingDispositionNonApplicable {
		return row
	}
	note := strings.ToLower(row.Note)
	switch {
	case strings.Contains(note, "superseded"):
		row.Reason = "superseded_setting"
	case strings.Contains(note, "protocol") || strings.Contains(note, "browser dom"):
		row.Reason = "protocol_mismatch"
	case strings.Contains(note, "no web_") || strings.Contains(note, "no websocket"):
		row.Reason = "operation_absent"
	case strings.Contains(note, "mode") || strings.Contains(note, "pacing bound"):
		row.Reason = "unused_mode_value"
	case strings.Contains(note, "disabled") || strings.Contains(note, " is off") || strings.Contains(note, "inactive") || strings.Contains(note, "empty"):
		row.Reason = "disabled_feature"
	default:
		row.Reason = "source_tool_metadata"
	}
	compact := compactRuntimeEnum(key)
	if runtimeConfigCanonicalSection(section) == "logging" || strings.HasPrefix(compact, "graph") || strings.HasPrefix(compact, "print") || compact == "streamlog" {
		row.Reason = "apex_managed_behavior"
		row.Note += " APEX uses its own logging and metrics policies; this does not disable native telemetry."
	}
	return row
}

func runtimeInactiveParentKey(section, key string) string {
	switch compactRuntimeEnum(key) {
	case "jsfortransportruntimesize", "jsfortransportstacksize":
		return "enablejsfortransport"
	case "automaticnestedtransactions", "automatictransactionsperfunc", "autotransfileline":
		return "automatictransactions"
	case "thinktimerandomlow", "thinktimerandomhigh", "factor":
		return "options"
	case "limit":
		return "limitflag"
	case "modemspeed":
		return "enablemodemspeed"
	case "custommodemspeed":
		return "enablecustommodemspeed"
	case "prefetchprerendercallbacktimerintervalms":
		return "simulateprefetchprerender"
	case "logmaxresponsesize":
		return "logenableresponselimit"
	case "proxyautoconfigscripturl":
		return "proxyuseautoconfigscript"
	case "autologbuffersize":
		return "autolog"
	case "clearcacheforsimulatenewuser":
		return "simulatenewuser"
	}
	return ""
}
