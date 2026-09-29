package perfevidence

import "slices"

// Reasons are stable, bounded codes; never include process output, paths or
// user-provided errors in the report. A rejected observation stays rejected.
var javaIncompleteCodes = []string{
	"initial_memory_unavailable", "engine_setup_failed", "host_diagnostics_incomplete",
	"final_memory_unavailable", "resource_counter_regressed", "engine_run_failed",
	"measurement_not_completed", "latency_samples_missing", "caller_cancelled",
	"memory_observation_failed", "memory_pss_unavailable", "memory_cgroup_unavailable",
	"active_vuser_mismatch", "initialization_timeout", "latency_sample_invalid", "owner_lease_lost",
}

func (s *JavaEvidenceSample) incomplete(code string) {
	s.Complete = false
	if !slices.Contains(s.IncompleteReasons, code) {
		s.IncompleteReasons = append(s.IncompleteReasons, code)
	}
}
func validJavaIncompleteReasons(reasons []string, complete bool) bool {
	if len(reasons) > len(javaIncompleteCodes) || complete && len(reasons) > 0 {
		return false
	}
	seen := map[string]bool{}
	for _, r := range reasons {
		if seen[r] || !slices.Contains(javaIncompleteCodes, r) {
			return false
		}
		seen[r] = true
	}
	return true
}
func javaMemoryFailureCode(e error) string {
	if e != nil {
		switch e.Error() {
		case "Java process PSS unavailable":
			return "memory_pss_unavailable"
		case "cannot enumerate Java accounting cgroup", "Java cgroup memory unavailable", "Java cgroup memory events unavailable":
			return "memory_cgroup_unavailable"
		}
	}
	return "memory_observation_failed"
}
