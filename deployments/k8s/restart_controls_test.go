package k8s_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRestartControlsGrantOnlyScopedRBAC(t *testing.T) {
	for _, path := range []string{
		"aks/restart-controls.yaml",
		"eks/restart-controls.yaml",
		"local/restart-controls.yaml",
		"onprem/restart-controls.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			docs := readRestartControlDocuments(t, path)
			if len(docs) != 2 {
				t.Fatalf("document count = %d, want exactly Role and RoleBinding", len(docs))
			}
			role := findManifestKind(t, docs, "Role")
			if role.Metadata.Name != "apexload-restart-controls" {
				t.Fatalf("Role name = %q", role.Metadata.Name)
			}
			got := map[string][]string{}
			for _, rule := range role.Rules {
				if len(rule.ResourceNames) != 0 || len(rule.NonResourceURLs) != 0 {
					t.Fatalf("Role rule contains unexpected resourceNames/nonResourceURLs: %#v", rule)
				}
				key := strings.Join(rule.APIGroups, ",") + "/" + strings.Join(rule.Resources, ",")
				if _, duplicate := got[key]; duplicate {
					t.Fatalf("Role contains duplicate rule for %q", key)
				}
				verbs := append([]string(nil), rule.Verbs...)
				sort.Strings(verbs)
				got[key] = verbs
			}
			want := map[string][]string{
				"/pods":             {"delete", "get", "list"},
				"/resourcequotas":   {"get", "list"},
				"apps/deployments":  {"get", "patch"},
				"apps/statefulsets": {"get", "patch"},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Role rules = %#v, want %#v", got, want)
			}

			binding := findManifestKind(t, docs, "RoleBinding")
			if binding.Metadata.Name != role.Metadata.Name {
				t.Fatalf("RoleBinding name = %q, want %q", binding.Metadata.Name, role.Metadata.Name)
			}
			if binding.RoleRef.APIGroup != "rbac.authorization.k8s.io" || binding.RoleRef.Kind != "Role" || binding.RoleRef.Name != role.Metadata.Name {
				t.Fatalf("RoleBinding roleRef = %#v", binding.RoleRef)
			}
			if len(binding.Subjects) != 1 {
				t.Fatalf("RoleBinding subjects = %#v", binding.Subjects)
			}
			subject := binding.Subjects[0]
			if role.Metadata.Namespace == "" || binding.Metadata.Namespace == "" || subject.Namespace == "" ||
				role.Metadata.Namespace != binding.Metadata.Namespace || subject.Namespace != binding.Metadata.Namespace ||
				subject.APIGroup != "" || subject.Kind != "ServiceAccount" || subject.Name != "apexload-controller" {
				t.Fatalf("RoleBinding subject/namespace = subject %#v binding %q role %q", subject, binding.Metadata.Namespace, role.Metadata.Namespace)
			}
		})
	}
}

func TestRestartControlsAreIncludedByDefault(t *testing.T) {
	for _, path := range []string{
		"aks/kustomization.yaml",
		"eks/kustomization.yaml",
		"local/kustomization.yaml",
		"onprem/kustomization.yaml",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(raw), "restart-controls.yaml") {
			t.Fatalf("%s does not include restart-controls.yaml by default", path)
		}
		// cluster-access.example.yaml binds RBAC to a group that a REMOTE
		// controller's IAM principal maps into. Including it by default would
		// grant pod-delete and workload-patch authority to any principal an
		// EKS access entry places in that group, on every fresh install.
		if strings.Contains(string(raw), "cluster-access.example.yaml") {
			t.Fatalf("%s includes cluster-access.example.yaml; remote-controller RBAC must remain opt-in", path)
		}
	}
}

