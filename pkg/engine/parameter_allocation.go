package engine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"strings"
)

// ParameterAllocation uses dense logical ordinals, never transport VUser IDs.
// Population and blocks are frozen for a run, including its planned ramp.
type ParameterAllocation struct {
	Population int `json:"population"`
	Offset     int `json:"offset"`
	Iterations int `json:"iterations,omitempty"`
}
type ParameterAllocationPreview struct {
	Name       string `json:"name"`
	Rows       int    `json:"rows"`
	BlockSize  int    `json:"block_size"`
	Population int    `json:"population"`
}

func HasAllocatedParameters(s *dsl.Script) bool {
	if s == nil {
		return false
	}
	for _, p := range s.Parameters {
		if p.Selection == "unique" && (p.AutoAllocate || p.BlockSize > 0) {
			return true
		}
	}
	return false
}
func allocationValues(p dsl.Parameter, d *DataProvider) ([]string, bool) {
	if len(p.Values) > 0 {
		return p.Values, true
	}
	if d == nil {
		return nil, false
	}
	ds := d.sources[p.DataSource]
	if ds == nil {
		return nil, false
	}
	v, ok := ds.columnValues[strings.TrimSpace(p.Column)]
	return v, ok
}
func CompileParameterAllocation(params []dsl.Parameter, d *DataProvider, a ParameterAllocation) ([]dsl.Parameter, []ParameterAllocationPreview, error) {
	out := append([]dsl.Parameter(nil), params...)
	var preview []ParameterAllocationPreview
	for i, p := range out {
		if dsl.CanonicalParameterType(p.Type) == "counter" && p.Selection == "unique" {
			if a.Population <= 0 || a.Offset < 0 || a.Offset >= a.Population {
				return nil, nil, fmt.Errorf("parameter %q requires a valid logical VUser allocation", p.Name)
			}
			if _, err := uniqueCounterBlockStart(p, a.Population-1); err != nil {
				return nil, nil, err
			}
			if effectiveOnExhaustion(p) == "error" && a.Iterations > p.BlockSize && effectiveParameterUpdate(p) == "each_iteration" {
				return nil, nil, fmt.Errorf("parameter %q has a number block smaller than the planned iteration requirement", p.Name)
			}
			preview = append(preview, ParameterAllocationPreview{Name: p.Name, BlockSize: p.BlockSize, Population: a.Population})
			continue
		}
		if p.Selection != "unique" || (!p.AutoAllocate && p.BlockSize == 0) {
			continue
		}
		if a.Population <= 0 || a.Offset < 0 || a.Offset >= a.Population {
			return nil, nil, fmt.Errorf("parameter %q requires a valid logical VUser allocation", p.Name)
		}
		values, ok := allocationValues(p, d)
		if !ok {
			return nil, nil, fmt.Errorf("parameter %q data is unavailable", p.Name)
		}
		rows := len(values) - max(0, p.StartRow-1)
		if rows < 0 {
			rows = 0
		}
		size := p.BlockSize
		if p.AutoAllocate && size == 0 {
			switch {
			case p.Update == "once":
				size = 1
			case p.Update == "each_occurrence":
				return nil, nil, fmt.Errorf("parameter %q requires an explicit block size for each-occurrence allocation", p.Name)
			case a.Iterations > 0:
				size = a.Iterations
			default:
				size = rows / a.Population
			}
		}
		if size <= 0 || (a.Population-1) >= (rows+size-1)/size {
			return nil, nil, fmt.Errorf("parameter %q has insufficient unique rows for %d VUsers", p.Name, a.Population)
		}
		if effectiveOnExhaustion(p) == "error" && a.Iterations > size && effectiveParameterUpdate(p) == "each_iteration" {
			return nil, nil, fmt.Errorf("parameter %q has a block smaller than the planned iteration requirement", p.Name)
		}
		if effectiveOnExhaustion(p) == "error" && a.Iterations > 0 && effectiveParameterUpdate(p) != "once" && rows-(a.Population-1)*size < min(size, a.Iterations) {
			return nil, nil, fmt.Errorf("parameter %q would exhaust its final VUser block before the planned iterations complete", p.Name)
		}
		out[i].BlockSize = size
		preview = append(preview, ParameterAllocationPreview{Name: p.Name, Rows: rows, BlockSize: size, Population: a.Population})
	}
	return out, preview, nil
}
func newAllocatedParameterRuntime(params []dsl.Parameter, d *DataProvider, id int, a ParameterAllocation) (*ParameterRuntime, error) {
	compiled, _, err := CompileParameterAllocation(params, d, a)
	if err != nil {
		return nil, err
	}
	pr := NewParameterRuntime(compiled, d, id)
	if pr != nil {
		pr.allocation = &a
	}
	return pr, nil
}

