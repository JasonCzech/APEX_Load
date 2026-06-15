package api

// distributed_compatibility.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"fmt"
	"sort"
	"strings"
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
		}
	}
	return spec
}

func generatorSpecVersion(spec generatorTargetSpec) string {
	if strings.TrimSpace(spec.Version) == "" {
		return "unknown"
	}
	return strings.TrimSpace(spec.Version)
}

func generatorSpecProtocols(spec generatorTargetSpec) []string {
	if len(spec.Protocols) == 0 {
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
	if version := strings.TrimSpace(snapshot.Version); version != "" {
		effective.Version = version
	}
	if len(snapshot.SupportedProtocols) > 0 {
		effective.Protocols = normalizedProtocolList(snapshot.SupportedProtocols)
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

func (i *generatorCompatibilityIndex) targetCanRun(target string, protocolName string) bool {
	if i == nil {
		return true
	}
	spec, ok := i.byTarget[target]
	if !ok {
		return true
	}
	return generatorCompatibilityState(spec) == "compatible" && targetSupportsProtocol(spec, protocolName)
}

func generatorPlacementRejectionMessage(group TestGroup, loadByTarget map[string]generatorLoadSnapshot, specs []generatorTargetSpec) string {
	protocolName := usageKey(group.Protocol, "unknown")
	reasons := make([]string, 0)
	for _, spec := range specs {
		if _, reachable := loadByTarget[spec.Address]; !reachable {
			continue
		}
		if snapshot, ok := loadByTarget[spec.Address]; ok {
			spec = generatorEffectiveSpec(spec, snapshot)
		}
		if issues := generatorCompatibilityIssues(spec); len(issues) > 0 {
			reasons = append(reasons, fmt.Sprintf("%s incompatible: %s", spec.Address, strings.Join(issues, "; ")))
			continue
		}
		if !targetSupportsProtocol(spec, group.Protocol) {
			reasons = append(reasons, fmt.Sprintf("%s does not support protocol %s", spec.Address, protocolName))
		}
	}
	if len(reasons) == 0 {
		return fmt.Sprintf("no compatible generator target is available for group %q using protocol %s", group.Name, protocolName)
	}
	sort.Strings(reasons)
	return fmt.Sprintf("no compatible generator target is available for group %q using protocol %s: %s", group.Name, protocolName, strings.Join(reasons, "; "))
}
