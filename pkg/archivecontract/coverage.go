package archivecontract

import "strings"

type RunArchiveSourceCoverage struct {
	Mode    string             `json:"mode"`
	Sources []RunArchiveSource `json:"sources"`
}

type RunArchiveSource struct {
	Target    string `json:"target"`
	Collected bool   `json:"collected"`
	// GeneratorID, Region and Groups name the generator and the groups it ran,
	// so a missing source says which part of the load has no evidence.
	GeneratorID string   `json:"generator_id,omitempty"`
	Region      string   `json:"region,omitempty"`
	Groups      []string `json:"groups,omitempty"`
}

// Label names a retained source using its generator, address, region and groups.
func (source RunArchiveSource) Label() string {
	target := strings.TrimSpace(source.Target)
	name := strings.TrimSpace(source.GeneratorID)
	if name == "" {
		name = target
	}
	var details []string
	if name != target && target != "" {
		details = append(details, target)
	}
	if region := strings.TrimSpace(source.Region); region != "" {
		details = append(details, region)
	}
	switch len(source.Groups) {
	case 0:
	case 1:
		details = append(details, "group "+source.Groups[0])
	default:
		details = append(details, "groups "+strings.Join(source.Groups, ", "))
	}
	if len(details) > 0 {
		name += " (" + strings.Join(details, "; ") + ")"
	}
	return name
}
