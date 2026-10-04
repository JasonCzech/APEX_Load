package engine

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxReplayRuntimeSnapshotEntries          = 256
	MaxReplayRuntimeSnapshotFrames           = 64
	MaxReplayRuntimeSnapshotRows             = 64
	MaxReplayRuntimeSnapshotJSONBytes        = 64 << 10
	MaxReplayRuntimeSnapshotValueBytes       = 4 << 10
	MaxReplayRuntimeSnapshotPathDepth        = 64
	MaxReplayRuntimeSnapshotPathSegmentBytes = 128
)

// ReplayRuntimeSnapshot is a detached full replacement, never a diff. Empty
// arrays remove prior bindings, row selections and frames from an inspector.
// Observed values describe successful resolution, not network acceptance.
type ReplayRuntimeSnapshot struct {
	State                      string                   `json:"state"`
	Boundary                   string                   `json:"boundary"`
	Location                   ReplayDebugAction        `json:"location"`
	Variables                  []ReplayRuntimeBinding   `json:"variables"`
	Parameters                 []ReplayRuntimeParameter `json:"parameters"`
	Rows                       []ReplayRuntimeRow       `json:"rows"`
	Frames                     []ReplayRuntimeFrame     `json:"frames"`
	Omissions                  ReplayRuntimeOmissions   `json:"omissions"`
	RuntimeEvidenceUnavailable bool                     `json:"runtime_evidence_unavailable,omitempty"`
	UnavailableReason          string                   `json:"unavailable_reason,omitempty"`
}

type ReplayRuntimeBinding struct {
	Name       string  `json:"name"`
	ValueState string  `json:"value_state"`
	Value      *string `json:"value,omitempty"`
}

type ReplayRuntimeParameter struct {
	Name            string  `json:"name"`
	State           string  `json:"state"`
	UpdatePolicy    string  `json:"update_policy"`
	ValueState      string  `json:"value_state"`
	Value           *string `json:"value,omitempty"`
	ObservedEventID string  `json:"observed_event_id,omitempty"`
}

type ReplayRuntimeRow struct {
	Kind            string `json:"kind"`
	Source          string `json:"source"`
	Name            string `json:"name"`
	RowOrdinal      *int   `json:"row_ordinal,omitempty"`
	Selection       string `json:"selection"`
	State           string `json:"state"`
	ObservedEventID string `json:"observed_event_id,omitempty"`
}

type ReplayRuntimeFrame struct {
	Kind          string   `json:"kind"`
	Phase         string   `json:"phase"`
	ActionIndex   int      `json:"action_index"`
	StepPath      []string `json:"step_path,omitempty"`
	IterationPath []int    `json:"iteration_path,omitempty"`
	BranchIndex   *int     `json:"branch_index,omitempty"`
}

type ReplayRuntimeOmissions struct {
	Variables  int `json:"variables"`
	Parameters int `json:"parameters"`
	Rows       int `json:"rows"`
	Frames     int `json:"frames"`
	Values     int `json:"values"`
}

// Extended capabilities replace their legacy hook, exactly once at the same
// boundary. They introduce no additional wait or after-final-action pause.
type ReplayDebugControllerWithRuntimeState interface {
	BeforeActionWithRuntimeState(context.Context, ReplayDebugAction, ReplayRuntimeSnapshot) error
}
type ReplayDebugControllerAfterWithRuntimeState interface {
	AfterActionWithRuntimeState(context.Context, ReplayDebugAction, *ReplayActionEvent, ReplayRuntimeSnapshot) error
}

func emptyReplayRuntimeSnapshot(boundary, reason string, unavailable bool) ReplayRuntimeSnapshot {
	if boundary != "after_action" {
		boundary = "before_action"
	}
	return ReplayRuntimeSnapshot{State: "unavailable", Boundary: boundary, Location: ReplayDebugAction{Phase: "action"}, Variables: []ReplayRuntimeBinding{}, Parameters: []ReplayRuntimeParameter{}, Rows: []ReplayRuntimeRow{}, Frames: []ReplayRuntimeFrame{}, RuntimeEvidenceUnavailable: unavailable, UnavailableReason: reason}
}

