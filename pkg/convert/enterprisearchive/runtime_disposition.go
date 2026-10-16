package enterprisearchive

// runtime_disposition.go classifies every key of the selected runtime profile
// (.cfg + .usp) into an explicit disposition so no source runtime setting is
// silently discarded. The classification feeds two consumers:
//   - preserved["runtime_setting_dispositions"]: a per-file/section/key map the
//     dashboard renders next to the preserved raw sections; and
//   - two rollup parity entries (compatibility_only / non_applicable) so the
//     migration-readiness score counts settings APEX does not execute.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/scrub"
)

const maxRuntimeReviewEvidenceBytes = 256

// boundedRuntimeReviewEvidence keeps source-controlled labels from expanding a
// parity finding into a multi-megabyte response. It truncates on a UTF-8
// boundary so the resulting metadata remains valid JSON text.
func boundedRuntimeReviewEvidence(value string) string {
	value = strings.TrimSpace(scrub.Value(value, scrub.Policy{}))
	if len(value) <= maxRuntimeReviewEvidenceBytes {
		return value
	}
	prefix := value[:maxRuntimeReviewEvidenceBytes]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix + "…"
}

func boundedRuntimeReviewList(values []string) string {
	const maxItems = 20
	bounded := make([]string, 0, min(len(values), maxItems)+1)
	for index, value := range values {
		if index == maxItems {
			bounded = append(bounded, fmt.Sprintf("+%d more", len(values)-maxItems))
			break
		}
		bounded = append(bounded, boundedRuntimeReviewEvidence(value))
	}
	return strings.Join(bounded, ", ")
}

// RuntimeSettingDispositionStatus is the per-key classification vocabulary.
type RuntimeSettingDispositionStatus string

const (
	// RuntimeSettingDispositionMapped marks keys the importer consumes into an
	// APEX destination (possibly with review notes carried on the parity entry).
	RuntimeSettingDispositionMapped RuntimeSettingDispositionStatus = "mapped"
	// RuntimeSettingDispositionCompatibilityOnly marks keys with runtime
	// meaning in the source that APEX preserves but does not execute.
	RuntimeSettingDispositionCompatibilityOnly RuntimeSettingDispositionStatus = "compatibility_only"
	// RuntimeSettingDispositionNonApplicable marks VuGen authoring, recorder,
	// bookkeeping, or inactive-default rows with no runtime meaning for APEX.
	RuntimeSettingDispositionNonApplicable RuntimeSettingDispositionStatus = "non_applicable"
)

// RuntimeSettingDisposition explains what happened to one source runtime key.
type RuntimeSettingDisposition struct {
	Status RuntimeSettingDispositionStatus `json:"status"`
	Reason string                          `json:"reason,omitempty"`
	// ParityID names the parity/support entry that accounts for a mapped key.
	ParityID string `json:"parity_id,omitempty"`
	// Fields are the APEX destination fields for a mapped key.
	Fields []string `json:"fields,omitempty"`
	Note   string   `json:"note,omitempty"`
}

// runtimeKeyClassification is a static classification table row.
type runtimeKeyClassification struct {
	status RuntimeSettingDispositionStatus
	note   string
	// activeWhen decides whether a compatibility_only row is behaviorally
	// active for the given value (inactive rows degrade to non_applicable).
	// nil means the generic activity predicate applies.
	activeWhen func(section map[string]string, value string) bool
}

// consumedRuntimeKeys lists keys the parser reads that are not covered by any
// parity-definition matcher. Keyed by canonical section + compact key.
var consumedRuntimeKeys = map[string]map[string]string{
	"general": {
		"defaultrunlogic": "Selects the run-logic (.usp) profile.",
	},
}