func (pr *ParameterRuntime) inheritAllocatedParameterState(parent *ParameterRuntime) {
	if pr == nil || parent == nil || parent.allocation == nil {
		return
	}
	allocation := *parent.allocation
	pr.allocation = &allocation
	for name, param := range parent.params {
		if param.Selection != "unique" || (param.BlockSize == 0 && !param.AutoAllocate) {
			continue
		}
		pr.params[name] = param
		if state := parent.states[name]; state != nil {
			copy := *state
			copy.values = append([]string(nil), state.values...)
			pr.states[name] = &copy
		}
	}
}

// Isolated parallel branches do not share a mutable each-occurrence cursor.
// Reject this combination rather than allocating the same row twice.
func ValidateParameterAllocationTopology(s *dsl.Script) error {
	if !HasAllocatedParameters(s) {
		return nil
	}
	var eachOccurrence bool
	for _, p := range s.Parameters {
		if p.Selection == "unique" && (p.AutoAllocate || p.BlockSize > 0) && effectiveParameterUpdate(p) == "each_occurrence" {
			eachOccurrence = true
		}
	}
	if !eachOccurrence {
		return nil
	}
	return dslwalk.Walk(s, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil && ref.Action.Type == "control_parallel" {
			return fmt.Errorf("unique each-occurrence parameters require sequential actions; parallel branch cursor sharing is unsupported")
		}
		return nil
	})
}

// PlanScenarioParameterAllocations reserves disjoint ordinals for every group
// executing the same script, before any group or planned ramp starts.
func PlanScenarioParameterAllocations(scripts []*dsl.Script, users []int) ([]ParameterAllocation, error) {
	if len(scripts) != len(users) {
		return nil, fmt.Errorf("invalid scenario allocation input")
	}
	out := make([]ParameterAllocation, len(scripts))
	type cohort struct {
		population, next, iterations int
		contract                     [32]byte
	}
	cohorts := map[string]*cohort{}
	keys := make([]string, len(scripts))
	for i, s := range scripts {
		if !HasAllocatedParameters(s) {
			continue
		}
		if users[i] < 0 || users[i] > MaxEngineVUsers {
			return nil, fmt.Errorf("invalid allocation population")
		}
		data, err := json.Marshal(struct {
			Parameters []dsl.Parameter
			Sources    []dsl.DataSource
		}{s.Parameters, s.DataSources})
		if err != nil {
			return nil, fmt.Errorf("cannot inspect allocation contract")
		}
		hash := sha256.Sum256(data)
		key := s.ID
		if key == "" {
			key = fmt.Sprintf("anonymous:%x", hash)
		}
		keys[i] = key
		c := cohorts[key]
		if c == nil {
			c = &cohort{iterations: s.Config.RunLogic.Iterations, contract: hash}
			cohorts[key] = c
		}
		if c.contract != hash || c.iterations != s.Config.RunLogic.Iterations {
			return nil, fmt.Errorf("groups sharing an allocated script require identical parameter contracts and effective iteration requirements")
		}
		if c.population > MaxEngineVUsers-users[i] {
			return nil, fmt.Errorf("allocation population exceeds supported bounds")
		}
		c.population += users[i]
	}
	for i, key := range keys {
		if key == "" {
			continue
		}
		c := cohorts[key]
		out[i] = ParameterAllocation{Population: c.population, Offset: c.next, Iterations: c.iterations}
		c.next += users[i]
	}
	return out, nil
}
