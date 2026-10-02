package enterprisearchive

import (
	"bytes"
	"encoding/json"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// These files describe source-browser behavior. Inventory them without executing
// JavaScript or copying preference values into the public review metadata.
const truClientBrowserProfileKey = "truclient_browser_profile_settings"

const truClientBrowserProfileMaxBytes = 2 << 20
const truClientBrowserProfileMaxDeclarations = 10000

type truClientBrowserProfileInventory struct {
	Files []truClientBrowserProfileFile `json:"files"`
}

type truClientBrowserProfileFile struct {
	SourceFile   string                             `json:"source_file"`
	Kind         string                             `json:"kind"`
	Declarations int                                `json:"declarations"`
	Unparsed     bool                               `json:"unparsed_source_content,omitempty"`
	Settings     []truClientBrowserProfileSetting   `json:"settings"`
	ListEntries  []truClientBrowserProfileListEntry `json:"source_list_entries,omitempty"`
}

type truClientBrowserProfileListEntry struct {
	Section    string                          `json:"section"`
	SourceLine int                             `json:"source_line"`
	Status     RuntimeSettingDispositionStatus `json:"status"`
}

type truClientBrowserProfileSetting struct {
	Key                       string                          `json:"key"`
	Section                   string                          `json:"section"`
	Status                    RuntimeSettingDispositionStatus `json:"status"`
	DeclarationLines          []int                           `json:"declaration_lines"`
	LastParsedDeclarationLine int                             `json:"last_parsed_declaration_line,omitempty"`
	Note                      string                          `json:"note"`
}

const truClientPreferenceNote = "Source browser preference is preserved for review and is not applied by Go Chromium replay. Declaration lines identify parsed source evidence only; actual source-browser effect and cross-file override precedence are unverified."
const truClientOverrideNote = "Source preference override and its default/locked/type/value fields are preserved in the original source. Source-browser applicability, locking, and cross-file override precedence are unverified; Go Chromium replay does not apply this override."
const truClientAuthenticationNote = "Source browser authentication filter selector and list records are preserved for review. No Go authentication-policy equivalence or source-policy inactivity is claimed."

// Accept literal preference declarations only. Unknown JavaScript remains in
// the supplied archive and keeps the authoring draft gated.
var truClientPreferenceStatement = regexp.MustCompile(`^user_pref\s*\(\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')\s*,\s*(?:true|false|null|[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')\s*\)\s*;`)

func inspectTruClientPreferenceFile(name, content string) truClientBrowserProfileFile {
	file := truClientBrowserProfileFile{SourceFile: name, Kind: "browser_preferences", Settings: []truClientBrowserProfileSetting{}}
	if len(content) > truClientBrowserProfileMaxBytes {
		file.Unparsed = true
		return file
	}
	var stripped strings.Builder
	inComment := false
	for _, line := range strings.Split(content, "\n") {
		stripped.WriteString(stripCComments(line, &inComment))
		stripped.WriteByte('\n')
	}
	text := stripped.String()
	rows := map[string]*truClientBrowserProfileSetting{}
	position := 0
	line := 1
	for position < len(text) {
		whitespace := len(text[position:]) - len(strings.TrimLeft(text[position:], " \t\r\n"))
		line += strings.Count(text[position:position+whitespace], "\n")
		position += whitespace
		if position == len(text) {
			break
		}
		if file.Declarations >= truClientBrowserProfileMaxDeclarations {
			file.Unparsed = true
			break
		}
		match := truClientPreferenceStatement.FindStringSubmatchIndex(text[position:])
		if match == nil {
			file.Unparsed = true
			break
		}
		literal := text[position+match[2] : position+match[3]]
		key := ""
		if strings.HasPrefix(literal, `"`) {
			if json.Unmarshal([]byte(literal), &key) != nil {
				file.Unparsed = true
				break
			}
		} else {
			// Do not guess JavaScript escape semantics for single-quoted keys.
			if strings.Contains(literal, `\`) {
				file.Unparsed = true
				break
			}
			key = literal[1 : len(literal)-1]
		}
		if key == "" {
			file.Unparsed = true
			break
		}
		row := rows[key]
		if row == nil {
			row = &truClientBrowserProfileSetting{Key: key, Section: "Source browser preferences", Status: RuntimeSettingDispositionCompatibilityOnly, Note: truClientPreferenceNote}
			rows[key] = row
		}
		row.DeclarationLines = append(row.DeclarationLines, line)
		row.LastParsedDeclarationLine = line
		file.Declarations++
		line += strings.Count(text[position:position+match[1]], "\n")
		position += match[1]
	}
	file.Unparsed = file.Unparsed || inComment
	for _, row := range rows {
		file.Settings = append(file.Settings, *row)
	}
	sort.Slice(file.Settings, func(i, j int) bool { return file.Settings[i].Key < file.Settings[j].Key })
	return file
}

func inspectTruClientPreferenceOverrides(name, content string) truClientBrowserProfileFile {
	file := truClientBrowserProfileFile{SourceFile: name, Kind: "browser_preference_overrides", Settings: []truClientBrowserProfileSetting{}}
	if len(content) > truClientBrowserProfileMaxBytes {
		file.Unparsed = true
		return file
	}
	rows := map[string]*truClientBrowserProfileSetting{}
	line, linePosition := 1, 0
	valid := walkTruClientJSONFields([]byte(content), func(key string, raw json.RawMessage, position int) bool {
		if file.Declarations >= truClientBrowserProfileMaxDeclarations {
			return false
		}
		line += strings.Count(content[linePosition:position], "\n")
		linePosition = position
		row := rows[key]
		if row == nil {
			row = &truClientBrowserProfileSetting{Key: key, Section: "Source browser preference overrides", Status: RuntimeSettingDispositionCompatibilityOnly, Note: truClientOverrideNote}
			rows[key] = row
		} else {
			file.Unparsed = true
		}
		row.DeclarationLines = append(row.DeclarationLines, line)
		file.Declarations++
		seen := map[string]bool{}
		if !walkTruClientJSONFields(raw, func(field string, _ json.RawMessage, _ int) bool {
			if seen[field] {
				file.Unparsed = true
			}
			seen[field] = true
			return true
		}) {
			file.Unparsed = true
		}
		return true
	})
	file.Unparsed = file.Unparsed || !valid
	for _, row := range rows {
		file.Settings = append(file.Settings, *row)
	}

	sort.Slice(file.Settings, func(i, j int) bool { return file.Settings[i].Key < file.Settings[j].Key })
	return file
}

// Token traversal retains duplicate declarations and actual key locations.
// Values are only decoded as opaque RawMessage and never enter the inventory.
func walkTruClientJSONFields(content []byte, visit func(string, json.RawMessage, int) bool) bool {
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	count := 0
	for decoder.More() {
		if count >= truClientBrowserProfileMaxDeclarations {
			return false
		}
		count++
		before := int(decoder.InputOffset())
		token, err = decoder.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok || key == "" {
			return false
		}
		position := bytes.IndexByte(content[before:], '"')
		if position < 0 {
			return false
		}
		position += before
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return false
		}
		if !visit(key, raw, position) {
			return false
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF
}

func inspectTruClientAuthenticationFilters(name, content string) truClientBrowserProfileFile {
	file := truClientBrowserProfileFile{SourceFile: name, Kind: "browser_authentication_filters", Settings: []truClientBrowserProfileSetting{}}
	if len(content) > truClientBrowserProfileMaxBytes {
		file.Unparsed = true
		return file
	}
	rows := map[string]*truClientBrowserProfileSetting{}
	section := ""
	for index, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if file.Declarations+len(file.ListEntries) >= truClientBrowserProfileMaxDeclarations {
			file.Unparsed = true
			break
		}
		if strings.EqualFold(section, "AllowList") || strings.EqualFold(section, "DenyList") {
			file.Unparsed = true
			file.ListEntries = append(file.ListEntries, truClientBrowserProfileListEntry{Section: section, SourceLine: index + 1, Status: RuntimeSettingDispositionCompatibilityOnly})
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || section == "" {
			file.Unparsed = true
			continue
		}
		id := section + "\x00" + key
		row := rows[id]
		if row == nil {
			row = &truClientBrowserProfileSetting{Key: key, Section: "Source browser authentication filters / " + section, Status: RuntimeSettingDispositionCompatibilityOnly, Note: truClientAuthenticationNote}
			rows[id] = row
		}
		row.DeclarationLines = append(row.DeclarationLines, index+1)
		file.Declarations++
	}
	for _, row := range rows {
		file.Settings = append(file.Settings, *row)
	}
	sort.Slice(file.Settings, func(i, j int) bool {
		if file.Settings[i].Section != file.Settings[j].Section {
			return file.Settings[i].Section < file.Settings[j].Section
		}
		return file.Settings[i].Key < file.Settings[j].Key
	})
	return file
}

func applyTruClientBrowserProfileReview(result *ConvertResult, files map[string]string) {
	if result == nil || result.Script == nil {
		return
	}
	inventory := truClientBrowserProfileInventory{Files: []truClientBrowserProfileFile{}}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var file truClientBrowserProfileFile
		switch strings.ToLower(path.Base(name)) {
		case "user.js":
			file = inspectTruClientPreferenceFile(name, files[name])
		case "user_modified_perfs.json":
			file = inspectTruClientPreferenceOverrides(name, files[name])
		case "tcieauthenticationfilters.ini":
			file = inspectTruClientAuthenticationFilters(name, files[name])
		default:
			continue
		}
		inventory.Files = append(inventory.Files, file)
	}
	if len(inventory.Files) == 0 {
		return
	}
	if result.Script.Metadata == nil {
		result.Script.Metadata = &dsl.ScriptMetadata{}
	}
	if result.Script.Metadata.Import == nil {
		result.Script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if result.Script.Metadata.Import.Preserved == nil {
		result.Script.Metadata.Import.Preserved = map[string]any{}
	}
	result.Script.Metadata.Import.Preserved[truClientBrowserProfileKey] = inventory
	gate := dsl.Action{Name: "Review source browser preferences and authentication policy", Type: "source_review", Metadata: map[string]any{"source_file": inventory.Files[0].SourceFile}}
	importreview.ApplyToAction(&gate, []importreview.Finding{{Code: "truclient_browser_preferences_require_review", ReasonCode: "truclient_browser_preferences_require_review", Gate: importreview.GateExecution, Source: "enterprise_script_archive", SourceRef: inventory.Files[0].SourceFile, Detail: "Source browser preference files, override precedence, and authentication policy require review before native browser promotion. The bounded source preview and supplied archive retain declaration evidence; no Go Chromium equivalence is claimed."}})
	result.Script.Init = append(result.Script.Init, gate)
	for _, file := range inventory.Files {
		appendSourceReviewMapping(result, SourceReviewMapping{SourceFile: file.SourceFile, StartLine: 1, Function: "Source browser profile settings", TargetPath: "metadata.import.preserved." + truClientBrowserProfileKey, TargetKind: "configuration", Status: "review_only", Notes: "Value-free preference inventory is available in Runtime settings; a bounded preview is available in Source files; complete declarations remain in the supplied archive."})
	}
}

func appendTruClientBrowserProfileRuntimeEvidence(preserved map[string]any, runtimeFiles map[string]map[string]map[string]string, dispositions map[string]map[string]map[string]RuntimeSettingDisposition) {
	inventory, ok := preserved[truClientBrowserProfileKey].(truClientBrowserProfileInventory)
	if !ok {
		return
	}
	for _, file := range inventory.Files {
		if runtimeFiles[file.SourceFile] == nil {
			runtimeFiles[file.SourceFile] = map[string]map[string]string{}
		}
		if dispositions[file.SourceFile] == nil {
			dispositions[file.SourceFile] = map[string]map[string]RuntimeSettingDisposition{}
		}
		for _, setting := range file.Settings {
			if runtimeFiles[file.SourceFile][setting.Section] == nil {
				runtimeFiles[file.SourceFile][setting.Section] = map[string]string{}
				dispositions[file.SourceFile][setting.Section] = map[string]RuntimeSettingDisposition{}
			}
			runtimeFiles[file.SourceFile][setting.Section][setting.Key] = "Preserved in source (" + strconv.Itoa(len(setting.DeclarationLines)) + " declarations)"
			note := setting.Note
			if len(file.ListEntries) > 0 {
				note += " " + strconv.Itoa(len(file.ListEntries)) + " source Allow/Deny list records remain unparsed; value-free section/line evidence is retained in the profile inventory and original records are visible in Source files."
			}
			dispositions[file.SourceFile][setting.Section][setting.Key] = RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, Note: note}
		}
	}
}
