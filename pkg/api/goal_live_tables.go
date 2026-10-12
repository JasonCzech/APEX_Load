package api

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A goal run whose consumers idle on an empty live table delivers less than its
// target, and more VUsers cannot fix that when every iteration draws from the
// table: they only wait longer. The controller then holds its scale-up and says
// why beside the target.

// liveTableStarvation names a run live table its consumers are idling on.
type liveTableStarvation struct {
	alias string
	// automatic is set when the binding draws a row before every iteration
	// (next, pop or lease), so every group of the run waits on it.
	automatic bool
}

// starvedLiveTable finds a live table of the run, its own or a project table
// it is attached to, with consumers waiting on no rows, or one that answered
// empty since the previous check. previous holds each table's empty-outcome
// count at that check; without one, only waiting consumers count. It returns
// the counts to compare against next time.
func (tr *TestRunner) starvedLiveTable(testID string, previous map[string]uint64) (*liveTableStarvation, map[string]uint64) {
	run := tr.liveRun(testID)
	if run == nil {
		return nil, nil
	}
	type candidate struct {
		alias, strategy string
		table           *livetable.Table
	}
	var candidates []candidate
	run.mu.RLock()
	for _, table := range run.tables {
		if table != nil && table.table != nil {
			candidates = append(candidates, candidate{alias: table.binding.Alias, strategy: table.binding.SelectionStrategy, table: table.table})
		}
	}
	attachments := make([]storage.ProjectLiveTableAttachment, 0, len(run.projectAttachments))
	for _, attachment := range run.projectAttachments {
		attachments = append(attachments, attachment)
	}
	strategies := make(map[string]string)
	for _, bindings := range run.groupBindings {
		for _, binding := range bindings {
			strategies[binding.Alias] = binding.Strategy
		}
	}
	run.mu.RUnlock()
	seen := make(map[string]bool, len(attachments))
	for _, attachment := range attachments {
		if seen[attachment.Alias] {
			continue
		}
		if table := tr.readyProjectLiveTable(attachment.InstanceID); table != nil {
			seen[attachment.Alias] = true
			candidates = append(candidates, candidate{alias: attachment.Alias, strategy: strategies[attachment.Alias], table: table})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].alias < candidates[j].alias })
	empties := make(map[string]uint64, len(candidates))
	var starved *liveTableStarvation
	for _, candidate := range candidates {
		stats := candidate.table.Stats()
		empties[candidate.alias] = stats.Observations.EmptyOutcomes
		if starved != nil || stats.Closed || stats.Unavailable {
			continue
		}
		waiting := stats.Waiters > 0 && stats.Counts.Available == 0
		prior, known := previous[candidate.alias]
		answeredEmpty := previous != nil && known && stats.Observations.EmptyOutcomes > prior
		if waiting || answeredEmpty {
			switch strings.TrimSpace(candidate.strategy) {
			case "next", "pop", "lease":
				starved = &liveTableStarvation{alias: candidate.alias, automatic: true}
			default:
				starved = &liveTableStarvation{alias: candidate.alias}
			}
		}
	}
	return starved, empties
}

// readyProjectLiveTable is the loaded table of a project live-table instance,
// or nil while it loads, failed to load or is being evicted.
func (tr *TestRunner) readyProjectLiveTable(instanceID string) *livetable.Table {
	tr.liveTablesMu.Lock()
	defer tr.liveTablesMu.Unlock()
	runtime := tr.projectLiveTables[instanceID]
	if runtime == nil || runtime.evicting {
		return nil
	}
	select {
	case <-runtime.ready:
	default:
		return nil
	}
	if runtime.loadErr != nil {
		return nil
	}
	return runtime.table
}

// holdGoalScaleUpForLiveTables records whether the run's consumers are idling
// on an empty live table and reports whether the scale-up should be held.
func (tr *TestRunner) holdGoalScaleUpForLiveTables(test *ManagedTest) bool {
	if test == nil {
		return false
	}
	tr.mu.RLock()
	var previous map[string]uint64
	recorded := false
	if test.goalControl != nil {
		previous = maps.Clone(test.goalControl.liveTableEmpties)
		recorded = previous != nil || test.goalControl.liveTableNote != "" || test.goalControl.liveTableHolding
	}
	tr.mu.RUnlock()
	starved, empties := tr.starvedLiveTable(test.ID, previous)
	if empties == nil && !recorded {
		// A run without live tables has nothing to record.
		return false
	}
	note := ""
	if starved != nil {
		if starved.automatic {
			note = fmt.Sprintf("Holding VUsers: every iteration draws from the live table %s, which is empty, so more VUsers would only wait.", starved.alias)
		} else {
			note = fmt.Sprintf("Consumers are waiting on the empty live table %s. Adding VUsers raises throughput only where producers are short.", starved.alias)
		}
	}
	holding := starved != nil && starved.automatic
	tr.mu.Lock()
	if test.goalControl != nil {
		test.goalControl.liveTableEmpties = empties
		test.goalControl.liveTableNote = note
		test.goalControl.liveTableHolding = holding
	}
	tr.mu.Unlock()
	return holding
}