func TestRestartControlsRenderForEveryKubernetesBase(t *testing.T) {
	for _, overlay := range []string{"local", "onprem", "aks", "eks"} {
		t.Run(overlay, func(t *testing.T) {
			output, err := kustomizeBuildCommand(t, overlay).CombinedOutput()
			if err != nil {
				t.Fatalf("kustomize build %s: %v\n%s", overlay, err, output)
			}
			docs := decodeWorkloadDocuments(t, output)
			controllerServiceAccount := findWorkload(t, docs, "ServiceAccount", "apexload-controller")
			if controllerServiceAccount.AutomountServiceAccountToken == nil || *controllerServiceAccount.AutomountServiceAccountToken {
				t.Fatalf("controller ServiceAccount automountServiceAccountToken = %#v, want false", controllerServiceAccount.AutomountServiceAccountToken)
			}
			controller := findWorkload(t, docs, "Deployment", "apexload-controller")
			if controller.Spec.Template.Spec.AutomountServiceAccountToken == nil || !*controller.Spec.Template.Spec.AutomountServiceAccountToken {
				t.Fatalf("controller automountServiceAccountToken = %#v, want true", controller.Spec.Template.Spec.AutomountServiceAccountToken)
			}
			if controller.Spec.Template.Spec.ServiceAccountName != "apexload-controller" {
				t.Fatalf("controller serviceAccountName = %q", controller.Spec.Template.Spec.ServiceAccountName)
			}
			env := containerEnv(controller, "controller")
			if env["APEXLOAD_K8S_RESTART_ENABLED"].Value != "true" {
				t.Fatalf("restart enabled env = %#v", env["APEXLOAD_K8S_RESTART_ENABLED"])
			}
			if env["APEXLOAD_K8S_NAMESPACE"].ValueFrom.FieldRef.FieldPath != "metadata.namespace" {
				t.Fatalf("namespace env = %#v", env["APEXLOAD_K8S_NAMESPACE"])
			}

			generator := findWorkload(t, docs, "StatefulSet", "apexload-generator")
			if generator.Spec.Template.Spec.AutomountServiceAccountToken == nil || *generator.Spec.Template.Spec.AutomountServiceAccountToken {
				t.Fatalf("generator automountServiceAccountToken = %#v, want false", generator.Spec.Template.Spec.AutomountServiceAccountToken)
			}
			assertRenderedRestartRBAC(t, docs, controller.Spec.Template.Spec.ServiceAccountName)
		})
	}
}

func kustomizeBuildCommand(t *testing.T, overlay string) *exec.Cmd {
	t.Helper()
	if kubectl, err := exec.LookPath("kubectl"); err == nil {
		return exec.Command(kubectl, "kustomize", overlay)
	}
	if kustomize, err := exec.LookPath("kustomize"); err == nil {
		return exec.Command(kustomize, "build", overlay)
	}
	t.Skip("kustomize or kubectl is required to render Kubernetes bases")
	return nil
}