// nonApplicableRuntimeKeys are VuGen authoring/recorder/UI/internal rows with
// no runtime meaning for replay, regardless of value.
var nonApplicableRuntimeKeys = map[string]map[string]string{
	"general": {
		"replay64bit":     "VuGen replay-process bitness; APEX runs its own engine.",
		"usethreads":      "mdrv threading model; APEX VUsers are goroutines. A Java Vuser that ran as threads imports with jvm_sharing threads.",
		"xlbridgetimeout": "VuGen Excel-bridge tooling timeout.",
	},
	"logging": {
		"printtimestamp": "mdrv log line formatting.",
		"includeenvinfo": "mdrv log environment banner.",
		"logdetail":      "Legacy VuGen log-detail flag with no modern UI equivalent.",
	},
	"http": {
		"webrecorderversion":                "VuGen recorder version stamp.",
		"treeviewrequestbodylimit":          "VuGen tree-view UI limit.",
		"printbuflinelen":                   "mdrv log buffer formatting.",
		"printbufescape0":                   "mdrv log buffer formatting.",
		"graphbytespersecond":               "LoadRunner online-graph publishing toggle.",
		"graphhitspersecondhttpstatuscodes": "LoadRunner online-graph publishing toggle.",
		"graphpagespersecond":               "LoadRunner online-graph publishing toggle.",
		"printntlmlog":                      "mdrv NTLM debug logging.",
		"printssllog":                       "mdrv SSL debug logging.",
	},
	"streaming": {
		"streamlog":          "LoadRunner streaming debug logging.",
		"savestreamsnapshot": "LoadRunner streaming snapshot capture.",
	},
}

