package perfevidence

import (
	"errors"
	"net"
	"net/url"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func JavaFixtureScript(workload, endpoint string, classpath []string) (*dsl.Script, error) {
	class := "LightActions"
	if workload == "middleware-heavy" {
		class = "HeavyActions"
	} else if workload != "light" {
		return nil, errors.New("Java fixture workload must be light or middleware-heavy")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, errors.New("Java fixture Endpoint must be a loopback HTTP origin")
	}
	if len(classpath) == 0 {
		return nil, errors.New("Java fixture requires a precompiled classpath")
	}
	dedicated := true
	action := func(method string) dsl.Action {
		return dsl.Action{Name: class + "." + method, Type: "java_vuser_run", Target: class, Params: map[string]any{"method": method, "lr_parameters": map[string]any{"Endpoint": endpoint}}, Metadata: map[string]any{"emits_business_transactions": true}}
	}
	s := &dsl.Script{ID: "java-evidence-" + workload, Name: "Java evidence " + workload, Version: "1.0.0", Protocol: "java_vuser",
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{Classpath: classpath, DedicatedClassLoader: &dedicated}, Pacing: dsl.PacingConfig{Type: "fixed_interval", IntervalMs: 500}},
		Init:   []dsl.Action{action("init")}, Actions: []dsl.Action{action("action")}, Teardown: []dsl.Action{action("end")}}
	if err := dsl.ValidateScriptError(s); err != nil {
		return nil, err
	}
	return s, nil
}