func TestHelmRestartControlsDefaultAndOptOut(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required to render the chart")
	}
	baseArgs := helmTemplateArgs("../helm/apexload")

	assertRender := func(t *testing.T, disabled bool) {
		t.Helper()
		args := append([]string(nil), baseArgs...)
		if disabled {
			args = append(args, "--set", "restartControls.enabled=false")
		}
		output, err := exec.Command(helm, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("helm template: %v\n%s", err, output)
		}
		docs := decodeWorkloadDocuments(t, output)
		controllerServiceAccount := findWorkload(t, docs, "ServiceAccount", "apexload-controller")
		if controllerServiceAccount.AutomountServiceAccountToken == nil || *controllerServiceAccount.AutomountServiceAccountToken {
			t.Fatalf("controller ServiceAccount automountServiceAccountToken = %#v, want false", controllerServiceAccount.AutomountServiceAccountToken)
		}
		generatorServiceAccount := findWorkload(t, docs, "ServiceAccount", "apexload-generator")
		if generatorServiceAccount.AutomountServiceAccountToken == nil || *generatorServiceAccount.AutomountServiceAccountToken {
			t.Fatalf("generator ServiceAccount automountServiceAccountToken = %#v, want false", generatorServiceAccount.AutomountServiceAccountToken)
		}
		controller := findWorkload(t, docs, "Deployment", "apexload-controller")
		mounted := controller.Spec.Template.Spec.AutomountServiceAccountToken != nil && *controller.Spec.Template.Spec.AutomountServiceAccountToken
		env := containerEnv(controller, "controller")
		generator := findWorkload(t, docs, "StatefulSet", "apexload-generator")
		if generator.Spec.Template.Spec.AutomountServiceAccountToken == nil || *generator.Spec.Template.Spec.AutomountServiceAccountToken {
			t.Fatalf("generator pod automountServiceAccountToken = %#v, want false", generator.Spec.Template.Spec.AutomountServiceAccountToken)
		}
		if disabled {
			if mounted {
				t.Fatal("disabled Helm restart controls still mount the controller token")
			}
			assertRenderedRestartControlsDisabled(t, env)
			if hasWorkload(docs, "Role", "apexload-restart-controls") || hasWorkload(docs, "RoleBinding", "apexload-restart-controls") {
				t.Fatal("disabled Helm restart controls still render RBAC")
			}
			return
		}
		if !mounted || env["APEXLOAD_K8S_RESTART_ENABLED"].Value != "true" {
			t.Fatalf("default Helm controller restart configuration = mounted %v env %#v", mounted, env["APEXLOAD_K8S_RESTART_ENABLED"])
		}
		for name, want := range map[string]string{
			"APEXLOAD_K8S_CONTROLLER_DEPLOYMENT": "apexload-controller",
			"APEXLOAD_K8S_GENERATOR_STATEFULSET": "apexload-generator",
			"APEXLOAD_K8S_GENERATOR_SELECTOR":    "app.kubernetes.io/name=apexload-generator",
			"APEXLOAD_K8S_RESTART_ALLOW_FORCE":   "true",
		} {
			if env[name].Value != want {
				t.Errorf("%s = %q, want %q", name, env[name].Value, want)
			}
		}
		assertRenderedRestartRBAC(t, docs, "apexload-controller")
	}

	t.Run("enabled by default", func(t *testing.T) { assertRender(t, false) })
	t.Run("explicit opt out", func(t *testing.T) { assertRender(t, true) })
	t.Run("HA opt out keeps only the projected leadership token", func(t *testing.T) {
		args := append([]string(nil), baseArgs...)
		args = append(args,
			"--set", "restartControls.enabled=false",
			"--set", "controller.ha.enabled=true",
			"--set", "importAssets.persistence.enabled=true",
			"--set", "importAssets.persistence.existingClaim=apexload-import-assets",
		)
		output, err := exec.Command(helm, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("helm template: %v\n%s", err, output)
		}
		docs := decodeWorkloadDocuments(t, output)
		controller := findWorkload(t, docs, "Deployment", "apexload-controller")
		if controller.Spec.Template.Spec.AutomountServiceAccountToken == nil || *controller.Spec.Template.Spec.AutomountServiceAccountToken {
			t.Fatal("HA opt-out must keep automatic service-account token mounting disabled")
		}
		assertRenderedRestartControlsDisabled(t, containerEnv(controller, "controller"))
		if !bytes.Contains(output, []byte("serviceAccountToken:")) || !bytes.Contains(output, []byte("active-endpoint-identity")) {
			t.Fatal("HA opt-out did not retain the projected EndpointSlice leadership token")
		}
		if hasWorkload(docs, "Role", "apexload-restart-controls") || hasWorkload(docs, "RoleBinding", "apexload-restart-controls") {
			t.Fatal("HA opt-out still renders restart RBAC")
		}
	})
	t.Run("custom workload names and selector", func(t *testing.T) {
		args := append([]string(nil), baseArgs...)
		args = append(args,
			"--set", "controller.serviceAccountName=custom-controller-sa",
			"--set", "restartControls.controllerDeployment=custom-controller",
			"--set", "restartControls.generatorStatefulSet=custom-generators",
			"--set", "restartControls.generatorSelector=app=custom-generator",
			"--set", "restartControls.allowForce=false",
		)
		output, err := exec.Command(helm, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("helm template: %v\n%s", err, output)
		}
		docs := decodeWorkloadDocuments(t, output)
		controller := findWorkload(t, docs, "Deployment", "apexload-controller")
		env := containerEnv(controller, "controller")
		for name, want := range map[string]string{
			"APEXLOAD_K8S_CONTROLLER_DEPLOYMENT": "custom-controller",
			"APEXLOAD_K8S_GENERATOR_STATEFULSET": "custom-generators",
			"APEXLOAD_K8S_GENERATOR_SELECTOR":    "app=custom-generator",
			"APEXLOAD_K8S_RESTART_ALLOW_FORCE":   "false",
		} {
			if env[name].Value != want {
				t.Errorf("%s = %q, want %q", name, env[name].Value, want)
			}
		}
		if controller.Spec.Template.Spec.ServiceAccountName != "custom-controller-sa" {
			t.Errorf("controller serviceAccountName = %q, want custom-controller-sa", controller.Spec.Template.Spec.ServiceAccountName)
		}
		findWorkload(t, docs, "ServiceAccount", "custom-controller-sa")
		assertRenderedRestartRBAC(t, docs, "custom-controller-sa")
	})
	t.Run("accepts documented Kubernetes selector subset", func(t *testing.T) {
		for _, selector := range []string{
			"a",
			"!retired",
			"app.kubernetes.io/name=apexload-generator",
			"environment in (production)",
			"environment notin (production)",
			"environment notin (production\\, qa)",
		} {
			args := append([]string(nil), baseArgs...)
			args = append(args, "--set-string", "restartControls.generatorSelector="+selector)
			if output, err := exec.Command(helm, args...).CombinedOutput(); err != nil {
				t.Errorf("helm rejected documented selector %q: %v\n%s", selector, err, output)
			}
		}
	})
	t.Run("rejects invalid restart targets", func(t *testing.T) {
		for _, invalid := range [][]string{
			{"--set-string", "restartControls.generatorSelector=app in ("},
			{"--set-string", "restartControls.generatorSelector=app=-bad"},
			{"--set-string", "restartControls.generatorSelector=app=bad-"},
			{"--set-string", "restartControls.generatorSelector=a..b/key=value"},
			{"--set-string", "restartControls.generatorSelector=app=" + strings.Repeat("a", 64)},
			{"--set-string", "restartControls.controllerDeployment=" + strings.Repeat("a", 254)},
			{"--set-string", "restartControls.generatorStatefulSet=invalid_name"},
		} {
			args := append([]string(nil), baseArgs...)
			args = append(args, invalid...)
			if output, err := exec.Command(helm, args...).CombinedOutput(); err == nil {
				t.Fatalf("helm accepted invalid restart value %q\n%s", invalid[len(invalid)-1], output)
			}
		}
	})
}