// compatibilityRuntimeKeys are source controls with runtime meaning that APEX
// preserves but does not execute. Active values become compatibility_only
// findings; inactive/default values are recorded as non_applicable.
var compatibilityRuntimeKeys = map[string]map[string]runtimeKeyClassification{
	"general": {
		"failtransonerrormsg":        {note: "Fails the open transaction when an error message is emitted; APEX transaction status follows action results."},
		"accessvtsportbyquerystring": {note: "Remote VTS connectivity is unsupported. APEX live-table bindings require explicit authoring and do not translate VTS connection settings."},
		"automaticnestedtransactions": {
			note:       "Nested automatic sub-transactions are not generated by APEX auto-transaction wrapping.",
			activeWhen: activeWhenAutomaticTransactionsEnabled,
		},
		"automatictransactionsperfunc": {
			note:       "Per-function automatic transactions are not generated; APEX wraps per action or per iteration.",
			activeWhen: activeWhenAutomaticTransactionsEnabled,
		},
	},
	"http": {
		"loadchilengine":                     {note: "nCipher HSM private-key integration is not implemented; review credential and TLS requirements."},
		"autotransfileline":                  {note: "Source file and line naming for automatic transactions is not reproduced."},
		"httperrorsaswarnings":               {note: "Downgrades HTTP 4xx/5xx to warnings; APEX pass/fail follows action assertions and status checks."},
		"failnoncriticalitem":                {note: "Controls whether failed non-critical resources fail the step; APEX resource fetch failures do not fail the action."},
		"resourcepagetimeoutiswarning":       {note: "Downgrades resource-page timeouts to warnings; APEX resource fetch failures do not fail the action."},
		"enablechecks":                       {note: "Legacy HTML verification switch for web_find and web_image_check; registered searches remain independent."},
		"integratedauthentication":           {note: "Windows integrated (NTLM/Kerberos) authentication is review-only in APEX and fails closed at runtime."},
		"usenativentlm":                      {note: "NTLM stack selection; NTLM is review-only in APEX."},
		"disablentlm2ss":                     {note: "NTLM session-security tuning; NTLM is review-only in APEX."},
		"overridentlmcreds":                  {note: "NTLM credential override; NTLM is review-only in APEX."},
		"heavykdcload":                       {note: "Kerberos KDC load tuning; Kerberos is review-only in APEX."},
		"spnaddnonedefport":                  {note: "Kerberos SPN formation tuning; Kerberos is review-only in APEX."},
		"spncnamelookup":                     {note: "Kerberos SPN CNAME lookup tuning; Kerberos is review-only in APEX."},
		"supportcharset":                     {note: "Charset conversion for request/response bodies is not applied by APEX replay.", activeWhen: activeWhenNotUTF8Charset},
		"utf8inputoutput":                    {note: "UTF-8 input/output conversion toggle; APEX treats bodies as bytes/UTF-8 without conversion."},
		"retry401thinktime":                  {note: "Authentication retry pacing is not reproduced."},
		"websyncretryintervalms":             {note: "web_sync retry interval; web_sync is not an executable APEX action."},
		"websyncretrytimeoutms":              {note: "web_sync retry timeout; web_sync is not an executable APEX action."},
		"enableipcache":                      {note: "Per-VUser DNS/IP caching toggle; APEX uses the Go resolver."},
		"enabledynatrace":                    {note: "Injects Dynatrace request headers during replay; APEX emits its own trace headers."},
		"proxyuseautoconfigscript":           {note: "PAC requires an unambiguous selector, an HTTP(S) URL and the supported bounded evaluator profile."},
		"proxyautoconfigscripturl":           {note: "PAC requires an unambiguous selector, an HTTP(S) URL and the supported bounded evaluator profile.", activeWhen: activeWhenNonBlank},
		"proxyusebrowser":                    {note: "System mode uses generator environment proxies; Windows browser settings require an explicit export."},
		"proxypasswordisencrypted":           {note: "VuGen credential-encoding marker; APEX manages proxy credentials as secrets."},
		"usedataformatextensions":            {note: "Enables the LoadRunner Data Format Extension chain; APEX executes only its own configured DFE chain, and no source chain configuration was imported."},
		"keepnontextmimetype":                {note: "Source MIME semantics are unverified. Select native MIME parsing/exclusion controls explicitly; automatic translation is not certified."},
		"maxselfmetarefreshcount":            {note: "HTML meta-refresh follow limit is not configurable."},
		"maxerrormatchesaserrors":            {note: "Caps how many content-check mismatches count as errors; APEX reports every assertion result."},
		"netbufsize":                         {note: "Socket buffer sizing is managed by the Go runtime."},
		"resetcontext":                       {note: "Resets the HTML replay context between iterations; APEX rebuilds context per request."},
		"sslversionkey":                      {note: "Numeric SSL selector encoding is unverified. Select a supported TLS policy explicitly; legacy SSL_VERSION values such as 2&3 are not TLS 1.2."},
		"zlibheadersincompressedrequestbody": {note: "zlib header emission for compressed request bodies is not reproduced."},
		"ignorecontentcachingtypes":          {note: "Source MIME/cache semantics are unverified. Native cache_excluded_mime_types is an explicit replacement option, not an automatic equivalent."},
		"clearcacheforsimulatenewuser": {
			note:       "Child option of Simulate a new user each iteration; APEX clears cache and cookies unconditionally when that setting is on.",
			activeWhen: activeWhenSimulateNewUserEnabled,
		},
		"usebrowseragent":                          {note: "Recorded-browser User-Agent inheritance; the imported browser selection or custom User-Agent is authoritative."},
		"aedutf8values":                            {note: "Source AedUtf8Values semantics have not been verified; no encoding behavior was inferred."},
		"disableaed":                               {note: "Source DisableAED semantics have not been verified; zero does not prove inactivity."},
		"enablejsfortransport":                     {note: "LoadRunner's JavaScript-for-transport replay path is not reproduced by the APEX HTTP client."},
		"jsfortransportruntimesize":                {note: "JavaScript-for-transport runtime-size tuning is not reproduced.", activeWhen: activeWhenJavaScriptTransportEnabled},
		"jsfortransportstacksize":                  {note: "JavaScript-for-transport stack-size tuning is not reproduced.", activeWhen: activeWhenJavaScriptTransportEnabled},
		"wininetreplay":                            {note: "WinInet replay semantics are not reproduced; APEX uses its own HTTP client."},
		"prefetchprerendercallbacktimerintervalms": {note: "Prefetch/prerender callback scheduling interval is not configurable."},
		"websocketcallbacktimerintervalms":         {note: "Native WebSocket scripts support config.websocket.callback_interval_ms; source callback registration and bodies require review before translating this interval."},
		"logfilewrite":                             {note: "LoadRunner log-file routing is not reproduced; APEX uses its own logging pipeline."},
		"logfilewritetracetofile":                  {note: "LoadRunner trace-file routing is not reproduced; APEX uses its own logging pipeline."},
		"logenableresponselimit":                   {note: "LoadRunner response-log truncation controls are not reproduced."},
		"logmaxresponsesize":                       {note: "LoadRunner response-log size limit is not configurable.", activeWhen: activeWhenResponseLogLimitEnabled},
	},
	"streaming": {
		"streamtimeout":    {note: "Streaming inactivity timeout is not executed; APEX streaming pacing uses buffer and bitrate settings."},
		"streamretrytime":  {note: "Streaming retry interval is not executed."},
		"streamseekmethod": {note: "Streaming seek strategy is not executed.", activeWhen: activeWhenNonBlank},
	},
	"downloadfilters": {
		"filtertype": {
			note: "Filter-mode discriminator; APEX derives the active mode from whichever filter list carries entries.",
			activeWhen: func(section map[string]string, _ string) bool {
				return strings.TrimSpace(runtimeSectionValue(section, "IncludeFiltersInList")) != "" ||
					strings.TrimSpace(runtimeSectionValue(section, "ExcludeFiltersInList")) != ""
			},
		},
	},
	"logging": {
		"autologbuffersize": {
			note: "Log-cache size for send-on-error logging; APEX buffers error-context logs with its own bounds.",
			activeWhen: func(section map[string]string, _ string) bool {
				return parseBool(runtimeSectionValue(section, "AutoLog"))
			},
		},
	},
}

