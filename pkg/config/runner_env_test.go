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
		t.Setenv(EnvJavaVuserRunnerMode, "")
		t.Setenv(EnvJavaVuserRunnerURL, "")
		t.Setenv(EnvJavaVuserDefaultJDKHome, "")
		t.Setenv(EnvJavaVuserAllowLegacyJava, "")
		t.Setenv(EnvJavaVuserHarnessJar, "")
		t.Setenv(EnvJavaVuserClasspath, "")
		t.Setenv(EnvJavaVuserWorkDir, "")
		t.Setenv(EnvJavaVuserMaxProcesses, "")
		t.Setenv(EnvJavaVuserRunnerAuthToken, "")

		cfg := LoadJavaVuserOperatorConfig()
		if cfg.Enabled {
			t.Error("Enabled = true, want false when unset")
		}
		if cfg.RunnerCommand != DefaultJavaVuserRunnerCommand {
			t.Errorf("RunnerCommand = %q, want %q", cfg.RunnerCommand, DefaultJavaVuserRunnerCommand)
		}
		if cfg.RunnerCommandExplicit {
			t.Error("RunnerCommandExplicit = true, want false when unset")
		}
		// The legacy-Java floor must stay at 17 unless an operator opts in; a
		// silently-true default would accept a Java 8 JVM everywhere.
		if cfg.AllowLegacyJava {
			t.Error("AllowLegacyJava = true, want false when unset")
		}
		if cfg.RunnerMode != DefaultJavaVuserRunnerMode {
			t.Errorf("RunnerMode = %q, want %q", cfg.RunnerMode, DefaultJavaVuserRunnerMode)
		}
		if cfg.RunnerURL != DefaultJavaVuserRunnerURL {
			t.Errorf("RunnerURL = %q, want %q", cfg.RunnerURL, DefaultJavaVuserRunnerURL)
		}
		if cfg.DefaultJDKHome != "" {
			t.Errorf("DefaultJDKHome = %q, want empty", cfg.DefaultJDKHome)
		}
		if cfg.MaxProcesses != DefaultJavaVuserMaxProcesses {
			t.Errorf("MaxProcesses = %d, want %d", cfg.MaxProcesses, DefaultJavaVuserMaxProcesses)
		}
		if len(cfg.Classpath) != 0 {
			t.Errorf("Classpath = %#v, want empty", cfg.Classpath)
		}
		if cfg.AuthToken != "" {
			t.Errorf("AuthToken = %q, want empty", cfg.AuthToken)
		}
	})

	t.Run("operator overrides", func(t *testing.T) {
		t.Setenv(EnvJavaVuserRunnerEnabled, "true")
		t.Setenv(EnvJavaVuserRunnerCommand, "/opt/java")
		t.Setenv(EnvJavaVuserRunnerMode, " SIDECAR ")
		t.Setenv(EnvJavaVuserRunnerURL, " http://127.0.0.1:18098 ")
		t.Setenv(EnvJavaVuserDefaultJDKHome, " /opt/jdk-21 ")
		t.Setenv(EnvJavaVuserHarnessJar, "/opt/harness.jar")
		t.Setenv(EnvJavaVuserWorkDir, "/work")
		t.Setenv(EnvJavaVuserMaxProcesses, "8")
		t.Setenv(EnvJavaVuserRunnerAuthToken, " sidecar-loopback-secret ")

		cfg := LoadJavaVuserOperatorConfig()
		if !cfg.Enabled || cfg.RunnerCommand != "/opt/java" || !cfg.RunnerCommandExplicit || cfg.RunnerMode != "sidecar" || cfg.RunnerURL != "http://127.0.0.1:18098" || cfg.DefaultJDKHome != "/opt/jdk-21" || cfg.HarnessJar != "/opt/harness.jar" || cfg.WorkDir != "/work" || cfg.MaxProcesses != 8 || cfg.AuthToken != "sidecar-loopback-secret" {
			t.Fatalf("operator config = %#v, want overrides applied", cfg)
		}
	})

	t.Run("non-positive max processes falls back", func(t *testing.T) {
		t.Setenv(EnvJavaVuserMaxProcesses, "0")
		if got := LoadJavaVuserOperatorConfig().MaxProcesses; got != DefaultJavaVuserMaxProcesses {
			t.Fatalf("MaxProcesses = %d, want default %d", got, DefaultJavaVuserMaxProcesses)
		}
	})

	t.Run("excessive max processes is bounded", func(t *testing.T) {
		t.Setenv(EnvJavaVuserMaxProcesses, "1000000000")
		if got := LoadJavaVuserOperatorConfig().MaxProcesses; got != MaxJavaVuserMaxProcesses {
			t.Fatalf("MaxProcesses = %d, want upper bound %d", got, MaxJavaVuserMaxProcesses)
		}
	})

	t.Run("maximum process bound is accepted", func(t *testing.T) {
		t.Setenv(EnvJavaVuserMaxProcesses, "10000")
		if got := LoadJavaVuserOperatorConfig().MaxProcesses; got != MaxJavaVuserMaxProcesses {
			t.Fatalf("MaxProcesses = %d, want configured maximum %d", got, MaxJavaVuserMaxProcesses)
		}
	})

	t.Run("classpath upload defaults off", func(t *testing.T) {
		t.Setenv(EnvJavaVuserClasspathUploadEnabled, "")
		t.Setenv(EnvJavaVuserClasspathAssetDir, "")
		cfg := LoadJavaVuserOperatorConfig()
		if cfg.ClasspathUploadEnabled {
			t.Error("ClasspathUploadEnabled = true, want false when unset")
		}
		if cfg.ClasspathAssetDir != "" {
			t.Errorf("ClasspathAssetDir = %q, want empty", cfg.ClasspathAssetDir)
		}
		if JavaClasspathUploadEnabled() {
			t.Error("JavaClasspathUploadEnabled() = true, want false when unset")
		}
	})

	t.Run("classpath upload opt-in", func(t *testing.T) {
		t.Setenv(EnvJavaVuserClasspathUploadEnabled, "true")
		t.Setenv(EnvJavaVuserClasspathAssetDir, " /var/apexload/import-assets ")
		cfg := LoadJavaVuserOperatorConfig()
		if !cfg.ClasspathUploadEnabled {
			t.Error("ClasspathUploadEnabled = false, want true")
		}
		if cfg.ClasspathAssetDir != "/var/apexload/import-assets" {
			t.Errorf("ClasspathAssetDir = %q, want trimmed path", cfg.ClasspathAssetDir)
		}
		if !JavaClasspathUploadEnabled() {
			t.Error("JavaClasspathUploadEnabled() = false, want true")
		}
	})
}

