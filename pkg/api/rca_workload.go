package api

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/rca"
)

// BaselineWorkloadQualification is the comparison's workload block: the
// current run's offered workload and, per baseline ID, whether that baseline
// offered the same load.
type BaselineWorkloadQualification struct {
	Current   rca.WorkloadKey                      `json:"current"`
	Baselines map[string]rca.WorkloadQualification `json:"baselines"`
}

// frozenWorkloadKey records the offered workload from the compiled groups at
// run start. scripts holds each group's disclosure-safe script digest.
func frozenWorkloadKey(p *RunArchiveProvenance, test *ManagedTest, scripts map[string]string) *rca.WorkloadKey {
	compiled := map[string]*dsl.Script{}
	for _, g := range test.Groups {
		if g.script != nil {
			compiled[g.Name] = g.script
		}
	}
	key := &rca.WorkloadKey{Version: rca.WorkloadKeyVersion, Source: rca.WorkloadKeyFrozen, Groups: map[string]rca.WorkloadGroup{}, RuntimeDigest: workloadRuntimeDigest(p, nil)}
	for _, g := range p.Groups {
		group := workloadGroup(g, compiled[g.Name])
		group.ScriptDigest = scripts[g.Name]
		key.Groups[g.Name] = group
	}
	return key
}

// comparisonWorkloadKey reads the frozen key from the run's initial
// provenance, or derives what the recorded provenance supports for runs
// archived before keys were frozen. Groups take their comparison names.
func comparisonWorkloadKey(r *baselineComparisonRun, mappings map[string]string, population string) rca.WorkloadKey {
	provenance := r.manifest.Provenance
	if r.manifest.InitialProvenance != nil {
		provenance = *r.manifest.InitialProvenance
	}
	comparisonName := func(name string) string {
		if mapped := mappings[r.identity.ID+"/"+name]; mapped != "" {
			return mapped
		}
		return name
	}
	var key rca.WorkloadKey
	if provenance.Workload != nil && provenance.Workload.Recorded() {
		key = *provenance.Workload
	} else {
		key = rca.WorkloadKey{Version: rca.WorkloadKeyVersion, Source: rca.WorkloadKeyDerived, Groups: map[string]rca.WorkloadGroup{}}
		for _, g := range provenance.Groups {
			key.Groups[g.Name] = workloadGroup(g, nil)
		}
	}
	named := make(map[string]rca.WorkloadGroup, len(key.Groups))
	for name, group := range key.Groups {
		group.Levels = append([]float64(nil), group.Levels...)
		named[comparisonName(name)] = group
	}
	key.Groups = named
	// The runtime digest is keyed by group name, so it is taken under the
	// comparison names; a renamed group with the same settings then matches.
	key.RuntimeDigest = workloadRuntimeDigest(&provenance, comparisonName)
	key.Population = population
	return key
}