func activeWhenNonBlank(_ map[string]string, value string) bool {
	return strings.TrimSpace(value) != ""
}

func activeWhenExplicitTrue(_ map[string]string, value string) bool {
	parsed, valid := parseBoolExplicit(value)
	return valid && parsed
}

func activeWhenAutomaticTransactionsEnabled(section map[string]string, value string) bool {
	child, childValid := parseBoolExplicit(value)
	parent, parentValid := parseBoolExplicit(runtimeSectionFirstNonBlank(section, "AutomaticTransactions", "AutoTransactions"))
	return childValid && child && parentValid && parent
}

func activeWhenSimulateNewUserEnabled(section map[string]string, value string) bool {
	child, childValid := parseBoolExplicit(value)
	parent, parentValid := parseBoolExplicit(runtimeSectionFirstNonBlank(
		section,
		"SimulateNewUser",
		"NewUserPerIteration",
		"NewUserEachIteration",
	))
	return childValid && child && parentValid && parent
}

func activeWhenJavaScriptTransportEnabled(section map[string]string, value string) bool {
	if !runtimeSettingValueIsActive(value) {
		return false
	}
	parent, valid := parseBoolExplicit(runtimeSectionValue(section, "EnableJsForTransport"))
	return valid && parent
}

func activeWhenResponseLogLimitEnabled(section map[string]string, value string) bool {
	if !runtimeSettingValueIsActive(value) {
		return false
	}
	parent, valid := parseBoolExplicit(runtimeSectionValue(section, "LogEnableResponseLimit"))
	return valid && parent
}

func activeWhenNotUTF8Charset(_ map[string]string, value string) bool {
	switch normalizeEnum(value) {
	case "", "none", "utf_8", "utf8":
		return false
	default:
		return true
	}
}

// runtimeBookkeepingKeys appear inside VuGen run-logic tree sections and the
// project/profile scaffolding; they describe the tree itself, not settings.
var runtimeBookkeepingKeys = map[string]struct{}{
	"mercinitreefather":      {},
	"mercinitreesectionname": {},
	"mercinitreesons":        {},
	"name":                   {},
	"runlogicobjectkind":     {},
	"runlogicactiontype":     {},
}

