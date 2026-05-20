package engine

import (
	"regexp"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestParameterRuntime_CounterUpdateModes(t *testing.T) {
	runtime := NewParameterRuntime([]dsl.Parameter{
		{Name: "once_counter", Type: "counter", Update: "once", Start: 10, Step: 1},
		{Name: "iter_counter", Type: "counter", Update: "each_iteration", Start: 20, Step: 5},
		{Name: "occurrence_counter", Type: "counter", Update: "each_occurrence", Start: 30, Step: 2},
	}, nil, 0)
	runtime.Prime()

	if got, _ := runtime.ResolveNamed("once_counter"); got != "10" {
		t.Fatalf("once_counter initial = %q, want 10", got)
	}
	if got, _ := runtime.ResolveNamed("once_counter"); got != "10" {
		t.Fatalf("once_counter cached = %q, want 10", got)
	}

	if got, _ := runtime.ResolveNamed("iter_counter"); got != "20" {
		t.Fatalf("iter_counter initial = %q, want 20", got)
	}
	runtime.RefreshIteration()
	if got, _ := runtime.ResolveNamed("iter_counter"); got != "25" {
		t.Fatalf("iter_counter after refresh = %q, want 25", got)
	}

	if got, _ := runtime.ResolveNamed("occurrence_counter"); got != "30" {
		t.Fatalf("occurrence_counter first = %q, want 30", got)
	}
	if got, _ := runtime.ResolveNamed("occurrence_counter"); got != "32" {
		t.Fatalf("occurrence_counter second = %q, want 32", got)
	}
}

func TestParameterRuntime_DataSourceParameter(t *testing.T) {
	provider, err := NewDataProvider([]dsl.DataSource{{
		Name:           "users",
		Type:           "csv",
		InlineData:     "username\nalice\nbob\n",
		FirstRowHeader: true,
	}})
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}

	runtime := NewParameterRuntime([]dsl.Parameter{{
		Name:       "username",
		Type:       "file",
		DataSource: "users",
		Column:     "username",
		Selection:  "sequential",
		Update:     "each_iteration",
	}}, provider, 0)
	runtime.Prime()

	if got, _ := runtime.ResolveNamed("username"); got != "alice" {
		t.Fatalf("username initial = %q, want alice", got)
	}
	runtime.RefreshIteration()
	if got, _ := runtime.ResolveNamed("username"); got != "bob" {
		t.Fatalf("username second iteration = %q, want bob", got)
	}
}

func TestVUser_Interpolate_ParametersAndFunctions(t *testing.T) {
	vu := &VUser{
		Variables: map[string]string{},
		Script: &dsl.Script{
			Parameters: []dsl.Parameter{
				{Name: "session_id", Type: "counter", Update: "once", Start: 42, Step: 1, PadWidth: 4},
				{Name: "request_id", Type: "counter", Update: "each_occurrence", Start: 100, Step: 1},
			},
		},
	}
	vu.Parameters = NewParameterRuntime(vu.Script.Parameters, nil, 0)
	vu.Parameters.Prime()

	if got := vu.interpolate("{{param.session_id}}-{{var.session_id}}"); got != "0042-0042" {
		t.Fatalf("parameter interpolation = %q, want 0042-0042", got)
	}
	if got := vu.interpolate("{{param.request_id}}/{{param.request_id}}"); got != "100/101" {
		t.Fatalf("occurrence interpolation = %q, want 100/101", got)
	}

	randomValue := vu.interpolate("{{fn.random(1,3)}}")
	if randomValue != "1" && randomValue != "2" && randomValue != "3" {
		t.Fatalf("fn.random value = %q, want 1..3", randomValue)
	}

	randomString := vu.interpolate("{{fn.random_string(12)}}")
	if len(randomString) != 12 {
		t.Fatalf("fn.random_string length = %d, want 12", len(randomString))
	}

	isoTimestamp := vu.interpolate("{{fn.timestamp.iso8601}}")
	if _, err := time.Parse(time.RFC3339, isoTimestamp); err != nil {
		t.Fatalf("fn.timestamp.iso8601 parse error: %v", err)
	}

	uuidValue := vu.interpolate("{{fn.uuid()}}")
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidPattern.MatchString(uuidValue) {
		t.Fatalf("fn.uuid() interpolation returned non-uuid value: %q", uuidValue)
	}

	fakeUUIDValue := vu.interpolate("{{fake.uuid}}")
	if !uuidPattern.MatchString(fakeUUIDValue) {
		t.Fatalf("fake.uuid interpolation returned non-uuid value: %q", fakeUUIDValue)
	}

	fakeEmailValue := vu.interpolate("{{fake.email}}")
	fakeEmailPattern := regexp.MustCompile(`^[a-z0-9]{12}@example\.test$`)
	if !fakeEmailPattern.MatchString(fakeEmailValue) {
		t.Fatalf("fake.email interpolation returned unexpected value: %q", fakeEmailValue)
	}
}