// workloadGroup describes one group's planned load. Without the compiled
// script the arrival model is known only when runtime overrides decide it.
func workloadGroup(g TestGroup, script *dsl.Script) rca.WorkloadGroup {
	var pacing dsl.PacingConfig
	var think dsl.ThinkTimeConfig
	model := rca.ArrivalUnknown
	if script != nil {
		pacing, think = script.Config.Pacing, script.Config.ThinkTime
		model = rca.ArrivalClosed
		if engine.IsOpenWorkload(script) {
			model = rca.ArrivalOpen
		}
	} else if g.RuntimeSettings != nil {
		if g.RuntimeSettings.Pacing != nil {
			pacing = *g.RuntimeSettings.Pacing
		}
		if g.RuntimeSettings.ThinkTime != nil {
			think = *g.RuntimeSettings.ThinkTime
		}
		if g.RuntimeSettings.RunLogic != nil && g.RuntimeSettings.RunLogic.WorkloadModel != "" && g.RuntimeSettings.Pacing != nil {
			model = rca.ArrivalClosed
			if dsl.CanonicalWorkloadModel(g.RuntimeSettings.RunLogic.WorkloadModel) == "open" && dsl.CanonicalPacingType(pacing.Type) == "tps" && pacing.TPS > 0 {
				model = rca.ArrivalOpen
			}
		}
	}
	group := rca.WorkloadGroup{ArrivalModel: model, Unit: rca.LoadUnitVUsers, Levels: []float64{}}
	appendLevel := func(level float64) {
		if level > 0 && (len(group.Levels) == 0 || group.Levels[len(group.Levels)-1] != level) {
			group.Levels = append(group.Levels, level)
		}
	}
	rateStages := false
	for _, stage := range g.Stages {
		rateStages = rateStages || stage.TargetTPS != nil
	}
	switch {
	case model == rca.ArrivalOpen:
		// The open model's arrival rate is its offered load; pacing carries only
		// the remaining policy.
		group.Unit = rca.LoadUnitTPS
		appendLevel(pacing.TPS)
		pacing.TPS = 0
	case rateStages:
		// Goal-oriented stages offer a target rate that the controller meets by
		// adjusting VUsers.
		group.Unit = rca.LoadUnitTPS
		for _, stage := range g.Stages {
			if stage.TargetTPS != nil {
				appendLevel(*stage.TargetTPS)
			}
		}
	case len(g.Stages) > 0:
		for _, stage := range g.Stages {
			appendLevel(float64(stage.TargetVUsers))
		}
	default:
		appendLevel(float64(g.VUsers))
	}
	group.Pacing = workloadPacingPolicy(pacing, think)
	return group
}

// workloadPacingPolicy is a canonical, comparable description of pacing and
// think time.
func workloadPacingPolicy(pacing dsl.PacingConfig, think dsl.ThinkTimeConfig) string {
	pacing.Type = dsl.CanonicalPacingType(pacing.Type)
	think.Type = strings.ToLower(strings.TrimSpace(think.Type))
	if think.Multiplier == 0 {
		think.Multiplier = 1
	}
	encoded, err := json.Marshal(struct {
		Pacing    dsl.PacingConfig    `json:"pacing"`
		ThinkTime dsl.ThinkTimeConfig `json:"think_time"`
	}{pacing, think})
	if err != nil {
		return ""
	}
	return string(encoded)
}

// workloadRuntimeDigest covers the scenario and group runtime overrides other
// than pacing and think time, which each group key compares on its own. name
// maps each group to the name it is compared under; nil keeps its own.
func workloadRuntimeDigest(p *RunArchiveProvenance, name func(string) string) string {
	strip := func(settings *RuntimeSettingsOverride) *RuntimeSettingsOverride {
		if settings == nil {
			return nil
		}
		copied := *settings
		copied.Pacing, copied.ThinkTime = nil, nil
		return &copied
	}
	groups := map[string]*RuntimeSettingsOverride{}
	for _, g := range p.Groups {
		key := g.Name
		if name != nil {
			key = name(g.Name)
		}
		groups[key] = strip(g.RuntimeSettings)
	}
	return comparisonDigest(struct {
		Scenario *RuntimeSettingsOverride
		Groups   map[string]*RuntimeSettingsOverride
	}{strip(p.RuntimeSettings), groups})
}

// achievedComparisonTPS is the run's time-weighted successful transaction rate
// while VUsers were active. It is reported as an outcome, never a qualifier.
func achievedComparisonTPS(summaries []IntervalSummaryRecord, started time.Time) float64 {
	total, seconds := 0.0, 0.0
	for i, summary := range summaries {
		duration, _ := loadComparisonIntervalSeconds(summaries, i, started)
		if duration <= 0 || summary.ActiveVUsers <= 0 || !finiteFloat(summary.TPS) {
			continue
		}
		total += summary.TPS * duration
		seconds += duration
	}
	if seconds == 0 {
		return 0
	}
	return total / seconds
}