// classifyRuntimeSelectedSections classifies every key of the selected runtime
// files for the given script protocol.
func classifyRuntimeSelectedSections(selected map[string]map[string]map[string]string, protocol string) map[string]map[string]map[string]RuntimeSettingDisposition {
	if len(selected) == 0 {
		return nil
	}
	definitions := sourceRTSSupportDefinitions
	if strings.EqualFold(strings.TrimSpace(protocol), "java_vuser") {
		definitions = allImportedRuntimeSettingSupportDefinitions()
	}
	result := make(map[string]map[string]map[string]RuntimeSettingDisposition, len(selected))
	for fileName, sections := range selected {
		fileResult := make(map[string]map[string]RuntimeSettingDisposition, len(sections))
		for sectionName, keys := range sections {
			sectionResult := make(map[string]RuntimeSettingDisposition, len(keys))
			for key, value := range keys {
				sectionResult[key] = classifyRuntimeSettingKey(definitions, protocol, sections, sectionName, keys, key, value)
			}
			if len(sectionResult) > 0 {
				fileResult[sectionName] = sectionResult
			}
		}
		if len(fileResult) > 0 {
			result[normalizeRuntimeArchivePath(fileName)] = fileResult
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func classifyRuntimeSettingKey(definitions []runtimeSettingSupportDefinition, protocol string, fileSections map[string]map[string]string, sectionName string, section map[string]string, key, value string) (result RuntimeSettingDisposition) {
	defer func() { result = explainRuntimeDisposition(result, sectionName, key) }()
	canonicalSection := runtimeConfigCanonicalSection(sectionName)
	if canonicalSection == "http" && strings.EqualFold(key, "AnalogMode") {
		return RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, Note: "Source AnalogMode meaning is unverified; zero is not proof of inactivity. No native behavior was guessed."}
	}
	compactKey := compactRuntimeEnum(key)

	// VuGen run-logic structural keys and the fixed per-group scaffolding of the
	// non-run lifecycle roots describe the tree, not runtime behavior. Unknown
	// behavioral keys in nested nodes deliberately fall through to review.
	if disposition, ok := classifyRuntimeBookkeepingKey(sectionName, compactKey); ok {
		return disposition
	}

	// Java VM sections have meaning only for the Java Vuser runner. On every
	// other protocol they are inert source rows, never advertised capability.
	if canonicalSection == "javavm" && !strings.EqualFold(strings.TrimSpace(protocol), "java_vuser") {
		return RuntimeSettingDisposition{
			Status: RuntimeSettingDispositionNonApplicable,
			Note:   "Java Vuser VM settings do not apply to this protocol's replay path.",
		}
	}

	// APEX-native round-trip sections have documented precedence over the raw
	// VuGen aliases. Rows in a shadowed alias section were intentionally not
	// consumed and must not be advertised as mapped capability.
	if sourceRuntimeAliasSectionShadowed(sectionName, fileSections) {
		return RuntimeSettingDisposition{
			Status: RuntimeSettingDispositionNonApplicable,
			Note:   "Superseded by the selected APEX-native runtime section in the same profile.",
		}
	}

	if canonicalSection == "thinktime" {
		mode := strings.ToUpper(strings.TrimSpace(runtimeSectionValue(section, "Options")))
		inactive := (compactKey == "thinktimerandomlow" || compactKey == "thinktimerandomhigh") && validThinkTimeMode(mode) && mode != "RANDOM"
		inactive = inactive || (compactKey == "factor" && validThinkTimeMode(mode) && mode != "MULTIPLY")
		inactive = inactive || (compactKey == "limit" && (mode == "NOTHINK" || strings.TrimSpace(runtimeSectionValue(section, "LimitFlag")) == "0"))
		if inactive {
			return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Reason: "unused_mode_value", Note: "Saved source value is inactive under think-time Options=" + mode + " or disabled LimitFlag."}
		}
	}
	if canonicalSection == "http" {
		if compactKey == "clearcacheforsimulatenewuser" && runtimeHTTPFeatureExplicitlyDisabled(fileSections, "SimulateNewUser") {
			return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: "Inactive in source: new-user simulation is disabled. The native cache-clear policy is supported."}
		}
		if compactKey == "logmaxresponsesize" && runtimeHTTPFeatureExplicitlyDisabled(fileSections, "LogEnableResponseLimit") {
			return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: "Inactive in source: the response-log limit is disabled."}
		}
		if compactKey == "autotransfileline" {
			general := findSection(fileSections, "General")
			if enabled, valid := parseBoolExplicit(runtimeSectionFirstNonBlank(general, "AutomaticTransactions", "AutoTransactions")); valid && !enabled {
				return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: "Inactive in source: automatic transactions are disabled."}
			}
		}
		switch compactKey {
		case "usenativentlm", "disablentlm2ss", "overridentlmcreds", "heavykdcload", "spnaddnonedefport", "spncnamelookup":
			if runtimeHTTPFeatureExplicitlyDisabled(fileSections, "IntegratedAuthentication") {
				return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: "Saved authentication option is inactive because IntegratedAuthentication is disabled."}
			}
		case "keepalivetimeout":
			if runtimeHTTPFeatureExplicitlyDisabled(fileSections, "KeepAlive") {
				return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, ParityID: "http_idle_timeout", Note: "Saved idle timeout is inactive because KeepAlive is disabled."}
			}
		}
	}
	if canonicalSection == "runlogicrunroot" {
		activeModes := map[string]string{
			"runlogicpaceconsttime": "CONST", "runlogicrandompacemin": "RANDOM", "runlogicrandompacemax": "RANDOM",
			"runlogicpaceconstaftertime": "CONSTAFTER", "runlogicafterpacemin": "AFTER", "runlogicafterpacemax": "AFTER",
		}
		mode := strings.ToUpper(strings.TrimSpace(runtimeSectionValue(section, "RunLogicPaceType")))
		if required, ok := activeModes[compactKey]; ok && (mode == "ASAP" || mode == "CONST" || mode == "RANDOM" || mode == "CONSTAFTER" || mode == "AFTER") && mode != required {
			return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: "Saved pacing bound is inactive under the selected RunLogicPaceType."}
		}
	}
	if disposition, ok := classifyConditionalSourceRuntimeKey(sectionName, section, key, value); ok {
		return disposition
	}

	// Parity-definition matchers are the authoritative "the importer reads
	// this" list; the definition status decides mapped vs compatibility-only.
	for _, definition := range definitions {
		for _, matcher := range definition.matchers {
			if !matcher.matchesSection(sectionName) || !matcher.matchesKey(key) || !matcher.matchesValue(value) {
				continue
			}
			if nativeHTTPCompatibilityID(definition.entry.ID) && !nativeHTTPCompatibilityProtocol(protocol) {
				return RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, ParityID: definition.entry.ID, Fields: cloneStrings(definition.entry.Fields), Note: "Native HTTP policy is not executed by this protocol."}
			}
			status := RuntimeSettingDispositionMapped
			if definition.entry.Status == RuntimeSettingStatusCompatibilityOnly {
				status = RuntimeSettingDispositionCompatibilityOnly
			}
			return RuntimeSettingDisposition{
				Status:   status,
				ParityID: definition.entry.ID,
				Fields:   cloneStrings(definition.entry.Fields),
			}
		}
	}

	if notes, ok := consumedRuntimeKeys[canonicalSection]; ok {
		if note, ok := notes[compactKey]; ok {
			return RuntimeSettingDisposition{Status: RuntimeSettingDispositionMapped, Note: note}
		}
	}

	// Numbered spill rows classify like their base key, downgraded to
	// compatibility-only: the base value is authoritative and the parser
	// reports divergent rows for review.
	if base, ok := runtimeSpillRowBaseKey(key); ok {
		baseDisposition := classifyRuntimeSettingKey(definitions, protocol, fileSections, sectionName, section, base, value)
		note := fmt.Sprintf("Numbered spill row of %s; the base value is authoritative and divergent rows are reported for review.", base)
		if baseDisposition.Status == RuntimeSettingDispositionNonApplicable {
			return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: note}
		}
		return RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, ParityID: baseDisposition.ParityID, Note: note}
	}

	if notes, ok := nonApplicableRuntimeKeys[canonicalSection]; ok {
		if note, ok := notes[compactKey]; ok {
			return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: note}
		}
	}

	if table, ok := compatibilityRuntimeKeys[canonicalSection]; ok {
		if classification, ok := table[compactKey]; ok {
			if inactive, note := runtimeCompatibilityInactive(canonicalSection, compactKey, value, fileSections); inactive {
				return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Reason: "disabled_feature", Note: note + " " + classification.note}
			}
			return RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, Note: classification.note}
		}
	}

	// Unknown key: never silent. Active values surface as compatibility-only
	// review evidence even when false/zero; only empty unknown rows are inactive.
	if strings.TrimSpace(value) != "" {
		return RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, Note: "No APEX equivalent is defined for this source setting; false and zero can be meaningful and are preserved for review."}
	}
	return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: "Empty source value with no APEX equivalent defined."}
}

