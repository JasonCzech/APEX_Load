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

func TestParameterRuntime_SourceNativeParameters(t *testing.T) {
	runtime := NewParameterRuntime([]dsl.Parameter{
		{Name: "iteration", Type: "current_iteration", Update: "each_iteration", PadWidth: 4},
		{Name: "lr_iteration_alias", Type: "CurrentIteration", Update: "each_iteration"},
		{Name: "lg_name", Type: "host", Update: "once", Values: []string{"lg-01"}},
		{Name: "fallback_lg_name", Type: "Host", Update: "once"},
		{Name: "clock", Type: "time", Update: "each_occurrence", Format: "15:04:05", Timezone: "UTC"},
	}, nil, 0)
	runtime.now = func() time.Time {
		return time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	}
	runtime.Prime()

	if got, _ := runtime.ResolveNamed("iteration"); got != "0001" {
		t.Fatalf("iteration initial = %q, want 0001", got)
	}
	runtime.RefreshIteration()
	if got, _ := runtime.ResolveNamed("iteration"); got != "0002" {
		t.Fatalf("iteration second = %q, want 0002", got)
	}
	if got, _ := runtime.ResolveNamed("lr_iteration_alias"); got != "2" {
		t.Fatalf("lr_iteration_alias after refresh = %q, want 2", got)
	}
	if got, _ := runtime.ResolveNamed("lg_name"); got != "lg-01" {
		t.Fatalf("lg_name = %q, want lg-01", got)
	}
	if got, ok := runtime.ResolveNamed("fallback_lg_name"); !ok || got == "" {
		t.Fatalf("fallback_lg_name = %q, %v; want non-empty host value", got, ok)
	}
	if got, _ := runtime.ResolveNamed("clock"); got != "05:06:07" {
		t.Fatalf("clock = %q, want 05:06:07", got)
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

func TestParameterRuntime_SourceStartRowAndVUserAllocation(t *testing.T) {
	provider, err := NewDataProvider([]dsl.DataSource{{
		Name:           "subs",
		Type:           "csv",
		InlineData:     "sub\n100\n200\n300\n400\n500\n",
		FirstRowHeader: true,
	}})
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}

	vuser0 := NewParameterRuntime([]dsl.Parameter{{
		Name:       "sub",
		Type:       "file",
		DataSource: "subs",
		Column:     "sub",
		Selection:  "sequential",
		Update:     "each_iteration",
		StartRow:   2,
		BlockSize:  2,
	}}, provider, 0)
	vuser0.Prime()
	if got, _ := vuser0.ResolveNamed("sub"); got != "200" {
		t.Fatalf("vuser0 initial sub = %q, want StartRow row 2", got)
	}
	vuser0.RefreshIteration()
	if got, _ := vuser0.ResolveNamed("sub"); got != "300" {
		t.Fatalf("vuser0 second sub = %q, want second row in allocated block", got)
	}

	vuser1 := NewParameterRuntime([]dsl.Parameter{{
		Name:              "sub",
		Type:              "file",
		DataSource:        "subs",
		Column:            "sub",
		Selection:         "sequential",
		Update:            "each_iteration",
		StartRow:          2,
		ValueForEachVUser: true,
		OnExhaustion:      "wrap",
	}}, provider, 1)
	vuser1.Prime()
	if got, _ := vuser1.ResolveNamed("sub"); got != "300" {
		t.Fatalf("vuser1 sub = %q, want stable value after StartRow offset", got)
	}
}

func TestParameterRuntime_BlockAllocationUsesLastRowOnVUserExhaustion(t *testing.T) {
	provider, err := NewDataProvider([]dsl.DataSource{{
		Name:           "config",
		Type:           "csv",
		InlineData:     "URL\nhttps://example.test\n",
		FirstRowHeader: true,
	}})
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}

	runtime := NewParameterRuntime([]dsl.Parameter{{
		Name:         "url",
		Type:         "file",
		DataSource:   "config",
		Column:       "URL",
		Selection:    "sequential",
		Update:       "each_iteration",
		OnExhaustion: "last",
		BlockSize:    1,
	}}, provider, 3)
	runtime.Prime()

	if got, ok := runtime.ResolveNamed("url"); !ok || got != "https://example.test" {
		t.Fatalf("vuser3 url = %q, %v; want final source row", got, ok)
	}
}

func TestParameterRuntime_VUserIDParameter(t *testing.T) {
	runtime := NewParameterRuntime([]dsl.Parameter{{
		Name:   "vuser",
		Type:   "vuser_id",
		Update: "once",
	}}, nil, 4)
	runtime.Prime()

	if got, _ := runtime.ResolveNamed("vuser"); got != "5" {
		t.Fatalf("vuser = %q, want 1-based VUser ID", got)
	}
}