func TestHelmRestartControlsReuseValuesUpgrade(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required to render the chart")
	}

	for _, tc := range []struct {
		name          string
		restartValues map[string]any
		extraArgs     []string
		wantEnabled   bool
		wantSelector  string
		wantAllow     string
	}{
		{
			name:         "legacy release without restart values inherits enabled defaults",
			wantEnabled:  true,
			wantSelector: "app.kubernetes.io/name=apexload-generator",
			wantAllow:    "true",
		},
		{
			name: "partial reused map preserves overrides and fills missing keys",
			restartValues: map[string]any{
				"allowForce":        false,
				"generatorSelector": "app=legacy-generator",
			},
			wantEnabled:  true,
			wantSelector: "app=legacy-generator",
			wantAllow:    "false",
		},
		{
			name:        "set enabled false opts a legacy release out",
			extraArgs:   []string{"--set", "restartControls.enabled=false"},
			wantEnabled: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Replacing the copied chart defaults with the release's stored values
			// is equivalent to Helm upgrade --reuse-values: new chart defaults are
			// not available to templates unless the chart supplies its own fallback.
			chart := helmChartWithReusedRestartValues(t, tc.restartValues)
			lintArgs := append(helmLintArgs(chart), tc.extraArgs...)
			if output, err := exec.Command(helm, lintArgs...).CombinedOutput(); err != nil {
				t.Fatalf("helm lint with reused values: %v\n%s", err, output)
			}
			args := append(helmTemplateArgs(chart), tc.extraArgs...)
			output, err := exec.Command(helm, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("helm template with reused values: %v\n%s", err, output)
			}

			docs := decodeWorkloadDocuments(t, output)
			controller := findWorkload(t, docs, "Deployment", "apexload-controller")
			mounted := controller.Spec.Template.Spec.AutomountServiceAccountToken != nil && *controller.Spec.Template.Spec.AutomountServiceAccountToken
			env := containerEnv(controller, "controller")
			if !tc.wantEnabled {
				if mounted {
					t.Fatal("legacy release opt-out still mounts the controller service-account token")
				}
				assertRenderedRestartControlsDisabled(t, env)
				if hasWorkload(docs, "Role", "apexload-restart-controls") || hasWorkload(docs, "RoleBinding", "apexload-restart-controls") {
					t.Fatal("legacy release opt-out still renders restart-control RBAC")
				}
				return
			}

			if !mounted || env["APEXLOAD_K8S_RESTART_ENABLED"].Value != "true" {
				t.Fatalf("legacy release restart configuration = mounted %v env %#v", mounted, env["APEXLOAD_K8S_RESTART_ENABLED"])
			}
			for name, want := range map[string]string{
				"APEXLOAD_K8S_CONTROLLER_DEPLOYMENT": "apexload-controller",
				"APEXLOAD_K8S_GENERATOR_STATEFULSET": "apexload-generator",
				"APEXLOAD_K8S_GENERATOR_SELECTOR":    tc.wantSelector,
				"APEXLOAD_K8S_RESTART_ALLOW_FORCE":   tc.wantAllow,
			} {
				if env[name].Value != want {
					t.Errorf("%s = %q, want %q", name, env[name].Value, want)
				}
			}
			assertRenderedRestartRBAC(t, docs, controller.Spec.Template.Spec.ServiceAccountName)
		})
	}
}

