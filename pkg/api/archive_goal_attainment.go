package api

import "math"

// goalAttainmentTolerancePct is how far a 10 s window may sit from its target
// and still count as on target in the report. It is wider than the live
// warning (goalGapAlertPct) because the report judges a whole run, where a
// window straddling a target change is expected to land between two points.
const goalAttainmentTolerancePct = 10.0

// buildRunArchiveGoalAttainment buckets a run's interval samples that carry a
// TPS target into goal windows and judges each against its target. Runs with
// no TPS target have no attainment. An interval's TPS covers every group, so
// the groups that carry no target (observer canaries, groups a traffic
// profile does not map) are taken out of it, as the live goal window does.
func buildRunArchiveGoalAttainment(summaries []IntervalSummaryRecord, untargeted map[string]bool) *RunArchiveGoalAttainment {
	type window struct {
		tps, target float64
		samples     int
	}
	var windows []window
	windowIndex := int64(-1)
	totalTPS, totalTarget, samples := 0.0, 0.0, 0
	var first int64
	firstSet := false
	for _, summary := range summaries {
		if summary.TargetTPS <= 0 || summary.Timestamp.IsZero() || math.IsNaN(summary.TPS) || math.IsInf(summary.TPS, 0) {
			continue
		}
		at := summary.Timestamp.UnixNano()
		if !firstSet {
			first, firstSet = at, true
		}
		index := (at - first) / int64(goalThroughputWindow)
		if index != windowIndex || len(windows) == 0 {
			windows = append(windows, window{})
			windowIndex = index
		}
		tps := summary.TPS
		for name, group := range summary.Groups {
			if untargeted[name] && group != nil {
				tps -= group.TPS
			}
		}
		tps = max(tps, 0)
		current := &windows[len(windows)-1]
		current.tps += tps
		current.target += summary.TargetTPS
		current.samples++
		totalTPS += tps
		totalTarget += summary.TargetTPS
		samples++
	}
	if samples == 0 {
		return nil
	}
	attainment := &RunArchiveGoalAttainment{
		TargetAverageTPS:   totalTarget / float64(samples),
		AchievedAverageTPS: totalTPS / float64(samples),
		WindowSeconds:      int64(goalThroughputWindow.Seconds()),
		TolerancePct:       goalAttainmentTolerancePct,
		Windows:            int64(len(windows)),
	}
	for _, current := range windows {
		target := current.target / float64(current.samples)
		gap := (target - current.tps/float64(current.samples)) / target * 100
		switch {
		case gap >= goalAttainmentTolerancePct:
			attainment.UnderWindows++
		case gap <= -goalAttainmentTolerancePct:
			attainment.OverWindows++
		default:
			attainment.WithinWindows++
		}
	}
	return attainment
}

// untargetedGoalGroups names a goal run's groups that carry no goal target:
// observer canaries, and in a traffic-profile run the groups the profile does
// not map (currentGoalTargetStatuses leaves both out).
func untargetedGoalGroups(provenance RunArchiveProvenance) map[string]bool {
	var mapped map[string]bool
	if profile := provenance.TrafficProfile; profile != nil && profile.RuntimeTarget == "tps" {
		mapped = make(map[string]bool, len(profile.Mappings))
		for _, mapping := range profile.Mappings {
			mapped[normalizeEffectiveGroupName(mapping.GroupName)] = true
		}
	}
	untargeted := map[string]bool{}
	for _, group := range provenance.Groups {
		if group.Role == GroupRoleObserver || (mapped != nil && !mapped[normalizeEffectiveGroupName(group.Name)]) {
			untargeted[group.Name] = true
		}
	}
	return untargeted
}
