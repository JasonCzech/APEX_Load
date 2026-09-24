package api

import (
	"fmt"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"sort"
)

func distributedSourcePlan(groups []TestGroup, project string, generation uint64) (*pb.SourcePlanManifest, error) {
	var m *pb.SourcePlanManifest
	for _, g := range groups {
		if g.script != nil && g.script.Config.JMeter != nil {
			j := g.script.Config.JMeter
			if m == nil {
				m = &pb.SourcePlanManifest{Version: 1, ProjectId: project, SourcePlanId: j.SourcePlanID, ExecutionGeneration: generation, CompatibilityProfile: dsl.JMeterMessagingProfile, RequiredFeatures: []string{generatorrpc.FeatureSourcePlanV1, dsl.FeatureJMeterMessagingV1}}
			} else if m.SourcePlanId != j.SourcePlanID {
				return nil, fmt.Errorf("one source plan per distributed source-engine run is required")
			}
		}
	}
	if m == nil {
		return nil, nil
	}
	for _, g := range groups {
		if g.script == nil || g.script.Config.JMeter == nil || g.script.ProjectID != project {
			return nil, fmt.Errorf("source-plan execution cannot mix source identities or projects")
		}
		_, hash, _, err := generatorrpc.PrepareDistributedScript(g.script)
		if err != nil {
			return nil, err
		}
		m.Groups = append(m.Groups, &pb.SourcePlanGroup{GroupName: g.Name, SourceGroupId: g.script.Config.JMeter.SourceGroupID, ScriptHash: hash, Vusers: int32(g.VUsers)})
	}
	sort.Slice(m.Groups, func(i, j int) bool { return m.Groups[i].GroupName < m.Groups[j].GroupName })
	return m, nil
}
