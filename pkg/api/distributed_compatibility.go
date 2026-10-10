package api

// distributed_compatibility.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func parseGeneratorTargetSpec(raw string) generatorTargetSpec {
	parts := strings.Split(raw, "|")
	spec := generatorTargetSpec{Address: strings.TrimSpace(parts[0])}
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(key)) {
		case "region":
			spec.Region = strings.TrimSpace(value)
		case "version":
			spec.Version = strings.TrimSpace(value)
		case "protocols":
			for _, protocol := range strings.Split(value, ",") {
				protocol = strings.TrimSpace(protocol)
				if protocol != "" {
					spec.Protocols = append(spec.Protocols, protocol)
				}
			}
		case "labels", "tags":
			spec.Labels = mergeStringMaps(spec.Labels, parseGeneratorTargetLabels(value))
		}
	}
	return spec
}

func parseGeneratorTargetLabels(raw string) map[string]string {
	out := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		var key, value string
		if left, right, ok := strings.Cut(item, ":"); ok {
			key, value = left, right
		} else if left, right, ok := strings.Cut(item, "="); ok {
			key, value = left, right
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeStringMaps(base map[string]string, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(overlay))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func generatorSpecVersion(spec generatorTargetSpec) string {
	if strings.TrimSpace(spec.Version) == "" {
		return "unknown"
	}
	return strings.TrimSpace(spec.Version)
}

func generatorSpecProtocols(spec generatorTargetSpec) []string {
	// A nil protocol slice means the configured target supplied no capability
	// declaration, so the controller's legacy bootstrap defaults remain a
	// provisional hint. A non-nil empty slice is an authoritative heartbeat
	// observation that the generator advertised no protocols.
	if spec.Protocols == nil {
		return defaultSupportedProtocols()
	}
	protocols := normalizedProtocolList(spec.Protocols)
	sort.Strings(protocols)
	return protocols
}

func normalizedProtocolList(protocols []string) []string {
	out := make([]string, 0, len(protocols))
	seen := make(map[string]struct{}, len(protocols))
	for _, protocol := range protocols {
		protocol = strings.TrimSpace(protocol)
		if protocol == "" {
			continue
		}
		if _, ok := seen[protocol]; ok {
			continue
		}
		seen[protocol] = struct{}{}
		out = append(out, protocol)
	}
	sort.Strings(out)
	return out
}

func generatorEffectiveSpec(spec generatorTargetSpec, snapshot generatorLoadSnapshot) generatorTargetSpec {
	effective := spec
	effective.Labels = cloneStringMap(spec.Labels)
	if version := strings.TrimSpace(snapshot.Version); version != "" {
		effective.Version = version
	}
	// A successful heartbeat is authoritative even when either advertised list
	// is empty. The non-nil checks retain compatibility with callers/tests that
	// provide negotiated lists without setting HeartbeatOK.
	if snapshot.HeartbeatOK || snapshot.SupportedProtocols != nil {
		effective.Protocols = normalizedProtocolList(snapshot.SupportedProtocols)
	}
	if snapshot.HeartbeatOK || snapshot.CompatibleFeatures != nil {
		effective.Features = normalizedProtocolList(snapshot.CompatibleFeatures)
	}
	return effective
}

func generatorCompatibilityState(spec generatorTargetSpec) string {
	if len(generatorCompatibilityIssues(spec)) > 0 {
		return "incompatible"
	}
	return "compatible"
}

func generatorCompatibilityIssues(spec generatorTargetSpec) []string {
	issues := []string{}
	version := generatorSpecVersion(spec)
	if version != "unknown" && !compatibleMajorVersion(version, "0.1.0") {
		issues = append(issues, fmt.Sprintf("generator version %s is incompatible with controller version 0.1.0", version))
	}
	return issues
}

func compatibleMajorVersion(candidate string, required string) bool {
	candidateMajor, candidateOK := leadingVersionPart(candidate)
	requiredMajor, requiredOK := leadingVersionPart(required)
	if !candidateOK || !requiredOK {
		return true
	}
	return candidateMajor == requiredMajor
}

func leadingVersionPart(version string) (string, bool) {
	version = strings.TrimSpace(version)
	if version == "" || version == "unknown" {
		return "", false
	}
	part, _, _ := strings.Cut(version, ".")
	part = strings.TrimSpace(part)
	part = strings.TrimLeft(part, "vV")
	if part == "" {
		return "", false
	}
	for _, r := range part {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return part, true
}

func targetSupportsProtocol(spec generatorTargetSpec, protocolName string) bool {
	protocolName = strings.TrimSpace(protocolName)
	if protocolName == "" {
		return true
	}
	for _, supported := range generatorSpecProtocols(spec) {
		if supported == protocolName {
			return true
		}
	}
	return false
}

func targetMatchesRequiredLabels(spec generatorTargetSpec, required map[string]string) bool {
	if len(required) == 0 {
		return true
	}
	for key, value := range required {
		if spec.Labels[key] != value {
			return false
		}
	}
	return true
}

type generatorCompatibilityIndex struct {
	byTarget map[string]generatorTargetSpec
}

func newGeneratorCompatibilityIndex(specs []generatorTargetSpec, loadByTarget map[string]generatorLoadSnapshot) *generatorCompatibilityIndex {
	index := &generatorCompatibilityIndex{byTarget: make(map[string]generatorTargetSpec, len(specs))}
	for _, spec := range specs {
		if snapshot, ok := loadByTarget[spec.Address]; ok {
			spec = generatorEffectiveSpec(spec, snapshot)
		}
		index.byTarget[spec.Address] = spec
	}
	return index
}

func (i *generatorCompatibilityIndex) targetCanRun(target string, protocolName string, requiredFeature string, requiredLabels map[string]string) bool {
	if i == nil {
		return true
	}
	spec, ok := i.byTarget[target]
	if !ok {
		return true
	}
	return generatorCompatibilityState(spec) == "compatible" && targetSupportsProtocol(spec, protocolName) &&
		protocol.RuntimeFeaturesSatisfy(spec.Features, requiredFeature) &&
		targetMatchesRequiredLabels(spec, requiredLabels)
}

// generatorPlacementRejectionMessage explains why a group could not be placed.
// inherited is the scenario/test-level (root) assignment so that an INHERITING
// group blocked by inherited required_labels reports the actual unmet labels
// instead of an empty/misleading reason.
func generatorPlacementRejectionMessage(group TestGroup, loadByTarget map[string]generatorLoadSnapshot, specs []generatorTargetSpec, inherited *GeneratorAssignmentSettings) string {
	if javaHostSettings(group) != nil {
		if groupInheritsAssignment(group) && inherited != nil &&
			(inherited.Mode == generatorAssignmentModeRegionWeighted || inherited.Mode == generatorAssignmentModeLoadZoneWeighted) {
			return fmt.Sprintf("java_host_inherited_weighted_placement_unsupported for group %q", group.Name)
		}
		if inherited != nil && inherited.PoolID != "" && inherited.Mode != generatorAssignmentModeSpecificHosts {
			return fmt.Sprintf("java_host_pool_membership_unresolved for group %q", group.Name)
		}
		admissions, err := javaHostScheduleAdmissions(group)
		if err != nil {
			return err.Error()
		}
		required := group
		required.VUsers = admissions
		reasons := []string{}
		for target, load := range loadByTarget {
			if _, err := javaHostPlacementForGroup(required, load); err != nil {
				reasons = append(reasons, target+": "+err.Error())
			}
		}
		if len(reasons) == 0 {
			reasons = append(reasons, "java_host_aggregate_capacity_or_placement_unavailable")
		}
		sort.Strings(reasons)
		return fmt.Sprintf("java_host_placement_unavailable for group %q (%d lifetime admissions required): %s", group.Name, admissions, strings.Join(reasons, "; "))
	}
	protocolName := usageKey(group.Protocol, "unknown")
	reasons := make([]string, 0)
	if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeSpecificHosts {
		for _, target := range group.Assignment.SelectedTargets {
			target = strings.TrimSpace(target)
			if target == "" {
				continue
			}
			if _, reachable := loadByTarget[target]; !reachable {
				reasons = append(reasons, fmt.Sprintf("%s is not reachable", target))
			}
		}
	}
	for _, spec := range specs {
		snapshot, reachable := loadByTarget[spec.Address]
		if !reachable {
			continue
		}
		spec = generatorEffectiveSpec(spec, snapshot)
		if issues := generatorCompatibilityIssues(spec); len(issues) > 0 {
			reasons = append(reasons, fmt.Sprintf("%s incompatible: %s", spec.Address, strings.Join(issues, "; ")))
			continue
		}
		if !targetSupportsProtocol(spec, group.Protocol) {
			reasons = append(reasons, fmt.Sprintf("%s does not support protocol %s", spec.Address, protocolName))
			continue
		}
		if requiredFeature := groupRequiredRuntimeFeature(group); !protocol.RuntimeFeaturesSatisfy(spec.Features, requiredFeature) {
			missing := requiredFeature
			if strings.EqualFold(requiredFeature, protocol.FeatureJavaVuserPersistentLifecycleV1) {
				missing = protocol.MissingJavaVuserPersistentRuntimeFeature(spec.Features)
			}
			reasons = append(reasons, fmt.Sprintf("%s does not advertise required runtime feature %s", spec.Address, missing))
			continue
		}
		requiredLabels := effectiveGroupRequiredLabels(group, inherited)
		if !targetMatchesRequiredLabels(spec, requiredLabels) {
			reasons = append(reasons, fmt.Sprintf("%s does not match required labels %s", spec.Address, formatRequiredLabels(requiredLabels)))
			continue
		}
		if int64(snapshot.AvailableCapacity) < int64(group.VUsers) {
			reasons = append(reasons, fmt.Sprintf("%s has %d available VUsers for group demand %d", spec.Address, max(0, snapshot.AvailableCapacity), group.VUsers))
		}
		if reason := runtimeResourcePlacementReason(group, snapshot); reason != "" {
			reasons = append(reasons, spec.Address+": "+reason)
		}
		requiredOccupancy := javaVuserOccupancyModeForGroup(group)
		if occupied, advertised := protocolRuntimeOccupancyMode(snapshot, "java_vuser"); requiredOccupancy != "" &&
			advertised && occupied != generatorrpc.RuntimeOccupancyModeIdle && occupied != requiredOccupancy {
			reasons = append(reasons, fmt.Sprintf(
				"%s java_vuser runtime occupancy %q cannot accept %s lifecycle work",
				spec.Address,
				occupied,
				requiredOccupancy,
			))
			continue
		}
		if available, advertised := protocolRuntimeAvailableVUsers(snapshot, "java_vuser"); advertised {
			demand := javaVuserCapacityPlanForGroup(group).Demand(group.VUsers)
			if demand > available {
				reasons = append(reasons, fmt.Sprintf(
					"%s has %d available java_vuser runtime slots for demand %d",
					spec.Address,
					available,
					demand,
				))
			}
		}
	}
	if len(reasons) == 0 {
		return fmt.Sprintf("no compatible generator target is available for group %q using protocol %s", group.Name, protocolName)
	}
	sort.Strings(reasons)
	message := fmt.Sprintf("no compatible generator target is available for group %q using protocol %s: %s", group.Name, protocolName, strings.Join(reasons, "; "))
	if strings.Contains(message, dsl.FeatureNativeHTTP3V1) {
		// Generators advertise native_http3_v1 only with the operator flag on
		// and an HTTP/3-capable build; say so, so the fix is clear.
		message += fmt.Sprintf(" (group %q uses HTTP/3: generators advertise %s only when %s is on and their build supports HTTP/3)", group.Name, dsl.FeatureNativeHTTP3V1, httpprotocol.EnvHTTP3)
	}
	return message
}