func classifyConditionalSourceRuntimeKey(sectionName string, section map[string]string, key, value string) (RuntimeSettingDisposition, bool) {
	if strings.EqualFold(strings.TrimSpace(sectionName), "ModemSpeed") {
		compactKey := compactRuntimeEnum(key)
		if (compactKey == "modemspeed" && strings.TrimSpace(runtimeSectionValue(section, "EnableModemSpeed")) == "") ||
			(compactKey == "custommodemspeed" && strings.TrimSpace(runtimeSectionValue(section, "EnableCustomModemSpeed")) == "") {
			return RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, Note: "The corresponding modem-speed enable selector is missing; a saved speed alone does not prove whether it is active."}, true
		}
		presetEnabled, presetValid := parseBoolExplicit(runtimeSectionValue(section, "EnableModemSpeed"))
		customEnabled, customValid := parseBoolExplicit(runtimeSectionValue(section, "EnableCustomModemSpeed"))
		if !presetValid && strings.TrimSpace(runtimeSectionValue(section, "EnableModemSpeed")) == "" {
			presetValid = true
		}
		if !customValid && strings.TrimSpace(runtimeSectionValue(section, "EnableCustomModemSpeed")) == "" {
			customValid = true
		}
		if (!presetValid || !customValid || (presetEnabled && customEnabled)) &&
			(compactKey == "enablemodemspeed" || compactKey == "enablecustommodemspeed" ||
				compactKey == "modemspeed" || compactKey == "custommodemspeed") {
			return RuntimeSettingDisposition{
				Status: RuntimeSettingDispositionCompatibilityOnly,
				Note:   "The source modem-speed selector is ambiguous or invalid; no bandwidth setting was applied.",
			}, true
		}
		switch compactKey {
		case "modemspeed":
			if !presetEnabled {
				return RuntimeSettingDisposition{
					Status: RuntimeSettingDispositionNonApplicable,
					Note:   "Preset modem speed is dormant because EnableModemSpeed is off.",
				}, true
			}
		case "custommodemspeed":
			if !customEnabled {
				return RuntimeSettingDisposition{
					Status: RuntimeSettingDispositionNonApplicable,
					Note:   "Custom modem speed is dormant because EnableCustomModemSpeed is off.",
				}, true
			}
		}
	}

	if strings.EqualFold(strings.TrimSpace(sectionName), "FILTERS") {
		compactKey := compactRuntimeEnum(key)
		if compactKey == "includefiltersinlist" || compactKey == "excludefiltersinlist" {
			include := strings.TrimSpace(runtimeSectionValue(section, "IncludeFiltersInList"))
			exclude := strings.TrimSpace(runtimeSectionValue(section, "ExcludeFiltersInList"))
			if include != "" && exclude != "" {
				return RuntimeSettingDisposition{
					Status: RuntimeSettingDispositionCompatibilityOnly,
					Note:   "Both source filter lists are populated, so neither list was applied.",
				}, true
			}
			if strings.TrimSpace(value) == "" {
				return RuntimeSettingDisposition{
					Status: RuntimeSettingDispositionNonApplicable,
					Note:   "The source filter list is empty.",
				}, true
			}
		}
	}

	return RuntimeSettingDisposition{}, false
}