func TestHelmRestartControlsUpgradeRunbook(t *testing.T) {
	optOutCommand := `helm upgrade apexload deployments/helm/apexload \
  --namespace apexload \
  --reuse-values \
  --set restartControls.enabled=false`
	for _, path := range []string{
		"../helm/apexload/README.md",
		"../../docs/CLUSTERS_AND_CLOUD.md",
	} {
		raw, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(raw), optOutCommand) {
			t.Errorf("%s does not document the exact --reuse-values opt-out command", path)
		}
	}

	raw, err := os.ReadFile(filepath.Clean("../../docs/CLUSTERS_AND_CLOUD.md"))
	if err != nil {
		t.Fatalf("read Helm restart-controls runbook: %v", err)
	}
	runbook := string(raw)
	for name, command := range map[string]string{
		"ownership preflight": `kubectl --namespace "$namespace" get \
  role/apexload-restart-controls \
  rolebinding/apexload-restart-controls \
  -o yaml`,
		"Helm ownership label": `kubectl --namespace "$namespace" label \
  role/apexload-restart-controls \
  rolebinding/apexload-restart-controls \
  app.kubernetes.io/managed-by=Helm --overwrite`,
		"Helm ownership annotations": `kubectl --namespace "$namespace" annotate \
  role/apexload-restart-controls \
  rolebinding/apexload-restart-controls \
  meta.helm.sh/release-name="$release" \
  meta.helm.sh/release-namespace="$namespace" \
  --overwrite`,
		"controlled RoleBinding deletion": `kubectl --namespace "$namespace" delete rolebinding/apexload-restart-controls`,
		"controlled Role deletion":        `kubectl --namespace "$namespace" delete role/apexload-restart-controls`,
		"rollback":                        `kubectl apply -f path/to/original-restart-controls.yaml`,
	} {
		if !strings.Contains(runbook, command) {
			t.Errorf("Helm restart-controls runbook does not document the exact %s command", name)
		}
	}
}

func helmTemplateArgs(chart string) []string {
	return append([]string{"template", "apexload", chart}, helmProductionValueArgs()...)
}

func helmLintArgs(chart string) []string {
	return append([]string{"lint", chart}, helmProductionValueArgs()...)
}

func helmProductionValueArgs() []string {
	return []string{
		"--set", "image.registry=registry.example.net",
		"--set", "ingress.host=apexload.example.net",
		"--set", "dataPlatform.azureStorageAccount=apexloadstorage",
		"--set", "dataPlatform.keyVaultUrl=https://apexload.vault.azure.net",
		"--set", "workloadIdentity.controllerClientId=00000000-0000-0000-0000-000000000001",
		"--set", "workloadIdentity.generatorClientId=00000000-0000-0000-0000-000000000002",
	}
}

