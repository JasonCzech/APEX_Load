package enterprisearchive

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

const truClientRuntimeMappingsKey = "truclient_browser_runtime_mappings"

// Source browser timeout values are seconds. Bound them before translating to
// the millisecond contract shared by the engine and Playwright adapter.
func truClientTimeoutMS(value string) (int, bool) {
	n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n > 3600 || math.Trunc(n*1000) != n*1000 {
		return 0, false
	}
	return int(n * 1000), true
}

func applyTruClientBrowserRuntimeSettings(result *ConvertResult, runtimeFile string, runtime *RuntimeConfig, files map[string]string) {
	if result == nil || result.Script == nil {
		return
	}
	script := result.Script
	mappings := map[string]map[string]map[string]RuntimeSettingDisposition{}
	content, ok := findFile(files, runtimeFile)
	if !ok {
		return
	}
	for section, values := range ParseConfigSections(content) {
		if strings.EqualFold(section, "ModemSpeed") {
			rows := map[string]RuntimeSettingDisposition{}
			if mappings[normalizeRuntimeArchivePath(runtimeFile)] == nil {
				mappings[normalizeRuntimeArchivePath(runtimeFile)] = map[string]map[string]RuntimeSettingDisposition{}
			}
			mappings[normalizeRuntimeArchivePath(runtimeFile)][section] = rows
			for key, value := range values {
				if strings.EqualFold(key, "EnableModemSpeed") || strings.EqualFold(key, "EnableCustomModemSpeed") {
					row := RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, Note: "Go browser replay does not apply the HTTP engine's network-speed shaping configuration."}
					if value == "0" {
						row.Status = RuntimeSettingDispositionNonApplicable
						row.Note = "Source network speed simulation selector is disabled; HTTP engine network shaping is not a browser capability."
					}
					rows[key] = row
				}
			}
			continue
		}
		if !strings.EqualFold(section, "TruClient") {
			continue
		}
		rows := map[string]RuntimeSettingDisposition{}
		if mappings[normalizeRuntimeArchivePath(runtimeFile)] == nil {
			mappings[normalizeRuntimeArchivePath(runtimeFile)] = map[string]map[string]RuntimeSettingDisposition{}
		}
		mappings[normalizeRuntimeArchivePath(runtimeFile)][section] = rows
		stepTimeout, objectTimeout, networkEndThreshold := 0, 0, 0
		for key, value := range values {
			row := RuntimeSettingDisposition{Status: RuntimeSettingDispositionCompatibilityOnly, Note: "Preserved source browser setting; its Go browser equivalence requires source-profile review."}
			switch strings.ToLower(key) {
			case "steptimeout":
				if n, valid := truClientTimeoutMS(value); valid {
					stepTimeout = n
					row = RuntimeSettingDisposition{Status: RuntimeSettingDispositionMapped, Fields: []string{"actions[].timeout_ms", "actions[].sync.timeout_ms", "actions[].wait_for.timeout_ms"}, Note: "Source seconds converted to bounded per-step, completion, and duration-wait timeout milliseconds."}
				}
			case "objecttimeout":
				if n, valid := truClientTimeoutMS(value); valid {
					objectTimeout = n
					row = RuntimeSettingDisposition{Status: RuntimeSettingDispositionMapped, Fields: []string{"actions[].params.timeout_ms", "actions[].wait_for.timeout_ms"}, Note: "Source seconds converted to selector/object timeout milliseconds; explicit per-step object timeouts override this value."}
				}
			case "networkendthreshold":
				if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > 0 && n <= dsl.MaxConfiguredProtocolTimeoutMS {
					networkEndThreshold = n
					row = RuntimeSettingDisposition{Status: RuntimeSettingDispositionMapped, Fields: []string{"actions[].sync.stable_ms"}, Note: "Source network-quiet threshold milliseconds applied to network_idle step completion policies."}
				}
			case "simulatenewuser":
				if value == "0" || value == "1" {
					b := value == "1"
					script.Config.Browser.NewUserPerIter = &b
					row = RuntimeSettingDisposition{Status: RuntimeSettingDispositionMapped, Fields: []string{"config.browser.new_user_per_iteration"}, Note: "Mapped browser new-user policy; browser identity and exact source reset behavior remain covered by the profile review gate."}
				}
			case "windowwidth", "windowheight":
				if runtimeSectionValue(values, "ApplyWinSizeByUser") == "0" {
					row = RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: "Saved window dimension is inactive because ApplyWinSizeByUser is disabled."}
				}
			case "userecordedduration":
				if value == "0" {
					row = RuntimeSettingDisposition{Status: RuntimeSettingDispositionNonApplicable, Note: "Recorded durations are preserved on source steps but source runtime duration replay is disabled."}
				}
			}
			rows[key] = row
		}
		_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
			action := ref.Action
			if !strings.HasPrefix(action.Type, "browser_") {
				return nil
			}
			if action.Params == nil {
				action.Params = map[string]any{}
			}
			if stepTimeout > 0 {
				action.TimeoutMs = stepTimeout
				if action.Sync != nil && action.Sync.TimeoutMs == 0 {
					action.Sync.TimeoutMs = stepTimeout
				}
			}
			if networkEndThreshold > 0 && action.Sync != nil && (action.Sync.Type == "network_idle" || action.Sync.Type == "networkidle") && action.Sync.StableMs == 0 {
				action.Sync.StableMs = networkEndThreshold
			}
			timeout := objectTimeout
			if value, ok := action.Metadata["source_object_timeout_seconds"].(string); ok {
				if n, valid := truClientTimeoutMS(value); valid {
					timeout = n
				}
			}
			if action.Type == "browser_navigate" {
				timeout = stepTimeout
			}
			if action.WaitFor != nil && (action.WaitFor.Type == "duration" || action.WaitFor.Type == "sleep") {
				timeout = stepTimeout
				action.WaitFor.TimeoutMs = stepTimeout
			}
			if timeout > 0 {
				if _, exists := action.Params["timeout_ms"]; !exists {
					action.Params["timeout_ms"] = timeout
				}
				if action.WaitFor != nil && action.WaitFor.TimeoutMs == 0 && action.WaitFor.Type != "duration" {
					action.WaitFor.TimeoutMs = timeout
				}
			}
			return nil
		})
	}
	if len(mappings) == 0 {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved[truClientRuntimeMappingsKey] = mappings
}

