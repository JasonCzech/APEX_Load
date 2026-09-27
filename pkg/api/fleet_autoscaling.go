package api

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

var autoscalingFleetIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$`)

// New KEDA signals are installation-scoped and emitted only by a fully reconciled
// execution leader. Legacy controller metrics remain available for dashboards.
func (s *Server) writeFleetAutoscalingMetrics(body *strings.Builder, observedOwner string, stats scalingSignals, regions map[string]generatorRegionStats, floor int, complete bool) {
	fleet := strings.TrimSpace(os.Getenv("APEXLOAD_FLEET_ID"))
	if !autoscalingFleetIDPattern.MatchString(fleet) || strings.Contains(strings.ToLower(fleet), "change-me") || !complete {
		return
	}
	now := time.Now()
	until := now.Add(30 * time.Second)
	if leader := s.runner.leadership; leader != nil {
		generation, owner, deadline, valid := leader.executionGeneratorClaim()
		if !valid || generation == 0 || owner != observedOwner || !leader.IsAdmitted() {
			return
		}
		if deadline.Before(until) {
			until = deadline
		}
	}
	saturated := 0
	for _, region := range regions {
		saturated += region.CapacityStates["saturated"]
	}
	body.WriteString("# HELP apexload_fleet_autoscaling Fleet demand from the current execution leader.\n# TYPE apexload_fleet_autoscaling gauge\n")
	signals := []struct {
		name  string
		value int
	}{
		{"total_vusers", stats.ActiveVUsers + stats.QueuedVUsers},
		{"active_vusers", stats.ActiveVUsers},
		{"total_browser_vusers", stats.ActiveProtocolVUsers["browser"] + stats.QueuedProtocolVUsers["browser"]},
		{"active_browser_vusers", stats.ActiveProtocolVUsers["browser"]},
		{"reserved_replica_floor", floor},
		{"saturated_generators", saturated},
	}
	for _, signal := range signals {
		fmt.Fprintf(body, "apexload_fleet_autoscaling{fleet=%q,signal=%q} %d\n", fleet, signal.name, signal.value)
	}
	body.WriteString("# HELP apexload_fleet_autoscaling_valid_until Expiry of this leader observation in Unix seconds.\n# TYPE apexload_fleet_autoscaling_valid_until gauge\n")
	fmt.Fprintf(body, "apexload_fleet_autoscaling_valid_until{fleet=%q} %.3f\n", fleet, float64(until.UnixMilli())/1000)
}
