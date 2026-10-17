package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// uniqueAccounts is a unique list parameter with no allocation settings, the
// shape LoadRunner imports and hand-written scripts commonly have.
func uniqueAccounts(update string, values ...string) dsl.Parameter {
	return dsl.Parameter{Name: "account", Type: "list", Selection: "unique", Update: update, Values: values}
}

// allocatedRows returns the rows one VUser reads over the given iterations.
func allocatedRows(t *testing.T, params []dsl.Parameter, a ParameterAllocation, iterations int) []string {
	t.Helper()
	runtime, err := newAllocatedParameterRuntime(params, nil, a.Offset, a)
	if err != nil {
		t.Fatal(err)
	}
	runtime.Prime()
	var rows []string
	for i := 0; i < iterations; i++ {
		if i > 0 {
			runtime.RefreshIteration()
		}
		value, ok := runtime.ResolveNamed("account")
		if !ok {
			t.Fatalf("ordinal %d iteration %d: no row", a.Offset, i+1)
		}
		rows = append(rows, value)
	}
	return rows
}

func TestUniqueParameterWithoutAllocationSettingsGivesEachVUserItsOwnRows(t *testing.T) {
	params := []dsl.Parameter{uniqueAccounts("each_iteration", "A1", "A2", "A3", "A4", "A5", "A6")}
	if !HasAllocatedParameters(&dsl.Script{Parameters: params}) {
		t.Fatal("unique rows are not planned as an allocation")
	}
	want := []string{"A1,A2", "A3,A4", "A5,A6"}
	for vuser := range want {
		if got := strings.Join(allocatedRows(t, params, ParameterAllocation{Population: 3, Offset: vuser, Iterations: 2}, 2), ","); got != want[vuser] {
			t.Fatalf("VUser %d rows = %s, want %s", vuser, got, want[vuser])
		}
	}
}

func TestUniqueRowsStayDisjointAcrossGenerators(t *testing.T) {
	// Two generators run two VUsers each; the controller hands each generator
	// consecutive ordinals of one population of four.
	params := []dsl.Parameter{uniqueAccounts("each_iteration", "A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8")}
	owner := map[string]int{}
	for generator := 0; generator < 2; generator++ {
		for local := 0; local < 2; local++ {
			ordinal := generator*2 + local
			for _, row := range allocatedRows(t, params, ParameterAllocation{Population: 4, Offset: ordinal, Iterations: 2}, 2) {
				if previous, taken := owner[row]; taken {
					t.Fatalf("row %s went to ordinals %d and %d", row, previous, ordinal)
				}
				owner[row] = ordinal
			}
		}
	}
	if len(owner) != 8 {
		t.Fatalf("rows used = %d, want all 8", len(owner))
	}
}

func TestUniqueParameterWithoutAllocationSettingsRejectsUnplannableRuns(t *testing.T) {
	for name, tc := range map[string]struct {
		param dsl.Parameter
		a     ParameterAllocation
		want  string
	}{
		"too few rows":    {uniqueAccounts("each_iteration", "A1", "A2", "A3", "A4", "A5", "A6"), ParameterAllocation{Population: 4, Iterations: 2}, "insufficient unique rows for 4 VUsers"},
		"each occurrence": {uniqueAccounts("each_occurrence", "A1", "A2", "A3", "A4"), ParameterAllocation{Population: 2, Iterations: 1}, "requires an explicit block size"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := CompileParameterAllocation([]dsl.Parameter{tc.param}, nil, tc.a); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestUniqueParameterWithoutAllocationYieldsNoRowsOutsideAPlannedRun(t *testing.T) {
	// A runtime built without the run's allocation must not fall back to every
	// VUser walking the same rows; the unresolved reference then fails the action.
	params := []dsl.Parameter{uniqueAccounts("each_iteration", "A1", "A2", "A3")}
	for vuser := 0; vuser < 3; vuser++ {
		runtime := NewParameterRuntime(params, nil, vuser)
		runtime.Prime()
		if value, ok := runtime.ResolveNamed("account"); ok {
			t.Fatalf("VUser %d read %q without an allocation", vuser, value)
		}
	}
}

func TestUniqueParameterRequiresAClosedWorkload(t *testing.T) {
	script := &dsl.Script{
		Name:     "open-unique",
		Protocol: "stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "open", MaxTransactions: 4},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 10},
		},
		Parameters: []dsl.Parameter{uniqueAccounts("each_iteration", "A1", "A2", "A3", "A4")},
		Actions:    []dsl.Action{{Name: "homepage", Type: "request", Target: "/{{param.account}}"}},
	}
	if _, err := New(Config{Script: script, VUsers: 2, Duration: time.Second}); err == nil || !strings.Contains(err.Error(), "require a closed workload") {
		t.Fatalf("New error = %v, want the closed-workload requirement", err)
	}
}
