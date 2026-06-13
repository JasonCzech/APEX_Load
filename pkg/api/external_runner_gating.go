package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func externalRunnerLaunchConflicts(groups []TestGroup) []LaunchConflict {
	registered := make(map[string]struct{})
	for _, name := range protocol.List() {
		registered[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	externalRunnerProtocols := protocolBundleExternalRunnerProtocolSet()
	conflictsByProtocol := make(map[string]LaunchConflict)
	for _, group := range groups {
		protocolName := strings.ToLower(strings.TrimSpace(group.Protocol))
		if protocolName == "" && group.script != nil {
			protocolName = strings.ToLower(strings.TrimSpace(group.script.Protocol))
		}
		if protocolName == "" {
			continue
		}
		if _, ok := registered[protocolName]; ok {
			continue
		}
		if _, ok := externalRunnerProtocols[protocolName]; !ok {
			continue
		}
		conflictsByProtocol[protocolName] = LaunchConflict{
			Code:       "external_runner_required",
			Severity:   "blocking",
			Source:     "protocol_runtime",
			ResourceID: protocolName,
			Message:    externalRunnerRequiredMessage(protocolName),
		}
	}
	protocols := make([]string, 0, len(conflictsByProtocol))
	for protocolName := range conflictsByProtocol {
		protocols = append(protocols, protocolName)
	}
	sort.Strings(protocols)
	conflicts := make([]LaunchConflict, 0, len(protocols))
	for _, protocolName := range protocols {
		conflicts = append(conflicts, conflictsByProtocol[protocolName])
	}
	return conflicts
}

func protocolBundleExternalRunnerProtocolSet() map[string]struct{} {
	protocols := make(map[string]struct{})
	for _, entry := range protocolBundleSupportMatrix() {
		if entry.Status != "external_runner_required" && entry.ExecutionPosture != "external_runner_required" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(entry.Protocol))
		if name == "" || name == "java_vuser" {
			continue
		}
		protocols[name] = struct{}{}
	}
	return protocols
}

func externalRunnerLaunchConflict(groups []TestGroup) *LaunchConflict {
	conflicts := externalRunnerLaunchConflicts(groups)
	if len(conflicts) == 0 {
		return nil
	}
	return &conflicts[0]
}

func javaVuserRunnerLaunchConflicts(groups []TestGroup) []LaunchConflict {
	// Enablement is operator-owned: the gate reflects whether an operator has
	// enabled the Java Vuser runner on this controller/generator fleet via
	// APEXLOAD_JAVA_VUSER_RUNNER_ENABLED. A script's config.java_vuser.enabled
	// flag is inert and intentionally NOT consulted here — otherwise any script
	// author could clear this blocking gate and drive local process execution.
	if config.JavaVuserRunnerEnabled() {
		return nil
	}
	for _, group := range groups {
		protocolName := strings.ToLower(strings.TrimSpace(group.Protocol))
		if protocolName == "" && group.script != nil {
			protocolName = strings.ToLower(strings.TrimSpace(group.script.Protocol))
		}
		if protocolName != "java_vuser" {
			continue
		}
		return []LaunchConflict{{
			Code:       "java_vuser_runner_required",
			Severity:   "blocking",
			Source:     "protocol_runtime",
			ResourceID: protocolName,
			Message:    javaVuserRunnerRequiredMessage(),
		}}
	}
	return nil
}

func javaVuserRunnerLaunchConflict(groups []TestGroup) *LaunchConflict {
	conflicts := javaVuserRunnerLaunchConflicts(groups)
	if len(conflicts) == 0 {
		return nil
	}
	return &conflicts[0]
}

func externalRunnerRequiredMessage(protocolName string) string {
	switch strings.ToLower(strings.TrimSpace(protocolName)) {
	case "sap_gui", "sapgui":
		return "SAP GUI scripts require the windows_sap_gui_runner bridge with SAP GUI Scripting enabled before launch."
	default:
		return fmt.Sprintf("%s scripts require an external runner bridge before launch.", protocolName)
	}
}

func javaVuserRunnerRequiredMessage() string {
	return "Java Vuser scripts require a governed generator-java JVM runner pool. An operator must enable it on the runner fleet via APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true (with the runner command, classpath, and working directory configured through APEXLOAD_JAVA_VUSER_*). Script config cannot enable the runner."
}
