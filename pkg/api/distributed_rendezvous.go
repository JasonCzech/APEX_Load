package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/distributed/rendezvous"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A coordinator reserves its full bounded snapshot allowance before recovery.
// Rendezvous state never competes for live-table waiter or receipt capacity.
const maxControllerRendezvousGroups = 32

type runGroupRendezvous struct {
	coordinator *rendezvous.Coordinator
	assignment  generatorrpc.RendezvousAssignment
	members     []rendezvous.MemberRange
	term        executionLeaseTerm
	closed      atomic.Bool
}

func rendezvousRuntimeKey(run, group string) string { return run + "\x00" + group }
func distributedRendezvousContracts(test *ManagedTest, group TestGroup) ([]rendezvous.Contract, error) {
	specs, err := dsl.ScriptDistributedRendezvous(group.script)
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, nil
	}
	if test.ScenarioMode == "goal_oriented" || test.GoalConfig != nil || group.stageDriven || len(group.Stages) > 0 || strings.EqualFold(group.script.Config.RunLogic.WorkloadModel, "open") || group.script.Config.JMeter != nil {
		return nil, errors.New("distributed rendezvous requires a fixed closed group without adaptive targets, stages or source plans")
	}
	if group.VUsers < 1 || group.VUsers > rendezvous.MaxPopulation {
		return nil, errors.New("distributed rendezvous population is outside supported bounds")
	}
	out := make([]rendezvous.Contract, 0, len(specs))
	for _, s := range specs {
		target := group.VUsers
		if s.Count > 0 {
			target = s.Count
		} else if s.Percent > 0 {
			target = (group.VUsers*s.Percent + 99) / 100
		}
		if target > group.VUsers {
			return nil, fmt.Errorf("rendezvous %q target exceeds its admitted group", s.Key)
		}
		out = append(out, rendezvous.Contract{Key: s.Key, Target: target, TimeoutMS: s.TimeoutMS})
	}
	return out, nil
}
func rendezvousMembers(shards []GeneratorShardSummary, population int) ([]rendezvous.MemberRange, error) {
	if len(shards) == 0 {
		return nil, rendezvous.ErrInvalid
	}
	out := make([]rendezvous.MemberRange, 0, len(shards))
	total := 0
	for _, shard := range shards {
		if shard.VUsers < 1 {
			return nil, rendezvous.ErrInvalid
		}
		out = append(out, rendezvous.MemberRange{First: shard.VUserIDOffset, Count: shard.VUsers})
		total += shard.VUsers
	}
	if total != population {
		return nil, errors.New("distributed rendezvous requires every admitted participant at launch")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].First < out[j].First })
	return out, nil
}
func (tr *TestRunner) prepareGroupRendezvous(ctx context.Context, test *ManagedTest, group TestGroup, shards []GeneratorShardSummary, restore bool, term executionLeaseTerm) error {
	contracts, err := distributedRendezvousContracts(test, group)
	if err != nil {
		return err
	}
	if len(contracts) == 0 {
		return nil
	}
	members, err := rendezvousMembers(shards, group.VUsers)
	if err != nil {
		return err
	}
	store, ok := tr.testStore.(storage.RendezvousStore)
	if !ok {
		return errors.New("distributed rendezvous persistence is unavailable")
	}
	if !tr.executionLeaseTermLocallyCurrent(term) {
		return rendezvous.ErrUnavailable
	}
	key := rendezvousRuntimeKey(test.ID, group.Name)
	tr.rendezvousMu.Lock()
	defer tr.rendezvousMu.Unlock()
	if tr.rendezvousGroups == nil {
		tr.rendezvousGroups = map[string]*runGroupRendezvous{}
	}
	old := tr.rendezvousGroups[key]
	if old != nil && old.term == term && !old.closed.Load() {
		if !reflect.DeepEqual(old.members, members) || !reflect.DeepEqual(old.assignment.Contracts, contracts) {
			return rendezvous.ErrInvalid
		}
		return nil
	}
	if old == nil && len(tr.rendezvousGroups) >= maxControllerRendezvousGroups {
		return rendezvous.ErrCapacity
	}
	rt := &runGroupRendezvous{assignment: generatorrpc.RendezvousAssignment{Population: group.VUsers, Contracts: contracts}, members: members, term: term}
	save := func(callCtx context.Context, previous uint64, next rendezvous.Snapshot) error {
		if rt.closed.Load() || !tr.executionLeaseTermLocallyCurrent(term) {
			return rendezvous.ErrUnavailable
		}
		body, e := json.Marshal(next)
		if e != nil {
			return e
		}
		callCtx, cancel := context.WithTimeout(callCtx, 4*time.Second)
		defer cancel()
		return tr.trackDurableWrite(func() error {
			return store.SaveRendezvous(callCtx, storage.RendezvousRecord{RunID: test.ID, Group: group.Name, ProjectID: test.ProjectID, Revision: next.Revision, Snapshot: body}, previous, liveStorageTerm(term))
		})
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if restore {
		var r *storage.RendezvousRecord
		e := tr.trackDurableWrite(func() error {
			var claimErr error
			r, claimErr = store.ClaimRendezvous(ctx, test.ID, group.Name, test.ProjectID, liveStorageTerm(term))
			return claimErr
		})
		if e != nil {
			return e
		}
		var snapshot rendezvous.Snapshot
		if e = json.Unmarshal(r.Snapshot, &snapshot); e != nil {
			return e
		}
		if snapshot.Revision != r.Revision || !reflect.DeepEqual(snapshot.Members, members) || !reflect.DeepEqual(snapshot.Contracts, contracts) {
			return rendezvous.ErrInvalid
		}
		rt.coordinator, err = rendezvous.Restore(snapshot, save)
	} else {
		rt.coordinator, err = rendezvous.New(members, contracts, save)
		if err == nil {
			body, e := json.Marshal(rt.coordinator.Snapshot())
			if e != nil {
				return e
			}
			err = tr.trackDurableWrite(func() error {
				return store.InitializeRendezvous(ctx, storage.RendezvousRecord{RunID: test.ID, Group: group.Name, ProjectID: test.ProjectID, Snapshot: body}, liveStorageTerm(term))
			})
		}
	}
	if err != nil {
		return err
	}
	if old != nil {
		old.closed.Store(true)
	}
	tr.rendezvousGroups[key] = rt
	return nil
}
func (tr *TestRunner) prepareLocalRendezvous(ctx context.Context, test *ManagedTest, term executionLeaseTerm) error {
	tr.mu.RLock()
	groups := append([]TestGroup(nil), test.Groups...)
	tr.mu.RUnlock()
	for _, group := range groups {
		if err := tr.prepareGroupRendezvous(ctx, test, group, []GeneratorShardSummary{{Group: group.Name, VUsers: group.VUsers}}, false, term); err != nil {
			return err
		}
	}
	return nil
}
func (tr *TestRunner) restoreRendezvous(ctx context.Context, test *ManagedTest, shards map[string][]GeneratorShardSummary, term executionLeaseTerm) error {
	for _, group := range test.Groups {
		if err := tr.prepareGroupRendezvous(ctx, test, group, shards[group.Name], true, term); err != nil {
			tr.dropRunRendezvous(test.ID, term)
			return err
		}
	}
	return nil
}
func (tr *TestRunner) rendezvousRuntime(run, group string) *runGroupRendezvous {
	tr.rendezvousMu.Lock()
	defer tr.rendezvousMu.Unlock()
	return tr.rendezvousGroups[rendezvousRuntimeKey(run, group)]
}
func (tr *TestRunner) rendezvousAssignment(run, group string) []byte {
	rt := tr.rendezvousRuntime(run, group)
	if rt == nil {
		return nil
	}
	body, _ := json.Marshal(rt.assignment)
	return body
}
func (tr *TestRunner) dropRunRendezvous(run string, term executionLeaseTerm) {
	tr.rendezvousMu.Lock()
	defer tr.rendezvousMu.Unlock()
	for key, rt := range tr.rendezvousGroups {
		if strings.HasPrefix(key, run+"\x00") && rt.term == term {
			rt.closed.Store(true)
			delete(tr.rendezvousGroups, key)
		}
	}
}

// retainRunRendezvousUntil transfers the prepared coordinators from a launcher
// to the remote run's lifetime. That context outlives a graceful Stop and ends
// when remote resources close, leadership is lost, or the controller shuts down.
// Capture the exact runtimes: a late callback from an earlier lifetime must not
// close replacement coordinators, even when they use the same lease term.
func (tr *TestRunner) retainRunRendezvousUntil(ctx context.Context, run string, term executionLeaseTerm) {
	tr.rendezvousMu.Lock()
	owned := make(map[string]*runGroupRendezvous)
	for key, rt := range tr.rendezvousGroups {
		if strings.HasPrefix(key, run+"\x00") && rt.term == term {
			owned[key] = rt
		}
	}
	tr.rendezvousMu.Unlock()
	if len(owned) == 0 {
		return
	}
	context.AfterFunc(ctx, func() {
		tr.rendezvousMu.Lock()
		defer tr.rendezvousMu.Unlock()
		for key, rt := range owned {
			rt.closed.Store(true)
			if tr.rendezvousGroups[key] == rt {
				delete(tr.rendezvousGroups, key)
			}
		}
	})
}

func (tr *TestRunner) rendezvousClient(run, group string, allowed []rendezvous.MemberRange) rendezvous.Client {
	rt := tr.rendezvousRuntime(run, group)
	if rt == nil {
		return nil
	}
	return rendezvous.ClientFunc(func(ctx context.Context, batch rendezvous.Batch) (rendezvous.Outcome, error) {
		if rt.closed.Load() || !tr.executionLeaseTermLocallyCurrent(rt.term) {
			return rendezvous.Outcome{}, rendezvous.ErrUnavailable
		}
		for _, id := range batch.Arrivals {
			valid := false
			for _, r := range allowed {
				if id >= r.First && id-r.First < uint64(r.Count) {
					valid = true
					break
				}
			}
			if !valid {
				return rendezvous.Outcome{}, rendezvous.ErrInvalid
			}
		}
		out, err := rt.coordinator.Exchange(ctx, batch)
		if rt.closed.Load() || !tr.executionLeaseTermLocallyCurrent(rt.term) {
			return rendezvous.Outcome{}, rendezvous.ErrUnavailable
		}
		return out, err
	})
}
func (tr *TestRunner) localRendezvousClient(run, group string) rendezvous.Client {
	rt := tr.rendezvousRuntime(run, group)
	if rt == nil {
		return nil
	}
	return tr.rendezvousClient(run, group, rt.members)
}

type rendezvousChannelClient interface {
	OpenRendezvous(context.Context, string, string, uint64, []byte, rendezvous.Client) error
}

func (tr *TestRunner) openRendezvousChannels(ctx context.Context, run string, generation uint64, token []byte, shards map[string][]GeneratorShardSummary, clients map[string]generatorClient) error {
	for group, participants := range shards {
		if tr.rendezvousRuntime(run, group) == nil {
			continue
		}
		byTarget := map[string][]rendezvous.MemberRange{}
		for _, shard := range participants {
			byTarget[shard.Target] = append(byTarget[shard.Target], rendezvous.MemberRange{First: shard.VUserIDOffset, Count: shard.VUsers})
		}
		for target, members := range byTarget {
			base := clients[target]
			if base == nil {
				continue
			}
			client, ok := base.(rendezvousChannelClient)
			if !ok {
				return fmt.Errorf("generator %q cannot open distributed rendezvous", target)
			}
			if err := client.OpenRendezvous(ctx, run, group, generation, token, tr.rendezvousClient(run, group, members)); err != nil {
				return fmt.Errorf("open rendezvous for group %q generator %q: %w", group, target, err)
			}
		}
	}
	return nil
}
