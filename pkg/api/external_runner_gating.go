package api

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
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

func (tr *TestRunner) javaVuserRunnerLaunchConflictsLocked(groups []TestGroup) []LaunchConflict {
	for _, group := range groups {
		if !groupRequiresJavaVuserRunner(group) {
			continue
		}
		if tr.javaVuserRunnerAvailableLocked() {
			return nil
		}
		return []LaunchConflict{{
			Code:       "java_vuser_runner_required",
			Severity:   "blocking",
			Source:     "protocol_runtime",
			ResourceID: "java_vuser",
			Message:    javaVuserRunnerRequiredMessage(),
		}}
	}
	return nil
}

func (tr *TestRunner) javaVuserRunnerAvailableLocked() bool {
	if tr != nil {
		if backend, ok := tr.execution.(*distributedExecutionBackend); ok {
			return tr.distributedJavaVuserRunnerAvailableLocked(backend)
		}
	}
	return config.JavaVuserRunnerEnabled()
}

func (tr *TestRunner) distributedJavaVuserRunnerAvailableLocked(backend *distributedExecutionBackend) bool {
	if backend == nil {
		return false
	}
	specs := tr.enrichGeneratorTargetSpecsWithLabels(backend.configuredTargetSpecs())
	for _, spec := range specs {
		if backend.generatorDrainState(spec.Address) != "active" {
			continue
		}
		if generatorSpecHasJavaVuserRunner(spec) {
			return true
		}
	}
	for _, entry := range tr.cachedGeneratorInventory() {
		if !generatorInventoryEntryReachable(entry) || entry.DrainState != "active" {
			continue
		}
		if generatorInventoryHasJavaVuserRunner(entry) {
			return true
		}
	}
	return false
}

func (tr *TestRunner) cachedGeneratorInventory() []GeneratorInventoryEntry {
	if tr == nil {
		return nil
	}
	tr.liveInventoryMu.RLock()
	entries := cloneGeneratorInventoryEntries(tr.liveInventory)
	capturedAt := tr.liveInventoryAt
	tr.liveInventoryMu.RUnlock()
	if len(entries) == 0 || capturedAt.IsZero() {
		return nil
	}
	if time.Since(capturedAt) > defaultGeneratorSnapshotTTL {
		return nil
	}
	return entries
}

func generatorSpecHasJavaVuserRunner(spec generatorTargetSpec) bool {
	if hasJavaVuserRunnerLabels(spec.Labels) {
		return true
	}
	for _, protocolName := range spec.Protocols {
		if strings.EqualFold(strings.TrimSpace(protocolName), "java_vuser") {
			return true
		}
	}
	return false
}

func generatorInventoryHasJavaVuserRunner(entry GeneratorInventoryEntry) bool {
	if hasJavaVuserRunnerLabels(entry.Labels) {
		return true
	}
	return stringSliceContainsEqualFold(entry.SupportedProtocols, "java_vuser") ||
		stringSliceContainsEqualFold(entry.CompatibleFeatures, "java_vuser")
}

func hasJavaVuserRunnerLabels(labels map[string]string) bool {
	if len(labels) == 0 {
		return false
	}
	runtime := strings.ToLower(strings.TrimSpace(labels["runtime"]))
	image := strings.ToLower(strings.TrimSpace(labels["image"]))
	return runtime == "java_vuser" || image == "generator-java"
}

func generatorInventoryEntryReachable(entry GeneratorInventoryEntry) bool {
	switch strings.ToLower(strings.TrimSpace(entry.Status)) {
	case "connected", "healthy", "ready":
		return true
	default:
		return false
	}
}

func stringSliceContainsEqualFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

func groupRequiresJavaVuserRunner(group TestGroup) bool {
	protocolName := strings.ToLower(strings.TrimSpace(group.Protocol))
	if protocolName == "" && group.script != nil {
		protocolName = strings.ToLower(strings.TrimSpace(group.script.Protocol))
	}
	if protocolName != "java_vuser" {
		return false
	}
	if group.script == nil {
		return true
	}
	return javaVuserActionsRequireRunner(group.script.Init) ||
		javaVuserActionsRequireRunner(group.script.Actions) ||
		javaVuserActionsRequireRunner(group.script.Teardown)
}

func javaVuserActionsRequireRunner(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			return true
		}
		if action.Control != nil {
			if javaVuserActionsRequireRunner(action.Control.Steps) || javaVuserActionsRequireRunner(action.Control.Else) {
				return true
			}
		}
	}
	return false
}

func javaVuserRunnerLaunchConflict(groups []TestGroup) *LaunchConflict {
	conflicts := (&TestRunner{}).javaVuserRunnerLaunchConflictsLocked(groups)
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
	return "Java Vuser sidecar actions (java_vuser_run) require a governed generator-java JVM runner pool. An operator must enable it on the runner fleet via APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true (with the runner command, classpath, and working directory configured through APEXLOAD_JAVA_VUSER_*). Review-only Java Vuser imports do not require this runner; script config cannot enable it."
}