func helmChartWithReusedRestartValues(t *testing.T, restartValues map[string]any) string {
	t.Helper()
	source := filepath.Clean("../helm/apexload")
	destination := filepath.Join(t.TempDir(), "apexload")
	if err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return errors.New("Helm chart contains a non-regular file: " + path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, info.Mode().Perm())
	}); err != nil {
		t.Fatalf("copy Helm chart: %v", err)
	}

	valuesPath := filepath.Join(destination, "values.yaml")
	raw, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("read copied Helm values: %v", err)
	}
	var values map[string]any
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatalf("decode copied Helm values: %v", err)
	}
	if restartValues == nil {
		delete(values, "restartControls")
	} else {
		values["restartControls"] = restartValues
	}
	raw, err = yaml.Marshal(values)
	if err != nil {
		t.Fatalf("encode reused Helm values: %v", err)
	}
	if err := os.WriteFile(valuesPath, raw, 0o644); err != nil {
		t.Fatalf("write reused Helm values: %v", err)
	}
	return destination
}

func readRestartControlDocuments(t *testing.T, path string) []restartManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var docs []restartManifest
	for {
		var doc restartManifest
		err := decoder.Decode(&doc)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode %s: %v", path, err)
		}
		if doc.Kind != "" {
			docs = append(docs, doc)
		}
	}
	return docs
}

func findManifestKind(t *testing.T, docs []restartManifest, kind string) restartManifest {
	t.Helper()
	for _, doc := range docs {
		if doc.Kind == kind {
			return doc
		}
	}
	t.Fatalf("missing %s manifest", kind)
	return restartManifest{}
}