func refineTruClientBrowserRuntimeDispositions(rows map[string]map[string]map[string]RuntimeSettingDisposition, script *dsl.Script) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return
	}
	overrides, _ := script.Metadata.Import.Preserved[truClientRuntimeMappingsKey].(map[string]map[string]map[string]RuntimeSettingDisposition)
	for file, sections := range overrides {
		for section, keys := range sections {
			for key, row := range keys {
				if rows[file] != nil && rows[file][section] != nil {
					rows[file][section][key] = row
				}
			}
		}
	}
}

func appendTruClientSourceMappings(result *ConvertResult, files map[string]string, selectedXMLFile string) {
	if result == nil || result.Script == nil {
		return
	}
	xmlName := selectedXMLFile
	xmlText := truClientSourceText(files[selectedXMLFile])
	if xmlName == "" {
		return
	}
	_ = dslwalk.WalkActions(result.Script, func(ref dslwalk.ActionRef) error {
		action := ref.Action
		id, _ := action.Metadata["source_step_id"].(string)
		transaction, _ := action.Metadata["source_transaction"].(string)
		if id == "" && transaction == "" {
			return nil
		}
		if id == "" {
			id, _ = action.Metadata["source_anchor_step_id"].(string)
		}
		line := 1
		if position := strings.Index(xmlText, `stepId="`+id+`"`); position >= 0 {
			line += strings.Count(xmlText[:position], "\n")
		}
		function, _ := action.Metadata["source_action"].(string)
		if function == "" && transaction != "" {
			function = "Transaction definition"
		}
		status := "converted"
		if review, _ := action.Metadata["review_required"].(bool); review {
			status = "review_only"
		}
		appendSourceReviewMapping(result, SourceReviewMapping{SourceFile: xmlName, StartLine: line, Function: function, TargetPath: ref.Path, TargetName: action.Name, TargetKind: "action", Status: status, Notes: fmt.Sprintf("Source step %s; source attributes and completion rules are retained on the draft action.", id)})
		if downloadID, _ := action.Metadata["source_download_step_id"].(string); downloadID != "" && downloadID != id {
			downloadLine := 1
			if position := strings.Index(xmlText, `stepId="`+downloadID+`"`); position >= 0 {
				downloadLine += strings.Count(xmlText[:position], "\n")
			}
			appendSourceReviewMapping(result, SourceReviewMapping{SourceFile: xmlName, StartLine: downloadLine, Function: "Wait for File Download", TargetPath: ref.Path, TargetName: action.Name, TargetKind: "action", Status: status, Notes: fmt.Sprintf("Source step %s coalesced into pre-armed download action; source attributes and completion rules are retained on the draft action.", downloadID)})
		}
		return nil
	})
}

func truClientSourceText(content string) string {
	if !strings.ContainsRune(content, '\x00') {
		return content
	}
	bytes := []byte(content)
	if len(bytes) >= 2 && bytes[0] == 0xff && bytes[1] == 0xfe {
		bytes = bytes[2:]
	}
	words := make([]uint16, 0, len(bytes)/2)
	for i := 0; i+1 < len(bytes); i += 2 {
		words = append(words, binary.LittleEndian.Uint16(bytes[i:i+2]))
	}
	return string(utf16.Decode(words))
}