func sourceRuntimeAliasSectionShadowed(sectionName string, sections map[string]map[string]string) bool {
	switch {
	case strings.EqualFold(strings.TrimSpace(sectionName), "ModemSpeed"):
		return nativeNetworkSimulationSectionConfigured(sections)
	case strings.EqualFold(strings.TrimSpace(sectionName), "FILTERS"):
		return nativeDownloadFiltersSectionConfigured(sections)
	default:
		return false
	}
}

func classifyRuntimeBookkeepingKey(sectionName, compactKey string) (RuntimeSettingDisposition, bool) {
	trimmedSection := strings.TrimSpace(sectionName)
	lowerSection := strings.ToLower(trimmedSection)
	isRunLogicTree := strings.HasPrefix(lowerSection, "runlogic") || lowerSection == "profile actions"
	if !isRunLogicTree {
		return RuntimeSettingDisposition{}, false
	}
	note := "VuGen run-logic tree bookkeeping."
	if _, ok := runtimeBookkeepingKeys[compactKey]; ok {
		return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: note}, true
	}
	switch lowerSection {
	case "runlogicinitroot", "runlogicendroot":
		// Init/end groups always run once in sequence; their per-group knobs
		// carry no additional meaning.
		return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: "Init/end lifecycle groups run once; per-group run-logic knobs have no additional meaning."}, true
	case "runlogicerrorhandlerroot":
		return RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: "Error-handler run-logic block; accounted for by the run_logic_error_handler finding."}, true
	}
	return RuntimeSettingDisposition{}, false
}

func runtimeSpillRowBaseKey(key string) (string, bool) {
	const marker = "_extra_arg_"
	// Slice the lowercased form only: strings.ToLower is not byte-length
	// preserving, so an index found in the lowered string must never slice the
	// original (same hazard documented on javaVMParameterExtraArgIndex). The
	// lowered base is fine here — every downstream lookup normalizes anyway.
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	markerAt := strings.LastIndex(lowerKey, marker)
	if markerAt <= 0 {
		return "", false
	}
	index, err := strconv.Atoi(lowerKey[markerAt+len(marker):])
	if err != nil || index <= 0 {
		return "", false
	}
	return lowerKey[:markerAt], true
}

