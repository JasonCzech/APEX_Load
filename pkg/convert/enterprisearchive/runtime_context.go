package enterprisearchive

import (
	"fmt"
	"path"
	"sort"
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
func refineRuntimeSettingDispositions(dispositions map[string]map[string]map[string]RuntimeSettingDisposition, rc *RuntimeConfig, files map[string]string, protocol string, projectConfig *ProjectConfig) {
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
	// The absence proof parses the archive's C sources, which dominates the
	// cost of importing a large archive. Run it once, and only when a saved
	// row depends on it.
	var operationsAbsent []bool
	operationAbsent := func(operation int) bool {
		if operationsAbsent == nil {
			operationsAbsent = runtimeSourceOperationsAbsentMatching(files, compiledSourceFiles(projectConfig, files), protocol, runtimeSourceOperationNames...)
		}
		return operationsAbsent[operation]
	}
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
						if operationAbsent(runtimeSourceWebSocket) && operationAbsent(runtimeSourceSocket) {
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
				if canonicalSection == "streaming" && (compactKey == "streamtimeout" || compactKey == "streamretrytime" || compactKey == "streamseekmethod") && operationAbsent(runtimeSourceStream) {
					note = "Saved streaming option is inactive: no web_stream operation is present in the inspected C source."
				}
				if canonicalSection == "http" && (compactKey == "websyncretryintervalms" || compactKey == "websyncretrytimeoutms") && operationAbsent(runtimeSourceSync) {
					note = "Saved synchronization option is inactive: no web_sync operation is present in the inspected C source."
				}
				if note != "" {
					keys[key] = RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: note}
				}
			}
		}
	}
}

// runtimeSourceOperationNames indexes the operations refinement can prove
// absent, so one pass over the source answers every saved row.
var runtimeSourceOperationNames = []string{"web_stream", "web_sync", "web_websocket", "web_socket"}

const (
	runtimeSourceStream = iota
	runtimeSourceSync
	runtimeSourceWebSocket
	runtimeSourceSocket
)

// compiledSourceFiles returns the archive paths of the C sources LoadRunner
// compiles for the project, and every archive file they #include,
// transitively. VuGen compiles combined_<script>.c, which includes globals.h
// and each declared action file; the project's extra C/header files are kept
// too. Nothing else can run, so VuGen's generated pre_cci.c and stray copies
// such as Action_old.c are left out. It returns nil, meaning every C source
// must be inspected, when that set is unknown: no project, a declared action
// file that is missing or ambiguous, or an #include whose target a macro
// computes.
func compiledSourceFiles(projectConfig *ProjectConfig, files map[string]string) []string {
	if projectConfig == nil {
		return nil
	}
	keyByPath := make(map[string]string, len(files))
	keysByBase := make(map[string][]string)
	for key := range files {
		normalized := strings.ToLower(normalizeRuntimeArchivePath(key))
		keyByPath[normalized] = key
		keysByBase[path.Base(normalized)] = append(keysByBase[path.Base(normalized)], key)
	}
	var compiled []string
	seen := map[string]bool{}
	add := func(key string) {
		if !seen[key] {
			seen[key] = true
			compiled = append(compiled, key)
		}
	}
	// Inspecting too much is safe, so a file named without a resolvable path
	// reaches every archive file with its basename. A name no archive file has
	// is absent from the archive, such as the SDK header lrun.h.
	addByBase := func(name string) {
		for _, key := range keysByBase[strings.ToLower(path.Base(normalizeRuntimeArchivePath(name)))] {
			add(key)
		}
	}
	declarations := orderedSourceActionFileDeclarations(projectConfig, files)
	if len(declarations) == 0 {
		return nil
	}
	for _, declaration := range declarations {
		resolved, _, ambiguous := findSourceArchiveFile(files, declaration.Name)
		key, ok := keyByPath[strings.ToLower(resolved)]
		if resolved == "" || len(ambiguous) > 0 || !ok {
			return nil
		}
		add(key)
	}
	addByBase("globals.h")
	for _, extra := range projectConfig.ExtraFiles {
		if extension := strings.ToLower(path.Ext(extra)); extension == ".c" || extension == ".h" {
			addByBase(extra)
		}
	}
	for base, keys := range keysByBase {
		if strings.HasPrefix(base, "combined_") && strings.HasSuffix(base, ".c") {
			for _, key := range keys {
				add(key)
			}
		}
	}
	for next := 0; next < len(compiled); next++ {
		includes, ok := cSourceIncludes(files[compiled[next]])
		if !ok {
			return nil
		}
		for _, include := range includes {
			addByBase(include)
		}
	}
	sort.Strings(compiled)
	return compiled
}