type restartManifest struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Rules []struct {
		APIGroups       []string `yaml:"apiGroups"`
		Resources       []string `yaml:"resources"`
		ResourceNames   []string `yaml:"resourceNames"`
		NonResourceURLs []string `yaml:"nonResourceURLs"`
		Verbs           []string `yaml:"verbs"`
	} `yaml:"rules"`
	Subjects []struct {
		APIGroup  string `yaml:"apiGroup"`
		Kind      string `yaml:"kind"`
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"subjects"`
	RoleRef struct {
		APIGroup string `yaml:"apiGroup"`
		Kind     string `yaml:"kind"`
		Name     string `yaml:"name"`
	} `yaml:"roleRef"`
}

type workloadManifest struct {
	Kind                         string `yaml:"kind"`
	AutomountServiceAccountToken *bool  `yaml:"automountServiceAccountToken"`
	Metadata                     struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Rules []struct {
		APIGroups       []string `yaml:"apiGroups"`
		Resources       []string `yaml:"resources"`
		ResourceNames   []string `yaml:"resourceNames"`
		NonResourceURLs []string `yaml:"nonResourceURLs"`
		Verbs           []string `yaml:"verbs"`
	} `yaml:"rules"`
	Subjects []struct {
		APIGroup  string `yaml:"apiGroup"`
		Kind      string `yaml:"kind"`
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"subjects"`
	RoleRef struct {
		APIGroup string `yaml:"apiGroup"`
		Kind     string `yaml:"kind"`
		Name     string `yaml:"name"`
	} `yaml:"roleRef"`
	Spec struct {
		Template struct {
			Spec struct {
				ServiceAccountName           string `yaml:"serviceAccountName"`
				AutomountServiceAccountToken *bool  `yaml:"automountServiceAccountToken"`
				Containers                   []struct {
					Name string `yaml:"name"`
					Env  []struct {
						Name      string `yaml:"name"`
						Value     string `yaml:"value"`
						ValueFrom struct {
							FieldRef struct {
								FieldPath string `yaml:"fieldPath"`
							} `yaml:"fieldRef"`
						} `yaml:"valueFrom"`
					} `yaml:"env"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

type workloadEnv struct {
	Name      string
	Value     string
	ValueFrom struct {
		FieldRef struct {
			FieldPath string
		}
	}
}

func decodeWorkloadDocuments(t *testing.T, raw []byte) []workloadManifest {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var docs []workloadManifest
	for {
		var doc workloadManifest
		if err := decoder.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return docs
			}
			t.Fatalf("decode rendered manifest: %v", err)
		}
		if doc.Kind != "" {
			docs = append(docs, doc)
		}
	}
}

func findWorkload(t *testing.T, docs []workloadManifest, kind, name string) workloadManifest {
	t.Helper()
	for _, doc := range docs {
		if doc.Kind == kind && doc.Metadata.Name == name {
			return doc
		}
	}
	t.Fatalf("missing %s %s", kind, name)
	return workloadManifest{}
}

func hasWorkload(docs []workloadManifest, kind, name string) bool {
	for _, doc := range docs {
		if doc.Kind == kind && doc.Metadata.Name == name {
			return true
		}
	}
	return false
}

func assertRenderedRestartRBAC(t *testing.T, docs []workloadManifest, serviceAccountName string) {
	t.Helper()
	role := findWorkload(t, docs, "Role", "apexload-restart-controls")
	got := map[string][]string{}
	for _, rule := range role.Rules {
		if len(rule.ResourceNames) != 0 || len(rule.NonResourceURLs) != 0 {
			t.Fatalf("rendered restart-control Role rule contains unexpected resourceNames/nonResourceURLs: %#v", rule)
		}
		key := strings.Join(rule.APIGroups, ",") + "/" + strings.Join(rule.Resources, ",")
		if _, duplicate := got[key]; duplicate {
			t.Fatalf("rendered restart-control Role contains duplicate rule for %q", key)
		}
		verbs := append([]string(nil), rule.Verbs...)
		sort.Strings(verbs)
		got[key] = verbs
	}
	want := map[string][]string{
		"/pods":             {"delete", "get", "list"},
		"/resourcequotas":   {"get", "list"},
		"apps/deployments":  {"get", "patch"},
		"apps/statefulsets": {"get", "patch"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered restart-control Role rules = %#v, want %#v", got, want)
	}

	binding := findWorkload(t, docs, "RoleBinding", "apexload-restart-controls")
	if binding.RoleRef.APIGroup != "rbac.authorization.k8s.io" || binding.RoleRef.Kind != "Role" || binding.RoleRef.Name != role.Metadata.Name {
		t.Fatalf("rendered restart-control RoleBinding roleRef = %#v", binding.RoleRef)
	}
	if len(binding.Subjects) != 1 {
		t.Fatalf("rendered restart-control RoleBinding subjects = %#v, want one controller ServiceAccount", binding.Subjects)
	}
	subject := binding.Subjects[0]
	if role.Metadata.Namespace == "" || binding.Metadata.Namespace == "" || subject.Namespace == "" ||
		subject.APIGroup != "" || subject.Kind != "ServiceAccount" || subject.Name != serviceAccountName ||
		subject.Namespace != binding.Metadata.Namespace || binding.Metadata.Namespace != role.Metadata.Namespace {
		t.Fatalf("rendered restart-control RoleBinding subject/namespace = subject %#v binding %q role %q", subject, binding.Metadata.Namespace, role.Metadata.Namespace)
	}
}

func containerEnv(doc workloadManifest, containerName string) map[string]workloadEnv {
	out := map[string]workloadEnv{}
	for _, container := range doc.Spec.Template.Spec.Containers {
		if container.Name != containerName {
			continue
		}
		for _, env := range container.Env {
			entry := workloadEnv{Name: env.Name, Value: env.Value}
			entry.ValueFrom.FieldRef.FieldPath = env.ValueFrom.FieldRef.FieldPath
			out[env.Name] = entry
		}
	}
	return out
}

func assertRenderedRestartControlsDisabled(t *testing.T, env map[string]workloadEnv) {
	t.Helper()
	if got, ok := env["APEXLOAD_K8S_RESTART_ENABLED"]; !ok || got.Value != "false" {
		t.Fatalf("disabled restart enablement env = %#v, want explicit false", got)
	}
	for _, name := range []string{
		"APEXLOAD_K8S_NAMESPACE",
		"APEXLOAD_K8S_CONTROLLER_DEPLOYMENT",
		"APEXLOAD_K8S_GENERATOR_STATEFULSET",
		"APEXLOAD_K8S_GENERATOR_SELECTOR",
		"APEXLOAD_K8S_RESTART_ALLOW_FORCE",
	} {
		if _, ok := env[name]; ok {
			t.Errorf("disabled restart controls still set %s", name)
		}
	}
}