// runtimeSettingValueIsActive is the generic activity predicate for keys
// without a bespoke rule: explicit-true booleans, non-zero numbers, and
// non-sentinel strings count as active. Negative values are conservative:
// source runtimes commonly use them as meaningful "unlimited"/inherit markers.
func runtimeSettingValueIsActive(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if parsed, valid := parseBoolExplicit(trimmed); valid {
		return parsed
	}
	if number, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return number != 0
	}
	switch normalizeEnum(trimmed) {
	case "none", "disabled", "off", "auto", "automatic", "default":
		return false
	}
	return true
}

// appendRuntimeDispositionParityEntries folds the per-key classification into
// the parity report as two rollup entries so unexecuted source settings weigh
// on the migration-readiness score instead of silently inflating it.
func appendRuntimeDispositionParityEntries(parity []RuntimeSettingParityEntry, dispositions map[string]map[string]map[string]RuntimeSettingDisposition) []RuntimeSettingParityEntry {
	if len(dispositions) == 0 {
		return parity
	}
	type keyEvidence struct {
		file    string
		section string
		key     string
	}
	const maxEvidence = 40
	compatible := make([]keyEvidence, 0, maxEvidence)
	compatibleCount := 0
	nonApplicableCount := 0
	compatibleFiles := make(map[string]struct{})
	nonApplicableFiles := make(map[string]struct{})
	evidenceLess := func(left, right keyEvidence) bool {
		if left.file != right.file {
			return left.file < right.file
		}
		if left.section != right.section {
			return left.section < right.section
		}
		return left.key < right.key
	}
	addCompatibleEvidence := func(item keyEvidence) {
		compatibleCount++
		if len(compatible) < maxEvidence {
			compatible = append(compatible, item)
		} else if !evidenceLess(item, compatible[len(compatible)-1]) {
			return
		} else {
			compatible[len(compatible)-1] = item
		}
		sort.Slice(compatible, func(left, right int) bool {
			return evidenceLess(compatible[left], compatible[right])
		})
	}
	for fileName, sections := range dispositions {
		for sectionName, keys := range sections {
			for key, disposition := range keys {
				// Keys a parity definition already accounts for (ParityID set)
				// are represented by that entry; re-counting them here would
				// double-charge the readiness score.
				if disposition.ParityID != "" {
					continue
				}
				switch disposition.Status {
				case RuntimeSettingDispositionCompatibilityOnly:
					addCompatibleEvidence(keyEvidence{file: fileName, section: sectionName, key: key})
					compatibleFiles[fileName] = struct{}{}
				case RuntimeSettingDispositionNonApplicable:
					nonApplicableCount++
					nonApplicableFiles[fileName] = struct{}{}
				}
			}
		}
	}
	sortedFiles := func(fileSet map[string]struct{}) []string {
		files := make([]string, 0, len(fileSet))
		for fileName := range fileSet {
			files = append(files, fileName)
		}
		sort.Strings(files)
		return files
	}

	if compatibleCount > 0 {
		evidence := make([]string, 0, len(compatible)+1)
		for _, item := range compatible {
			evidence = append(evidence, fmt.Sprintf(
				"[%s] %s",
				boundedRuntimeReviewEvidence(item.section),
				boundedRuntimeReviewEvidence(item.key),
			))
		}
		if omitted := compatibleCount - len(compatible); omitted > 0 {
			evidence = append(evidence, fmt.Sprintf("+%d more", omitted))
		}
		parity = append(parity, RuntimeSettingParityEntry{
			ID:      "runtime_unmapped",
			Area:    "Runtime Settings",
			Concept: "Source settings preserved without execution",
			Status:  RuntimeSettingStatusCompatibilityOnly,
			Notes: fmt.Sprintf("%d source runtime settings have no verified APEX execution mapping and are preserved for review: %s. Per-key dispositions are recorded in the imported source runtime settings.",
				compatibleCount, strings.Join(evidence, "; ")),
			RuntimeFiles: sortedFiles(compatibleFiles),
		})
	}
	if nonApplicableCount > 0 {
		parity = append(parity, RuntimeSettingParityEntry{
			ID:      "runtime_non_applicable",
			Area:    "Runtime Settings",
			Concept: "Source rows with no runtime meaning",
			Status:  RuntimeSettingStatusNonApplicable,
			Notes: fmt.Sprintf("%d source rows are VuGen authoring/recorder metadata, run-logic bookkeeping, or inactive defaults with no runtime meaning; per-key dispositions are recorded in the imported source runtime settings.",
				nonApplicableCount),
			RuntimeFiles: sortedFiles(nonApplicableFiles),
		})
	}
	return parity
}
