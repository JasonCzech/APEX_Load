package engine

import (
	"context"
	"github.com/jasonczech/apexload/pkg/dsl"
	"regexp"
	"strings"
)

const (
	maxReplayRuntimeInputEntries   = 4096
	maxReplayRuntimeInputBytes     = 1 << 20
	maxReplayRuntimeProjectionWork = 16 << 20
)

// Only the replay execution goroutine owns these receipts. Snapshot projection
// reads them; it never calls a generator or consults a global allocation cursor.
type replayRuntimeState struct {
	rows            map[string]ReplayRuntimeRow
	frames          []ReplayRuntimeFrame
	eventID         string
	workUnavailable bool
}
type parameterRuntimeObservation struct {
	state   string
	value   string
	eventID string
	row     *ReplayRuntimeRow
}
type dataRowReceipt struct {
	ordinal   int
	selection string
	state     string
}

var replayRuntimeStructuralPath = regexp.MustCompile(`^(?:(?:requests|then|else|default|loop)\[[0-9]+\]|case\[[0-9]+\]\[[0-9]+\]|branches\[[0-9]+\]\.steps\[[0-9]+\])$`)

func runtimeStructuralPath(value string) bool { return replayRuntimeStructuralPath.MatchString(value) }
func runtimeRowSelection(value string) string {
	if runtimeEnum(value, "sequential", "random", "unique", "pop", "same_line", "foreach", "fixed") {
		return value
	}
	return "unavailable"
}
func replayRuntimeEnabled(controller ReplayDebugController) bool {
	_, before := controller.(ReplayDebugControllerWithRuntimeState)
	_, after := controller.(ReplayDebugControllerAfterWithRuntimeState)
	return before || after
}
func (vu *VUser) recordReplayRuntimeRow(kind, source, name string, receipt dataRowReceipt) {
	if vu.replayRuntime == nil {
		return
	}
	key := kind + "\x00" + name
	if len(vu.replayRuntime.rows) >= maxReplayRuntimeInputEntries {
		if _, ok := vu.replayRuntime.rows[key]; !ok {
			vu.replayRuntime.workUnavailable = true
			return
		}
	}
	row := ReplayRuntimeRow{Kind: kind, Source: source, Name: name, State: receipt.state, Selection: receipt.selection, ObservedEventID: vu.replayRuntime.eventID}
	if receipt.state == "selected" {
		row.RowOrdinal = cloneRuntimeInt(&receipt.ordinal)
	}
	vu.replayRuntime.rows[key] = row
}
func (vu *VUser) pushReplayRuntimeFrame(frame ReplayRuntimeFrame) func() {
	if vu.replayRuntime == nil {
		return func() {}
	}
	length := len(vu.replayRuntime.frames)
	if length >= maxReplayRuntimeInputEntries {
		vu.replayRuntime.workUnavailable = true
		return func() {}
	}
	frame.StepPath = append([]string(nil), frame.StepPath...)
	frame.IterationPath = append([]int(nil), frame.IterationPath...)
	vu.replayRuntime.frames = append(vu.replayRuntime.frames, frame)
	return func() { vu.replayRuntime.frames = vu.replayRuntime.frames[:length] }
}
func (vu *VUser) enterReplayRuntimeIteration(path []int) func() {
	if vu.replayRuntime == nil || len(vu.replayRuntime.frames) == 0 {
		return func() {}
	}
	index := len(vu.replayRuntime.frames) - 1
	previous := vu.replayRuntime.frames[index].IterationPath
	vu.replayRuntime.frames[index].IterationPath = append([]int(nil), path...)
	return func() { vu.replayRuntime.frames[index].IterationPath = previous }
}
func (vu *VUser) enterReplayRuntimeEvent(eventID string) func() {
	if vu.replayRuntime == nil {
		return func() {}
	}
	previous := vu.replayRuntime.eventID
	vu.replayRuntime.eventID = eventID
	return func() { vu.replayRuntime.eventID = previous }
}
func cloneReplayRuntimeRows(rows map[string]ReplayRuntimeRow) map[string]ReplayRuntimeRow {
	if rows == nil {
		return nil
	}
	out := make(map[string]ReplayRuntimeRow, len(rows))
	for k, v := range rows {
		out[k] = v
	}
	return out
}
func (pr *ParameterRuntime) recordRuntimeObservation(name, state, value string, row bool) {
	if pr.runtimeObservation == nil {
		return
	}
	if len(name)+len(value) > maxReplayRuntimeInputBytes {
		pr.runtimeObservation.workUnavailable = true
		return
	}
	observation := parameterRuntimeObservation{state: state, value: strings.Clone(value)}
	if state == "observed" {
		observation.eventID = pr.runtimeObservation.eventID
	}
	p := pr.params[name]
	s := pr.states[name]
	if row && runtimeEnum(dsl.CanonicalParameterType(p.Type), "file", "list") {
		selected := s != nil && s.hasRowPick
		r := ReplayRuntimeRow{Kind: "parameter", Source: p.DataSource, Name: name, Selection: runtimeRowSelection(effectiveParameterSelection(p)), State: "exhausted", ObservedEventID: observation.eventID}
		if hasSameLineReference(p) {
			r.Selection = "same_line"
		}
		if selected {
			ordinal := s.rowOffset + s.lastIndex*s.rowStride + 1
			r.RowOrdinal = &ordinal
			r.State = "selected"
		}
		observation.row = &r
	}
	if len(pr.runtimeObservations) >= maxReplayRuntimeInputEntries {
		if _, ok := pr.runtimeObservations[name]; !ok {
			pr.runtimeObservation.workUnavailable = true
			return
		}
	}
	prior := pr.runtimeObservations[name]
	nextBytes := pr.runtimeObservationBytes - len(prior.value) + len(value)
	if nextBytes > maxReplayRuntimeInputBytes {
		pr.runtimeObservation.workUnavailable = true
		return
	}
	if pr.runtimeObservations == nil {
		pr.runtimeObservations = make(map[string]parameterRuntimeObservation)
	}
	pr.runtimeObservationBytes = nextBytes
	pr.runtimeObservations[name] = observation
}
func (vu *VUser) replayBeforeBoundary(ctx context.Context, controller ReplayDebugController, action, location ReplayDebugAction) error {
	if extended, ok := controller.(ReplayDebugControllerWithRuntimeState); ok {
		snapshot := vu.captureReplayRuntimeSnapshot("before_action", location)
		if snapshot.State != "unavailable" {
			action = snapshot.Location
		}
		return extended.BeforeActionWithRuntimeState(ctx, action, snapshot)
	}
	if controller != nil {
		return controller.BeforeAction(ctx, action)
	}
	return nil
}
func (vu *VUser) replayAfterBoundary(ctx context.Context, controller ReplayDebugController, action, location ReplayDebugAction, event *ReplayActionEvent) error {
	if extended, ok := controller.(ReplayDebugControllerAfterWithRuntimeState); ok {
		snapshot := vu.captureReplayRuntimeSnapshot("after_action", location)
		if snapshot.State != "unavailable" {
			action = snapshot.Location
		}
		return extended.AfterActionWithRuntimeState(ctx, action, event, snapshot)
	}
	if legacy, ok := controller.(ReplayDebugControllerAfter); ok {
		return legacy.AfterAction(ctx, action, event)
	}
	return nil
}

// Same-allocation receipt: every strategy returns its selected physical index
// alongside the map it built, including a pop selected under the same lock.
func (dp *DataProvider) getRowWithReceipt(sourceName string, vuserID int) (map[string]string, dataRowReceipt) {
	return dp.getRowAllocated(sourceName, vuserID)
}
func (view dataRowView) rowWithReceipt(index int) (map[string]string, dataRowReceipt) {
	row := view.Row(index)
	receipt := dataRowReceipt{selection: "foreach", state: "exhausted"}
	if view.source == nil {
		receipt.state = "unavailable"
	}
	if row != nil {
		count := view.shard.Count
		receipt.ordinal = int(uint64(view.shard.Ordinal)+uint64(index)*uint64(count)) + 1
		receipt.state = "selected"
	}
	return row, receipt
}
func runtimeObservationState(paramType string) string {
	if runtimeEnum(dsl.CanonicalParameterType(paramType), "file", "list", "counter", "current_iteration") {
		return "exhausted"
	}
	return "unavailable"
}
