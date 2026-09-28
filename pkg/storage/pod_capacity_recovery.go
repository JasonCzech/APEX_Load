package storage

import (
	"errors"
	"strings"
	"unicode"
)

// PodCapacityRecoveryPlacement binds a reserved recovery budget to identities
// observed through Kubernetes. Generator-reported host names are not authority.
// ReplacementSources records the planner's distinct destinations for each
// single-worker contingency. Live source claims have no replacement sources.
type PodCapacityRecoveryPlacement struct {
	Namespace          string   `json:"namespace"`
	WorkloadUID        string   `json:"workload_uid"`
	PodUID             string   `json:"pod_uid"`
	NodeName           string   `json:"node_name"`
	ReplacementSources []string `json:"replacement_sources,omitempty"`
}

func validatePodCapacityRecoveryPlacement(claim PodCapacityReservation) error {
	p := claim.RecoveryPlacement
	if p == nil {
		return nil
	}
	valid := func(value string) bool {
		return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
	}
	for _, value := range []string{p.Namespace, p.WorkloadUID, p.PodUID, p.NodeName} {
		if !valid(value) {
			return errors.New("invalid recovery capacity placement identity")
		}
	}
	if len(p.ReplacementSources) > 256 {
		return errors.New("recovery capacity placement exceeds source bound")
	}
	seen := map[string]bool{}
	for _, source := range p.ReplacementSources {
		if !valid(source) || source == claim.Target || seen[source] {
			return errors.New("invalid recovery capacity replacement source")
		}
		seen[source] = true
	}
	if len(p.ReplacementSources) > 0 && !claim.Dedicated {
		return errors.New("recovery spare must retain a dedicated budget")
	}
	return nil
}