func TestLoadK8sRestartOperatorConfig(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		clearK8sRestartEnv(t)

		cfg := LoadK8sRestartOperatorConfig()
		if cfg.Enabled {
			t.Error("Enabled = true, want false when unset")
		}
		if cfg.ControllerDeployment != DefaultK8sControllerDeployment {
			t.Errorf("ControllerDeployment = %q, want %q", cfg.ControllerDeployment, DefaultK8sControllerDeployment)
		}
		if cfg.GeneratorStatefulSet != DefaultK8sGeneratorStatefulSet {
			t.Errorf("GeneratorStatefulSet = %q, want %q", cfg.GeneratorStatefulSet, DefaultK8sGeneratorStatefulSet)
		}
		if cfg.GeneratorSelector != DefaultK8sGeneratorSelector {
			t.Errorf("GeneratorSelector = %q, want %q", cfg.GeneratorSelector, DefaultK8sGeneratorSelector)
		}
		if !cfg.AllowForce {
			t.Error("AllowForce = false, want true by default")
		}
	})

	t.Run("operator overrides", func(t *testing.T) {
		clearK8sRestartEnv(t)
		t.Setenv(EnvK8sRestartEnabled, "true")
		t.Setenv(EnvK8sNamespace, "perf")
		t.Setenv(EnvK8sControllerDeployment, "controller-prod")
		t.Setenv(EnvK8sGeneratorStatefulSet, "generators-prod")
		t.Setenv(EnvK8sGeneratorSelector, "app=generators")
		t.Setenv(EnvK8sRestartAllowForce, "false")

		cfg := LoadK8sRestartOperatorConfig()
		if !cfg.Enabled {
			t.Fatal("Enabled = false, want true")
		}
		if cfg.Namespace != "perf" || cfg.ControllerDeployment != "controller-prod" || cfg.GeneratorStatefulSet != "generators-prod" || cfg.GeneratorSelector != "app=generators" {
			t.Fatalf("operator config = %#v, want overrides applied", cfg)
		}
		if cfg.AllowForce {
			t.Fatal("AllowForce = true, want false")
		}
	})
}

func clearK8sRestartEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvK8sRestartEnabled, "")
	t.Setenv(EnvK8sNamespace, "")
	t.Setenv(EnvK8sControllerDeployment, "")
	t.Setenv(EnvK8sGeneratorStatefulSet, "")
	t.Setenv(EnvK8sGeneratorSelector, "")
	t.Setenv(EnvK8sRestartAllowForce, "")
}