// cSourceIncludes returns the targets of content's #include directives,
// including those in inactive #if blocks. It reports false for a directive
// whose target is not a quoted or bracketed name, since a macro computes it.
func cSourceIncludes(content string) ([]string, bool) {
	var includes []string
	inComment := false
	for line := range strings.SplitSeq(content, "\n") {
		code := strings.TrimSpace(stripCComments(line, &inComment))
		if !strings.HasPrefix(code, "#") {
			continue
		}
		directive, isInclude := strings.CutPrefix(strings.TrimSpace(code[1:]), "include")
		if !isInclude {
			continue
		}
		directive = strings.TrimSpace(directive)
		closing := byte(0)
		switch {
		case strings.HasPrefix(directive, `"`):
			closing = '"'
		case strings.HasPrefix(directive, "<"):
			closing = '>'
		default:
			return nil, false
		}
		end := strings.IndexByte(directive[1:], closing)
		if end < 0 {
			return nil, false
		}
		includes = append(includes, directive[1:1+end])
	}
	return includes, true
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

// runtimeSourceOperationsAbsentMatching reports, for each operation name
// prefix, whether the inspected source provably never references it. This is a
// conservative absence check, not a control-flow reachability proof. It
// inspects the archive paths in scope (see compiledSourceFiles), or every
// C/header file when scope is nil. Unparseable source, or any Java, JavaScript
// or native library in the archive, leaves every answer false. Macro and
// function-pointer references count as references, while generated SDK
// function declarations are not mistaken for operations.
//
// Every inspected file is lexed before any is parsed: an incomplete corpus or a
// reference to every name decides the answer without the C parse, which costs
// far more than the rest of the check combined.
func runtimeSourceOperationsAbsentMatching(files map[string]string, scope []string, protocol string, names ...string) []bool {
	absent := make([]bool, len(names))
	if protocol != "http" && protocol != "http/1.1" && protocol != "http/2" && protocol != "http/3" && protocol != "soap" {
		return absent
	}
	found := make([]bool, len(names))
	remaining := len(names)
	mark := func(text string, matches func(string, string) bool) {
		for i, name := range names {
			if !found[i] && matches(text, name) {
				found[i] = true
				remaining--
			}
		}
	}
	var sources []string
	for file := range files {
		switch strings.ToLower(path.Ext(file)) {
		case ".java", ".js", ".dll", ".so":
			return absent
		case ".c", ".h":
			if scope == nil {
				sources = append(sources, file)
			}
		}
	}
	if scope != nil {
		sources = scope
	}
	if len(sources) == 0 {
		return absent
	}
	for _, file := range sources {
		content := files[file]
		// A function can be passed or assigned without being called by name.
		// Keep those references reviewable without counting SDK prototypes:
		// their identifier is followed by an argument list. Direct calls are
		// covered by ParseActionFile below; literals/comments are not tokens.
		tokens := lexCTransactionTokens(content, true)
		for i, token := range tokens {
			if token.kind != cTransactionIdentifier || (i+1 < len(tokens) && tokens[i+1].text == "(") {
				continue
			}
			mark(strings.ToLower(token.text), strings.HasPrefix)
		}
		inComment := false
		for line := range strings.SplitSeq(content, "\n") {
			code := stripCComments(line, &inComment)
			if strings.HasPrefix(strings.TrimSpace(code), "#") {
				code = strings.ToLower(code)
				mark(code, strings.Contains)
				if strings.Contains(code, "##") || strings.HasSuffix(strings.TrimSpace(code), "\\") {
					return absent
				}
			}
		}
		if remaining == 0 {
			return absent
		}
	}
	for _, file := range sources {
		parsed, err := ParseActionFile(file, files[file])
		if err != nil || parsed == nil || len(parsed.ParseFailures) > 0 {
			return absent
		}
		for _, call := range parsed.Functions {
			mark(strings.ToLower(call.Name), strings.HasPrefix)
		}
		if remaining == 0 {
			return absent
		}
	}
	for i := range names {
		absent[i] = !found[i]
	}
	return absent
}
