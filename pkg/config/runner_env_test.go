package config

import "testing"

func TestJavaVuserRunnerEnabled(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		val  string
		want bool
	}{
		{name: "unset", set: false, want: false},
		{name: "empty", set: true, val: "", want: false},
		{name: "true", set: true, val: "true", want: true},
		{name: "one", set: true, val: "1", want: true},
		{name: "false", set: true, val: "false", want: false},
		{name: "garbage", set: true, val: "yes-please", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(EnvJavaVuserRunnerEnabled, tc.val)
			} else {
				// Ensure a clean ambient value for this subtest.
				t.Setenv(EnvJavaVuserRunnerEnabled, "")
			}
			if got := JavaVuserRunnerEnabled(); got != tc.want {
				t.Fatalf("JavaVuserRunnerEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoadJavaVuserOperatorConfig(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		t.Setenv(EnvJavaVuserRunnerEnabled, "")
		t.Setenv(EnvJavaVuserRunnerCommand, "")
		t.Setenv(EnvJavaVuserHarnessJar, "")
		t.Setenv(EnvJavaVuserClasspath, "")
		t.Setenv(EnvJavaVuserWorkDir, "")
		t.Setenv(EnvJavaVuserMaxProcesses, "")

		cfg := LoadJavaVuserOperatorConfig()
		if cfg.Enabled {
			t.Error("Enabled = true, want false when unset")
		}
		if cfg.RunnerCommand != DefaultJavaVuserRunnerCommand {
			t.Errorf("RunnerCommand = %q, want %q", cfg.RunnerCommand, DefaultJavaVuserRunnerCommand)
		}
		if cfg.MaxProcesses != DefaultJavaVuserMaxProcesses {
			t.Errorf("MaxProcesses = %d, want %d", cfg.MaxProcesses, DefaultJavaVuserMaxProcesses)
		}
		if len(cfg.Classpath) != 0 {
			t.Errorf("Classpath = %#v, want empty", cfg.Classpath)
		}
	})

	t.Run("operator overrides", func(t *testing.T) {
		t.Setenv(EnvJavaVuserRunnerEnabled, "true")
		t.Setenv(EnvJavaVuserRunnerCommand, "/opt/java")
		t.Setenv(EnvJavaVuserHarnessJar, "/opt/harness.jar")
		t.Setenv(EnvJavaVuserWorkDir, "/work")
		t.Setenv(EnvJavaVuserMaxProcesses, "8")

		cfg := LoadJavaVuserOperatorConfig()
		if !cfg.Enabled || cfg.RunnerCommand != "/opt/java" || cfg.HarnessJar != "/opt/harness.jar" || cfg.WorkDir != "/work" || cfg.MaxProcesses != 8 {
			t.Fatalf("operator config = %#v, want overrides applied", cfg)
		}
	})

	t.Run("non-positive max processes falls back", func(t *testing.T) {
		t.Setenv(EnvJavaVuserMaxProcesses, "0")
		if got := LoadJavaVuserOperatorConfig().MaxProcesses; got != DefaultJavaVuserMaxProcesses {
			t.Fatalf("MaxProcesses = %d, want default %d", got, DefaultJavaVuserMaxProcesses)
		}
	})
}
