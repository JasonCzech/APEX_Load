package api

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
)

func observerOverlapGroup(name string, usernames ...string) TestGroup {
	identities := make([]dsl.BrowserIdentity, len(usernames))
	for i, username := range usernames {
		identities[i] = dsl.BrowserIdentity{Username: username, StorageState: "{{secret.state}}"}
	}
	return TestGroup{Name: name, Protocol: "browser", Role: GroupRoleObserver, script: &dsl.Script{Protocol: "browser",
		Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{Session: &dsl.BrowserSessionConfig{Strategy: dsl.BrowserSessionStorageStatePool, Identities: identities}}}}}
}

// Launch preflight warns when an observer's user also signs in as load, in a
// list parameter or a data source (inline, base64 or a file asset), or as
// another observer group's user; disjoint pools pass quietly.
func TestObserverIdentityOverlapLaunchWarnings(t *testing.T) {
	root := t.TempDir()
	if err := fileasset.RegisterRoot(root); err != nil {
		t.Fatal(err)
	}
	usersFile := filepath.Join(root, "load-users.csv")
	if err := os.WriteFile(usersFile, []byte("username,password\nload1@acme.test,x\n\"perf3@acme.test\",y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	load := TestGroup{Name: "load", Protocol: "http", script: &dsl.Script{Protocol: "http",
		Parameters: []dsl.Parameter{{Name: "user", Type: "list", Values: []string{"load2@acme.test", "PERF1@acme.test"}}},
		DataSources: []dsl.DataSource{
			{Name: "inline", Type: "csv", InlineData: "username\nload3@acme.test\nperf2@acme.test\n"},
			{Name: "encoded", Type: "csv", Encoding: "base64", InlineData: base64.StdEncoding.EncodeToString([]byte("perf4@acme.test\tsecret"))},
			{Name: "file", Type: "csv", Path: usersFile},
			{Name: "missing", Type: "csv", Path: filepath.Join(root, "missing.csv")},
		}}}
	observers := observerOverlapGroup("observers", "perf1@acme.test", "perf2@acme.test", "perf3@acme.test", "perf4@acme.test", "perf9@acme.test")
	second := observerOverlapGroup("second observers", "perf9@acme.test", "perf10@acme.test")
	warnings := observerIdentityOverlapLaunchWarnings([]TestGroup{observers, load, second})
	if len(warnings) != 2 {
		t.Fatalf("warnings = %+v", warnings)
	}
	byOther := map[string]LaunchConflict{}
	for _, warning := range warnings {
		if warning.Code != "observer_identity_overlap" || warning.Severity != "warning" || warning.ResourceID != "observers" {
			t.Fatalf("warning = %+v", warning)
		}
		for _, other := range []string{`group "load"`, `group "second observers"`} {
			if strings.Contains(warning.Message, other) {
				byOther[other] = warning
			}
		}
	}
	if got := byOther[`group "load"`].Message; !strings.Contains(got, `shares users with group "load": perf1@acme.test, perf2@acme.test, perf3@acme.test, perf4@acme.test.`) {
		t.Fatalf("load overlap = %q", got)
	}
	if got := byOther[`group "second observers"`].Message; !strings.Contains(got, `shares users with group "second observers": perf9@acme.test.`) {
		t.Fatalf("observer overlap = %q", got)
	}
	if warnings := observerIdentityOverlapLaunchWarnings([]TestGroup{observerOverlapGroup("observers", "obs1@acme.test"), load}); len(warnings) != 0 {
		t.Fatalf("disjoint pools warned: %+v", warnings)
	}
	if warnings := observerIdentityOverlapLaunchWarnings([]TestGroup{load}); warnings != nil {
		t.Fatalf("a run without observers warned: %+v", warnings)
	}
}

// One warning names at most five users and counts the rest.
func TestObserverIdentityOverlapBoundsNamedUsers(t *testing.T) {
	usernames := []string{"u1@a.test", "u2@a.test", "u3@a.test", "u4@a.test", "u5@a.test", "u6@a.test", "u7@a.test"}
	load := TestGroup{Name: "load", script: &dsl.Script{Parameters: []dsl.Parameter{{Name: "user", Type: "list", Values: usernames}}}}
	warnings := observerIdentityOverlapLaunchWarnings([]TestGroup{observerOverlapGroup("observers", usernames...), load})
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "u5@a.test and 2 more.") || strings.Contains(warnings[0].Message, "u6@a.test") {
		t.Fatalf("warnings = %+v", warnings)
	}
}

// The scenario builder's launch review carries the warning.
func TestPreflightLaunchWarnsWhenObserversShareLoadUsers(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID: "observer-script", Name: "Observer", Protocol: "browser", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://acme.lightning.force.com", Browser: dsl.BrowserConfig{Session: &dsl.BrowserSessionConfig{
			Strategy:   dsl.BrowserSessionStorageStatePool,
			Identities: []dsl.BrowserIdentity{{Username: "perf1@acme.test", StorageState: "{{secret.sf_state_1}}"}},
		}}},
		Actions: []dsl.Action{{Name: "Home", Type: "browser_navigate", Target: "/lightning/page/home"}},
	})
	runner.StoreScript(&dsl.Script{
		ID: "load-script", Name: "Load", Protocol: "http", Version: "1.0.0",
		Config:     dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Parameters: []dsl.Parameter{{Name: "user", Type: "list", Values: []string{"perf1@acme.test", "load1@acme.test"}}},
		Actions:    []dsl.Action{{Name: "Query", Type: "request", Method: "GET", Target: "/services/data/v61.0/query?q={{user}}"}},
	})
	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name: "Observed load", Duration: "5m",
		Groups: []CreateGroupEntry{{Name: "observers", ScriptID: "observer-script", VUsers: 1}, {Name: "load", ScriptID: "load-script", VUsers: 2}},
	}})
	if err != nil {
		t.Fatalf("PreflightLaunch: %v", err)
	}
	for _, warning := range preflight.Warnings {
		if warning.Code == "observer_identity_overlap" && strings.Contains(warning.Message, `Observer group "observers" shares users with group "load": perf1@acme.test.`) {
			return
		}
	}
	t.Fatalf("warnings = %+v", preflight.Warnings)
}
