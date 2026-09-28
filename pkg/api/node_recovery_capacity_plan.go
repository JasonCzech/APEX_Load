package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// recoveryCapacityParticipant is one indivisible source generator's frozen
// peak demand. Recovery initially requires unused destination pods because
// evacuation cannot move a second source onto an already participating pod.
type recoveryCapacityParticipant struct {
	Target, PodUID, Incarnation, Node string
	Demand                            protocol.CapacityVector
}

// recoveryCapacitySpare describes an already eligible, idle, unreserved pod.
// The caller must establish freshness, namespace/cluster identity, permissions,
// protocol and assignment compatibility, then supply the exact eligible sources.
// This pure planner neither trusts generator-reported node names nor performs
// reservation, fencing, quota admission, or execution.
type recoveryCapacitySpare struct {
	Target, PodUID, Incarnation, Node string
	Budget                            protocol.CapacityVector
	EligibleSources                   []string
}

type nodeRecoveryCapacityPlan struct {
	// Replacements has a distinct destination for each lost source within every
	// single-node contingency. A spare may serve different alternative failures.
	Replacements map[string]map[string]string
	SpareTargets []string
}

const maxRecoveryCapacityPlanPods = 256

func planNodeRecoveryCapacity(participants []recoveryCapacityParticipant, spares []recoveryCapacitySpare, maximumReplicas int) (*nodeRecoveryCapacityPlan, error) {
	if len(participants) == 0 || len(participants)+len(spares) > maxRecoveryCapacityPlanPods || maximumReplicas <= 0 || maximumReplicas > maxRecoveryCapacityPlanPods {
		return nil, fmt.Errorf("node recovery requires a bounded nonempty placement")
	}
	sources := append([]recoveryCapacityParticipant(nil), participants...)
	candidates := append([]recoveryCapacitySpare(nil), spares...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Target < sources[j].Target })
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Target < candidates[j].Target })
	targets, pods, incarnations := map[string]bool{}, map[string]bool{}, map[string]bool{}
	validIdentity := func(target, uid, incarnation, node string) bool {
		for _, value := range []string{target, uid, incarnation, node} {
			if value == "" || len(value) > 512 || strings.TrimSpace(value) != value {
				return false
			}
		}
		if targets[target] || pods[uid] || incarnations[incarnation] {
			return false
		}
		targets[target], pods[uid], incarnations[incarnation] = true, true, true
		return true
	}
	byNode := map[string][]int{}
	sourceIDs := map[string]bool{}
	for i, p := range sources {
		if !validIdentity(p.Target, p.PodUID, p.Incarnation, p.Node) || len(p.Demand.Protocols) > 64 || p.Demand.Validate(false) != nil || p.Demand.VUsers <= 0 {
			return nil, fmt.Errorf("invalid recovery participant identity or demand")
		}
		byNode[p.Node] = append(byNode[p.Node], i)
		sourceIDs[p.Target] = true
	}
	edges := make([][]int, len(sources))
	for j, c := range candidates {
		if !validIdentity(c.Target, c.PodUID, c.Incarnation, c.Node) || len(c.Budget.Protocols) > 64 || c.Budget.Validate(true) != nil {
			return nil, fmt.Errorf("invalid spare identity or capacity")
		}
		eligible := map[string]bool{}
		for _, target := range c.EligibleSources {
			if !sourceIDs[target] || eligible[target] {
				return nil, fmt.Errorf("invalid spare source eligibility")
			}
			eligible[target] = true
		}
		for i, p := range sources {
			if eligible[p.Target] && p.Node != c.Node && c.Budget.Fits(p.Demand) == nil {
				edges[i] = append(edges[i], j)
			}
		}
	}
	nodes := make([]string, 0, len(byNode))
	for node := range byNode {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	result := &nodeRecoveryCapacityPlan{Replacements: map[string]map[string]string{}}
	used := map[string]bool{}
	for _, node := range nodes {
		match := map[int]int{}
		var assign func(int, map[int]bool) bool
		assign = func(source int, visited map[int]bool) bool {
			for _, spare := range edges[source] {
				if visited[spare] {
					continue
				}
				visited[spare] = true
				old, occupied := match[spare]
				if !occupied || assign(old, visited) {
					match[spare] = source
					return true
				}
			}
			return false
		}
		for _, source := range byNode[node] {
			if !assign(source, map[int]bool{}) {
				return nil, fmt.Errorf("node %s lacks distinct compatible unused replacement pods", node)
			}
		}
		replacements := map[string]string{}
		for spare, source := range match {
			replacements[sources[source].Target] = candidates[spare].Target
			used[candidates[spare].Target] = true
		}
		result.Replacements[node] = replacements
	}
	// This deterministic matching is conservative; it does not optimize the
	// union of spares across alternative node failures. Never exceed the cap.
	if len(sources)+len(used) > maximumReplicas {
		return nil, fmt.Errorf("node recovery placement exceeds the replica limit")
	}
	for target := range used {
		result.SpareTargets = append(result.SpareTargets, target)
	}
	sort.Strings(result.SpareTargets)
	return result, nil
}