// The legacy opt-in is read from the operator process environment, never from
// script DSL, so the env var itself has to be wired — not just the struct field
// that javarunner and javasidecar tests set directly.
func TestLoadJavaVuserOperatorConfigReadsAllowLegacyJavaFromEnv(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{value: "true", want: true},
		{value: "1", want: true},
		{value: "false", want: false},
		{value: "", want: false},
	} {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv(EnvJavaVuserAllowLegacyJava, tc.value)
			if got := LoadJavaVuserOperatorConfig().AllowLegacyJava; got != tc.want {
				t.Fatalf("AllowLegacyJava = %v, want %v for %q", got, tc.want, tc.value)
			}
		})
	}
}

func TestLoadJavaVuserSharedJVMConfig(t *testing.T) {
	for _, key := range []string{EnvJavaVuserSharedJVM, EnvJavaVuserSharedJVMVUsersPerJVM, EnvJavaVuserSharedJVMHeapMB, EnvJavaVuserJVMMemoryBudgetMB, EnvJavaVuserDedicatedJVMMemoryMB, EnvJavaVuserSlotAPIJar, EnvJavaVuserSlotShimJar} {
		t.Setenv(key, "")
	}
	t.Setenv(EnvJavaVuserHarnessJar, "/opt/apexload/java-vuser/java-vuser-runner.jar")
	cfg := LoadJavaVuserOperatorConfig().SharedJVM
	if cfg.Enabled || cfg.VUsersPerJVM != DefaultJavaVuserSharedJVMVUsersPerJVM || cfg.HeapMB != DefaultJavaVuserSharedJVMHeapMB || cfg.MemoryBudgetMB != 0 || cfg.DedicatedJVMMemoryMB != DefaultJavaVuserDedicatedJVMMemoryMB {
		t.Fatalf("defaults %+v", cfg)
	}
	if cfg.SlotAPIJar != "/opt/apexload/java-vuser/apexload-slot-api.jar" || cfg.SlotShimJar != "/opt/apexload/java-vuser/apexload-slot-shim.jar" {
		t.Fatalf("slot jars default beside the harness: %+v", cfg)
	}
	t.Setenv(EnvJavaVuserSharedJVM, "true")
	t.Setenv(EnvJavaVuserSharedJVMVUsersPerJVM, "500")
	t.Setenv(EnvJavaVuserSharedJVMHeapMB, "2048")
	t.Setenv(EnvJavaVuserJVMMemoryBudgetMB, "24576")
	t.Setenv(EnvJavaVuserDedicatedJVMMemoryMB, "192")
	t.Setenv(EnvJavaVuserSlotAPIJar, " /jars/api.jar ")
	cfg = LoadJavaVuserOperatorConfig().SharedJVM
	if !cfg.Enabled || cfg.VUsersPerJVM != 500 || cfg.HeapMB != 2048 || cfg.MemoryBudgetMB != 24576 || cfg.DedicatedJVMMemoryMB != 192 || cfg.SlotAPIJar != "/jars/api.jar" {
		t.Fatalf("explicit %+v", cfg)
	}
	for _, tc := range []struct {
		key, value string
		check      func(JavaVuserSharedJVMConfig) bool
	}{
		{EnvJavaVuserSharedJVMVUsersPerJVM, "1", func(c JavaVuserSharedJVMConfig) bool { return c.VUsersPerJVM == DefaultJavaVuserSharedJVMVUsersPerJVM }},
		{EnvJavaVuserSharedJVMVUsersPerJVM, "1025", func(c JavaVuserSharedJVMConfig) bool { return c.VUsersPerJVM == DefaultJavaVuserSharedJVMVUsersPerJVM }},
		{EnvJavaVuserSharedJVMHeapMB, "8", func(c JavaVuserSharedJVMConfig) bool { return c.HeapMB == DefaultJavaVuserSharedJVMHeapMB }},
		{EnvJavaVuserDedicatedJVMMemoryMB, "2", func(c JavaVuserSharedJVMConfig) bool {
			return c.DedicatedJVMMemoryMB == DefaultJavaVuserDedicatedJVMMemoryMB
		}},
		{EnvJavaVuserJVMMemoryBudgetMB, "-1", func(c JavaVuserSharedJVMConfig) bool { return c.MemoryBudgetMB == 0 }},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if cfg := LoadJavaVuserOperatorConfig().SharedJVM; !tc.check(cfg) {
				t.Fatalf("not normalized: %+v", cfg)
			}
		})
	}
}
