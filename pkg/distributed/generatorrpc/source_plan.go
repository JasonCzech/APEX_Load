package generatorrpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/protobuf/proto"
)

const FeatureSourcePlanV1 = "source_plan_execution_v1"

// SourcePlanDigest binds the release barrier to the exact staged membership.
func SourcePlanDigest(m *pb.SourcePlanManifest) string {
	if m == nil {
		return ""
	}
	b, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(m)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func validateSourcePlanAssignment(a *pb.WorkAssignment) error {
	m := a.GetSourcePlan()
	if m == nil {
		return nil
	}
	if m.Version != 1 || m.ProjectId == "" || len(m.ProjectId) > 256 || m.SourcePlanId == "" || len(m.SourcePlanId) > 256 || m.ExecutionGeneration != a.ExecutionGeneration || len(m.Groups) == 0 || len(m.Groups) > 256 {
		return fmt.Errorf("invalid source-plan manifest")
	}
	if a.ShardCount > 1 || a.ShardOrdinal != 0 || a.DurationOffset != nil && a.DurationOffset.AsDuration() != 0 {
		return fmt.Errorf("source plans cannot shard or resume")
	}
	if m.CompatibilityProfile == "" {
		if len(m.RequiredFeatures) != 1 || m.RequiredFeatures[0] != FeatureSourcePlanV1 {
			return fmt.Errorf("unsupported source-plan capabilities")
		}
	} else if m.CompatibilityProfile != dsl.JMeterMessagingProfile || len(m.RequiredFeatures) != 2 || m.RequiredFeatures[0] != FeatureSourcePlanV1 || m.RequiredFeatures[1] != dsl.FeatureJMeterMessagingV1 {
		return fmt.Errorf("unsupported source-plan compatibility requirements")
	}
	found := false
	prev := ""
	sources := map[string]bool{}
	for _, g := range m.Groups {
		if g == nil || g.GroupName == "" || len(g.GroupName) > 256 || g.GroupName <= prev || g.SourceGroupId == "" || len(g.SourceGroupId) > 256 || sources[g.SourceGroupId] || g.Vusers < 1 || len(g.ScriptHash) != 64 {
			return fmt.Errorf("invalid or duplicate source-plan group")
		}
		if _, err := hex.DecodeString(g.ScriptHash); err != nil {
			return fmt.Errorf("invalid source-plan script hash")
		}
		prev = g.GroupName
		sources[g.SourceGroupId] = true
		if g.GroupName == a.GroupName {
			found = true
			if g.ScriptHash != a.ExpectedScriptHash || g.Vusers != a.VuserCount {
				return fmt.Errorf("source-plan assignment does not match manifest")
			}
		}
	}
	if sourcePlanUsers(m) > maxGeneratorVUsers {
		return fmt.Errorf("source-plan population exceeds generator limit")
	}
	if !found {
		return fmt.Errorf("unexpected source-plan group")
	}
	return nil
}

// A sourcePlanExecution owns one engine. Group records retain their own sinks
// and lifecycle reporting, but cannot independently restart shared state.
type sourcePlanExecution struct {
	engine          *engine.MultiGroupEngine
	telemetryGroup  string
	processorGroups map[string]bool
	once            sync.Once
	done            chan struct{}
	err             error
}

func (o *sourcePlanExecution) run(ctx context.Context, e *groupExecution) error {
	o.once.Do(func() {
		defer func() {
			if recover() != nil {
				o.err = fmt.Errorf("source-plan engine failed unexpectedly")
			}
			close(o.done)
		}()
		_, o.err = o.engine.RunAt(ctx, e.startAt)
	})
	<-o.done
	return o.err
}

func assembleSourcePlan(executions map[string]*groupExecution) (*sourcePlanExecution, error) {
	if len(executions) == 0 {
		return nil, fmt.Errorf("source-plan groups are not staged")
	}
	names := make([]string, 0, len(executions))
	for n := range executions {
		names = append(names, n)
	}
	sort.Strings(names)
	processorGroups := make(map[string]bool)
	var cfg engine.MultiGroupConfig
	for i, n := range names {
		e := executions[n]
		if e == nil || e.stagedConfig == nil {
			return nil, fmt.Errorf("source-plan group is not staged")
		}
		c := *e.stagedConfig
		if i == 0 {
			cfg = c
			cfg.Groups = nil
		} else if c.Duration != cfg.Duration || c.PreBaseline != cfg.PreBaseline || c.PostBaseline != cfg.PostBaseline || c.StartPaused != cfg.StartPaused || c.TerminationMode != cfg.TerminationMode {
			return nil, fmt.Errorf("source-plan groups have inconsistent lifecycle settings")
		}
		for _, group := range c.Groups {
			plan, err := engine.NewRuntimeResourcePlan(group.Script, group.InitStrategy)
			if err != nil {
				return nil, err
			}
			processorGroups[group.Name] = plan.RequiresReservation()
		}
		cfg.Groups = append(cfg.Groups, c.Groups...)
	}
	m, err := engine.NewMultiGroup(cfg)
	if err != nil {
		return nil, err
	}
	return &sourcePlanExecution{engine: m, telemetryGroup: names[0], processorGroups: processorGroups, done: make(chan struct{})}, nil
}

func sourcePlanUsers(m *pb.SourcePlanManifest) int64 {
	var total int64
	if m != nil {
		for _, g := range m.Groups {
			total += int64(g.Vusers)
		}
	}
	return total
}

// Abort only this still-staged incarnation. The detach happens under the same
// lock as commit, so a delayed failed request cannot tear down a committed run.
func (s *Service) abortSourcePlanStage(testID string, generation uint64, token []byte) {
	s.mu.Lock()
	a, ok := s.assignments[testID]
	if !ok || a.sourcePlan == nil || a.committed || a.executionGeneration != generation || !sameRunSupervisionTokenHash(a.runSupervisionTokenHash, runSupervisionTokenHash(token)) {
		s.mu.Unlock()
		return
	}
	stops := s.detachFencedTestsLocked([]string{testID}, generation, false)
	s.mu.Unlock()
	for _, stop := range stops {
		stop.finalState = "FAILED"
		stop.finalError = "source-plan staging or commit failed; no replay was attempted"
		stop.escalationReason = testFailureEscalationReason
		<-s.startDetachedRunDrain(stop, false)
	}
}

func cloneSourcePlan(m *pb.SourcePlanManifest) *pb.SourcePlanManifest {
	return proto.Clone(m).(*pb.SourcePlanManifest)
}

// Process-wide telemetry is emitted once, while population and SLA evidence
// retain the group identity. Transactions come from each group's own buffer.
func (o *sourcePlanExecution) groupSnapshot(full engine.MetricsSnapshot, group string) engine.MetricsSnapshot {
	snapshot := engine.MetricsSnapshot{Timestamp: full.Timestamp, Window: full.Window, Resources: full.Resources}
	snapshot.BrowserDocuments = browserDocumentsForGroup(full.BrowserDocuments, group)
	if o.processorGroups[group] {
		snapshot.MessagingProgress = messagingProgressForGroup(full.MessagingProgress, group)
	}
	if g := full.ByGroup[group]; g != nil {
		snapshot.ActiveVUsers = g.ActiveVUsers
	}
	if group == o.telemetryGroup {
		snapshot.Telemetry = full.Telemetry
	}
	for _, v := range full.TransactionResponseTimeSLA {
		if v.Group == group {
			snapshot.TransactionResponseTimeSLA = append(snapshot.TransactionResponseTimeSLA, v)
		}
	}
	return snapshot
}

// Bind every staged script to the manifest, including the absence of a
// compatibility profile. A compatible source plan cannot mix ordinary scripts.
func validateSourcePlanScript(a *pb.WorkAssignment, script *dsl.Script) error {
	m, j := a.SourcePlan, script.Config.JMeter
	if m == nil {
		if j != nil {
			return fmt.Errorf("JMeter execution requires source-plan membership")
		}
		return nil
	}
	if m.ProjectId != script.ProjectID {
		return fmt.Errorf("source-plan project identity mismatch")
	}
	if j == nil {
		if m.CompatibilityProfile != "" {
			return fmt.Errorf("source-plan compatibility profile requires a compatible script")
		}
		return nil
	}
	if m.SourcePlanId != j.SourcePlanID || m.CompatibilityProfile != j.Profile {
		return fmt.Errorf("source-plan compatibility identity mismatch")
	}
	for _, g := range m.Groups {
		if g.GroupName == a.GroupName {
			if g.SourceGroupId != j.SourceGroupID {
				return fmt.Errorf("source group identity mismatch")
			}
			return nil
		}
	}
	return fmt.Errorf("source-plan group is absent")
}