func TestParameterRuntime_OnExhaustionLastKeepsFinalValue(t *testing.T) {
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
		Name:         "username",
		Type:         "file",
		DataSource:   "users",
		Column:       "username",
		Selection:    "sequential",
		Update:       "each_occurrence",
		OnExhaustion: "last",
	}}, provider, 0)

	for index, want := range []string{"alice", "bob", "bob", "bob"} {
		got, ok := runtime.ResolveNamed("username")
		if !ok || got != want {
			t.Fatalf("resolve %d = %q/%v, want %q/true", index, got, ok, want)
		}
	}
}

func TestParameterRuntime_CounterOnExhaustionLastKeepsEndValue(t *testing.T) {
	runtime := NewParameterRuntime([]dsl.Parameter{{
		Name:         "account",
		Type:         "counter",
		Update:       "each_occurrence",
		Start:        7,
		End:          9,
		Step:         1,
		OnExhaustion: "last",
	}}, nil, 0)

	for index, want := range []string{"7", "8", "9", "9", "9"} {
		got, ok := runtime.ResolveNamed("account")
		if !ok || got != want {
			t.Fatalf("resolve %d = %q/%v, want %q/true", index, got, ok, want)
		}
	}
}

func TestParameterRuntime_SameLineParameterUsesReferencedRow(t *testing.T) {
	seed := int64(17)
	provider, err := NewDataProvider([]dsl.DataSource{{
		Name:           "users",
		Type:           "csv",
		InlineData:     "username,password\nalice,a-pass\nbob,b-pass\ncarol,c-pass\n",
		FirstRowHeader: true,
	}})
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}

	runtime := NewParameterRuntime([]dsl.Parameter{
		{
			Name:       "password",
			Type:       "file",
			DataSource: "users",
			Column:     "password",
			Selection:  "random",
			SameLineAs: "username",
			Update:     "each_iteration",
		},
		{
			Name:       "username",
			Type:       "file",
			DataSource: "users",
			Column:     "username",
			Selection:  "random",
			Update:     "each_iteration",
			Seed:       &seed,
		},
	}, provider, 0)
	runtime.Prime()

	expectedPassword := map[string]string{
		"alice": "a-pass",
		"bob":   "b-pass",
		"carol": "c-pass",
	}
	for iteration := 0; iteration < 6; iteration++ {
		username, userOK := runtime.ResolveNamed("username")
		password, passwordOK := runtime.ResolveNamed("password")
		if !userOK || !passwordOK {
			t.Fatalf("iteration %d ResolveNamed ok = %v/%v, want both true", iteration, userOK, passwordOK)
		}
		if password != expectedPassword[username] {
			t.Fatalf("iteration %d password = %q for username %q, want %q", iteration, password, username, expectedPassword[username])
		}
		runtime.RefreshIteration()
	}
}

func TestParameterRuntime_TrimsFileParameterValuesBeforeInterpolation(t *testing.T) {
	vu := &VUser{
		Variables: map[string]string{},
		Script: &dsl.Script{
			Parameters: []dsl.Parameter{{
				Name:      "url",
				Type:      "file",
				Values:    []string{"https://csi-gw-ext.att.com:443 \r\n"},
				Selection: "sequential",
				Update:    "each_iteration",
			}},
		},
	}
	vu.Parameters = NewParameterRuntime(vu.Script.Parameters, nil, 0)
	vu.Parameters.Prime()

	if got := vu.interpolate("{{var.url}}/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws"); got != "https://csi-gw-ext.att.com:443/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws" {
		t.Fatalf("interpolated URL = %q", got)
	}
}

func TestParameterRuntime_ResolvesParameterNamesCaseInsensitively(t *testing.T) {
	runtime := NewParameterRuntime([]dsl.Parameter{{
		Name:   "url",
		Type:   "list",
		Values: []string{"https://csi-gw-ext.att.com:443"},
	}}, nil, 0)
	runtime.Prime()

	if got, ok := runtime.ResolveNamed("URL"); !ok || got != "https://csi-gw-ext.att.com:443" {
		t.Fatalf("ResolveNamed(\"URL\") = %q, %v", got, ok)
	}
}

func TestParameterRuntime_CaseInsensitiveDuplicateFallbackIsDeterministic(t *testing.T) {
	runtime := NewParameterRuntime([]dsl.Parameter{
		{Name: "Token", Type: "list", Values: []string{"first"}},
		{Name: "token", Type: "list", Values: []string{"second"}},
	}, nil, 0)
	runtime.Prime()

	for i := 0; i < 100; i++ {
		if got, ok := runtime.ResolveNamed("TOKEN"); !ok || got != "first" {
			t.Fatalf("ResolveNamed(\"TOKEN\") iteration %d = %q, %v, want deterministic first declaration", i, got, ok)
		}
	}
	if got, ok := runtime.ResolveNamed("token"); !ok || got != "second" {
		t.Fatalf("exact ResolveNamed(\"token\") = %q, %v, want exact legacy declaration", got, ok)
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