func runtimeEnum(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
func runtimeTextBounded(value string) bool {
	return len(value) <= MaxReplayRuntimeSnapshotValueBytes && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
func runtimeValueBounded(state string, value *string, observed bool) bool {
	if state == "omitted" {
		return value == nil
	}
	if !runtimeEnum(state, "assigned", "redacted") && !(observed && state == "observed") {
		return false
	}
	return value != nil && runtimeTextBounded(*value)
}
func runtimePathsBounded(path []string, iterations []int) bool {
	if len(path) > MaxReplayRuntimeSnapshotPathDepth || len(iterations) > MaxReplayRuntimeSnapshotPathDepth {
		return false
	}
	for _, segment := range path {
		if len(segment) > MaxReplayRuntimeSnapshotPathSegmentBytes || !runtimeStructuralPath(segment) {
			return false
		}
	}
	for _, iteration := range iterations {
		if iteration <= 0 {
			return false
		}
	}
	return true
}
func runtimeLocationBounded(a ReplayDebugAction) bool {
	if !runtimeEnum(a.Phase, "init", "action", "teardown") || a.ActionIndex < 0 || a.EventIndex < 0 || a.RequestCount < 0 || a.LeafOrdinal < 0 {
		return false
	}
	if a.RequestIndex != nil && (*a.RequestIndex < 0 || a.RequestCount <= *a.RequestIndex) {
		return false
	}
	if a.Iteration != nil && *a.Iteration <= 0 {
		return false
	}
	if !runtimePathsBounded(a.StepPath, a.IterationPath) {
		return false
	}
	for _, value := range []string{a.EventID, a.ActionName, a.ActionType, a.Method, a.Target, a.Selector, a.SourceStepID, a.ParentActionName} {
		if !runtimeTextBounded(value) {
			return false
		}
	}
	return true
}

// ValidateReplayRuntimeSnapshotBounds admits only the typed closed contract.
// Its exact JSON size counter visits already-bounded fields without constructing
// an unbounded serialized buffer, including HTML and Unicode JSON escapes.
func ValidateReplayRuntimeSnapshotBounds(s ReplayRuntimeSnapshot) bool {
	if !runtimeEnum(s.State, "complete", "bounded", "unavailable") || !runtimeEnum(s.Boundary, "before_action", "after_action") {
		return false
	}
	if s.Variables == nil || s.Parameters == nil || s.Rows == nil || s.Frames == nil {
		return false
	}
	if len(s.Variables)+len(s.Parameters)+len(s.Rows)+len(s.Frames) > MaxReplayRuntimeSnapshotEntries || len(s.Rows) > MaxReplayRuntimeSnapshotRows || len(s.Frames) > MaxReplayRuntimeSnapshotFrames {
		return false
	}
	o := s.Omissions
	if o.Variables < 0 || o.Parameters < 0 || o.Rows < 0 || o.Frames < 0 || o.Values < 0 {
		return false
	}
	if s.State == "unavailable" {
		fixed := emptyReplayRuntimeSnapshot(s.Boundary, s.UnavailableReason, s.RuntimeEvidenceUnavailable)
		if !runtimeEnum(s.UnavailableReason, "publication_unavailable", "work_limit") || s.RuntimeEvidenceUnavailable != (s.UnavailableReason == "publication_unavailable") || !reflect.DeepEqual(s, fixed) {
			return false
		}
	} else {
		if s.RuntimeEvidenceUnavailable || s.UnavailableReason != "" || !runtimeLocationBounded(s.Location) {
			return false
		}
		if s.State == "complete" && o != (ReplayRuntimeOmissions{}) {
			return false
		}
	}
	for _, v := range s.Variables {
		if !runtimeTextBounded(v.Name) || !runtimeValueBounded(v.ValueState, v.Value, false) {
			return false
		}
	}
	for _, p := range s.Parameters {
		if !runtimeTextBounded(p.Name) || !runtimeTextBounded(p.ObservedEventID) || !runtimeEnum(p.State, "not_evaluated", "prepared", "observed", "exhausted", "unavailable") || !runtimeEnum(p.UpdatePolicy, "once", "each_iteration", "each_occurrence", "unavailable") || !runtimeValueBounded(p.ValueState, p.Value, true) {
			return false
		}
		if runtimeEnum(p.State, "not_evaluated", "exhausted", "unavailable") && (p.Value != nil || p.ValueState != "omitted" || p.ObservedEventID != "") {
			return false
		}
		if p.State == "prepared" && (p.ObservedEventID != "" || p.ValueState == "observed") {
			return false
		}
		if p.State == "observed" && p.ValueState == "assigned" {
			return false
		}
	}
	for _, r := range s.Rows {
		if !runtimeEnum(r.Kind, "datasource", "parameter", "foreach") || !runtimeEnum(r.Selection, "sequential", "random", "unique", "pop", "same_line", "foreach", "fixed", "unavailable") || !runtimeEnum(r.State, "selected", "exhausted", "unavailable") || !runtimeTextBounded(r.Name) || !runtimeTextBounded(r.Source) || !runtimeTextBounded(r.ObservedEventID) {
			return false
		}
		if r.State == "selected" {
			if r.RowOrdinal == nil || *r.RowOrdinal <= 0 {
				return false
			}
		} else if r.RowOrdinal != nil {
			return false
		}
	}
	for _, f := range s.Frames {
		if !runtimeEnum(f.Kind, "phase", "http_sequence", "control_if", "control_switch", "control_loop", "foreach", "parallel", "parallel_branch") || !runtimeEnum(f.Phase, "init", "action", "teardown") || (f.Kind == "phase" && f.ActionIndex != -1) || (f.Kind != "phase" && f.ActionIndex < 0) || !runtimePathsBounded(f.StepPath, f.IterationPath) {
			return false
		}
		if f.BranchIndex != nil && (f.Kind != "parallel_branch" || *f.BranchIndex < 0) {
			return false
		}
		if f.Kind == "parallel_branch" && f.BranchIndex == nil {
			return false
		}
	}
	remaining := MaxReplayRuntimeSnapshotJSONBytes
	return countReplayRuntimeJSON(reflect.ValueOf(s), &remaining)
}

// CloneReplayRuntimeSnapshot does not retain any caller-owned pointers. Invalid
// input yields a fixed empty DTO, preserving only the privacy failure signal.
func CloneReplayRuntimeSnapshot(s ReplayRuntimeSnapshot) ReplayRuntimeSnapshot {
	if !ValidateReplayRuntimeSnapshotBounds(s) {
		reason := "work_limit"
		if s.RuntimeEvidenceUnavailable {
			reason = "publication_unavailable"
		}
		return emptyReplayRuntimeSnapshot(s.Boundary, reason, s.RuntimeEvidenceUnavailable)
	}
	s.State, s.Boundary, s.UnavailableReason = strings.Clone(s.State), strings.Clone(s.Boundary), strings.Clone(s.UnavailableReason)
	s.Location = cloneRuntimeLocation(s.Location)
	s.Variables = append([]ReplayRuntimeBinding{}, s.Variables...)
	for i := range s.Variables {
		s.Variables[i].Name = strings.Clone(s.Variables[i].Name)
		s.Variables[i].ValueState = strings.Clone(s.Variables[i].ValueState)
		s.Variables[i].Value = cloneRuntimeValue(s.Variables[i].Value)
	}
	s.Parameters = append([]ReplayRuntimeParameter{}, s.Parameters...)
	for i := range s.Parameters {
		p := &s.Parameters[i]
		p.Name = strings.Clone(p.Name)
		p.State = strings.Clone(p.State)
		p.UpdatePolicy = strings.Clone(p.UpdatePolicy)
		p.ValueState = strings.Clone(p.ValueState)
		p.ObservedEventID = strings.Clone(p.ObservedEventID)
		p.Value = cloneRuntimeValue(p.Value)
	}
	s.Rows = append([]ReplayRuntimeRow{}, s.Rows...)
	for i := range s.Rows {
		r := &s.Rows[i]
		r.Kind = strings.Clone(r.Kind)
		r.Source = strings.Clone(r.Source)
		r.Name = strings.Clone(r.Name)
		r.Selection = strings.Clone(r.Selection)
		r.State = strings.Clone(r.State)
		r.ObservedEventID = strings.Clone(r.ObservedEventID)
		r.RowOrdinal = cloneRuntimeInt(r.RowOrdinal)
	}
	s.Frames = append([]ReplayRuntimeFrame{}, s.Frames...)
	for i := range s.Frames {
		f := &s.Frames[i]
		f.Kind = strings.Clone(f.Kind)
		f.Phase = strings.Clone(f.Phase)
		f.StepPath = cloneRuntimePath(f.StepPath)
		f.IterationPath = append([]int(nil), f.IterationPath...)
		f.BranchIndex = cloneRuntimeInt(f.BranchIndex)
	}
	return s
}
func cloneRuntimePath(path []string) []string {
	if path == nil {
		return nil
	}
	out := make([]string, len(path))
	for i, v := range path {
		out[i] = strings.Clone(v)
	}
	return out
}
func cloneRuntimeLocation(a ReplayDebugAction) ReplayDebugAction {
	a = *cloneReplayDebugAction(a)
	a.EventID = strings.Clone(a.EventID)
	a.Phase = strings.Clone(a.Phase)
	a.ActionName = strings.Clone(a.ActionName)
	a.ActionType = strings.Clone(a.ActionType)
	a.Method = strings.Clone(a.Method)
	a.Target = strings.Clone(a.Target)
	a.Selector = strings.Clone(a.Selector)
	a.SourceStepID = strings.Clone(a.SourceStepID)
	a.ParentActionName = strings.Clone(a.ParentActionName)
	a.StepPath = cloneRuntimePath(a.StepPath)
	a.IterationPath = append([]int(nil), a.IterationPath...)
	return a
}
func cloneRuntimeValue(v *string) *string {
	if v == nil {
		return nil
	}
	copy := strings.Clone(*v)
	return &copy
}
func cloneRuntimeInt(v *int) *int {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}

func countReplayRuntimeJSON(v reflect.Value, remaining *int) bool {
	take := func(n int) bool {
		if n > *remaining {
			return false
		}
		*remaining -= n
		return true
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return take(4)
		}
		return countReplayRuntimeJSON(v.Elem(), remaining)
	case reflect.String:
		if !take(2) {
			return false
		}
		for _, r := range v.String() {
			n := utf8.RuneLen(r)
			if r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t' || r == '\b' || r == '\f' {
				n = 2
			} else if r < 0x20 || r == '<' || r == '>' || r == '&' || r == 0x2028 || r == 0x2029 {
				n = 6
			}
			if !take(n) {
				return false
			}
		}
		return true
	case reflect.Int:
		return take(len(strconv.FormatInt(v.Int(), 10)))
	case reflect.Bool:
		if v.Bool() {
			return take(4)
		}
		return take(5)
	case reflect.Slice:
		if v.IsNil() {
			return take(4)
		}
		if !take(2) {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if i > 0 && !take(1) {
				return false
			}
			if !countReplayRuntimeJSON(v.Index(i), remaining) {
				return false
			}
		}
		return true
	case reflect.Struct:
		if !take(2) {
			return false
		}
		fields := 0
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			field := v.Field(i)
			if len(tag) > 1 && tag[1] == "omitempty" && runtimeJSONEmpty(field) {
				continue
			}
			if fields > 0 && !take(1) {
				return false
			}
			fields++
			if !take(len(tag[0])+3) || !countReplayRuntimeJSON(field, remaining) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
func runtimeJSONEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Pointer:
		return v.IsNil()
	case reflect.Int:
		return v.Int() == 0
	case reflect.Bool:
		return !v.Bool()
	}
	return false
}
