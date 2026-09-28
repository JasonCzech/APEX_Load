package k8s_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func javaBrokerDocuments(t *testing.T) []map[string]any {
	t.Helper()
	var data []byte
	if kubectl, e := exec.LookPath("kubectl"); e == nil {
		out, e := exec.Command(kubectl, "kustomize", "java-host-broker").CombinedOutput()
		if e != nil {
			t.Fatalf("render: %v\n%s", e, out)
		}
		data = out
	} else {
		// Security assertions still run on CI workers without kubectl.
		var k struct {
			Resources []string `yaml:"resources"`
		}
		raw, e := os.ReadFile("java-host-broker/kustomization.yaml")
		if e != nil || yaml.Unmarshal(raw, &k) != nil {
			t.Fatal("cannot read component")
		}
		for _, name := range k.Resources {
			raw, e := os.ReadFile(filepath.Join("java-host-broker", name))
			if e != nil {
				t.Fatal(e)
			}
			data = append(data, []byte("\n---\n")...)
			data = append(data, raw...)
		}
	}
	var docs []map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc map[string]any
		e := dec.Decode(&doc)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if len(doc) > 0 {
			docs = append(docs, doc)
		}
	}
	return docs
}
func javaBrokerObject(t *testing.T, docs []map[string]any, kind, namespace, name string) map[string]any {
	t.Helper()
	for _, doc := range docs {
		meta := doc["metadata"].(map[string]any)
		if doc["kind"] == kind && meta["name"] == name && (namespace == "" || meta["namespace"] == namespace) {
			return doc
		}
	}
	t.Fatalf("missing %s %s/%s", kind, namespace, name)
	return nil
}

func TestJavaBrokerScaffoldKeepsAuthoritySeparateAndDisabled(t *testing.T) {
	docs := javaBrokerDocuments(t)
	for _, namespace := range []string{"apexload-java-broker", "apexload-java-execution"} {
		obj := javaBrokerObject(t, docs, "Namespace", "", namespace)
		labels := obj["metadata"].(map[string]any)["labels"].(map[string]any)
		if labels["pod-security.kubernetes.io/enforce"] != "restricted" {
			t.Fatal("unrestricted namespace")
		}
	}
	for _, doc := range docs {
		if doc["kind"] == "ClusterRole" || doc["kind"] == "ClusterRoleBinding" || doc["kind"] == "Secret" || doc["kind"] == "PersistentVolumeClaim" || doc["kind"] == "ScaledObject" {
			t.Fatal("scaffold acquired global authority, embedded credentials, disposable state or execution autoscaling", doc["kind"])
		}
	}
	role := javaBrokerObject(t, docs, "Role", "apexload-java-execution", "java-hosts")
	rules := role["rules"].([]any)
	if len(rules) != 1 {
		t.Fatal("extra privileges")
	}
	rule := rules[0].(map[string]any)
	if !reflect.DeepEqual(rule["apiGroups"], []any{""}) || !reflect.DeepEqual(rule["resources"], []any{"pods", "secrets"}) || !reflect.DeepEqual(rule["verbs"], []any{"get", "create", "delete"}) {
		t.Fatal("unexpected execution permissions", rule)
	}
	binding := javaBrokerObject(t, docs, "RoleBinding", "apexload-java-execution", "java-hosts")
	subjects := binding["subjects"].([]any)
	if len(subjects) != 1 {
		t.Fatal("multiple privileged subjects")
	}
	subject := subjects[0].(map[string]any)
	if subject["name"] != "java-broker" || subject["namespace"] != "apexload-java-broker" {
		t.Fatal("execution pod received API authority")
	}
	for _, pair := range [][2]string{{"apexload-java-broker", "java-broker"}, {"apexload-java-execution", "java-execution"}} {
		sa := javaBrokerObject(t, docs, "ServiceAccount", pair[0], pair[1])
		if sa["automountServiceAccountToken"] != false {
			t.Fatal("automatic credentials enabled")
		}
	}
	deployment := javaBrokerObject(t, docs, "Deployment", "apexload-java-broker", "java-host-broker")
	spec := deployment["spec"].(map[string]any)
	if spec["replicas"] != 0 || spec["strategy"].(map[string]any)["type"] != "Recreate" {
		t.Fatal("broker automatically activated or rolled across live epochs")
	}
	pod := spec["template"].(map[string]any)["spec"].(map[string]any)
	if pod["automountServiceAccountToken"] != false || pod["enableServiceLinks"] != false {
		t.Fatal("unbounded credentials/service environment")
	}
	containers := pod["containers"].([]any)
	if len(containers) != 1 {
		t.Fatal("extra containers")
	}
	container := containers[0].(map[string]any)
	security := container["securityContext"].(map[string]any)
	if security["readOnlyRootFilesystem"] != true || security["allowPrivilegeEscalation"] != false || !reflect.DeepEqual(security["capabilities"].(map[string]any)["drop"], []any{"ALL"}) {
		t.Fatal("unsafe broker process")
	}
	if !strings.Contains(container["image"].(string), "@sha256:") || !reflect.DeepEqual(container["args"], []any{"--config", "/config/broker.json"}) {
		t.Fatal("mutable image or automatic authority initialization")
	}
	state := false
	token := false
	for _, raw := range pod["volumes"].([]any) {
		v := raw.(map[string]any)
		if _, ok := v["hostPath"]; ok {
			t.Fatal("host filesystem exposed")
		}
		if v["name"] == "state" {
			state = v["persistentVolumeClaim"].(map[string]any)["claimName"] == "java-broker-state"
		}
		if v["name"] == "kubernetes" {
			sources := v["projected"].(map[string]any)["sources"].([]any)
			for _, source := range sources {
				if projection, ok := source.(map[string]any)["serviceAccountToken"]; ok {
					token = projection.(map[string]any)["expirationSeconds"] == 3600
				}
			}
		}
	}
	if !state || !token {
		t.Fatal("missing durable state or bounded projected broker token")
	}
}

func TestJavaBrokerScaffoldDefaultDenyAndSeparateExecutionQuota(t *testing.T) {
	docs := javaBrokerDocuments(t)
	for _, ns := range []string{"apexload-java-broker", "apexload-java-execution"} {
		policy := javaBrokerObject(t, docs, "NetworkPolicy", ns, "default-deny")["spec"].(map[string]any)
		if len(policy["podSelector"].(map[string]any)) != 0 || !reflect.DeepEqual(policy["policyTypes"], []any{"Ingress", "Egress"}) || policy["ingress"] != nil || policy["egress"] != nil {
			t.Fatal("default policy is not closed")
		}
	}
	for _, doc := range docs {
		if doc["kind"] != "NetworkPolicy" {
			continue
		}
		spec := doc["spec"].(map[string]any)
		for _, lane := range []string{"ingress", "egress"} {
			rules, _ := spec[lane].([]any)
			for _, raw := range rules {
				rule := raw.(map[string]any)
				if len(rule["ports"].([]any)) == 0 {
					t.Fatal("all-port exception")
				}
				direction := "to"
				if lane == "ingress" {
					direction = "from"
				}
				for _, peer := range rule[direction].([]any) {
					p := peer.(map[string]any)
					if p["ipBlock"] != nil || p["namespaceSelector"] == nil || p["podSelector"] == nil {
						t.Fatal("broad or unreviewed network exception")
					}
				}
			}
		}
	}
	quota := javaBrokerObject(t, docs, "ResourceQuota", "apexload-java-execution", "java-hosts")["spec"].(map[string]any)["hard"].(map[string]any)
	if quota["pods"] != "25" || quota["count/secrets"] != "100" || quota["requests.memory"] != "8Gi" {
		t.Fatal("execution quota drift", quota)
	}
	// The standalone scaffold must not change existing generator/KEDA overlays.
	for _, overlay := range []string{"aks", "aks-java-vuser", "local", "onprem"} {
		data, e := os.ReadFile(filepath.Join(overlay, "kustomization.yaml"))
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(data, []byte("java-host-broker")) {
			t.Fatal("experimental broker enabled in", overlay)
		}
	}
}

func TestJavaBrokerBootstrapIsExplicitAndCredentialFree(t *testing.T) {
	data, e := os.ReadFile("java-host-broker/bootstrap-job.optional.yaml")
	if e != nil {
		t.Fatal(e)
	}
	var job map[string]any
	if e = yaml.Unmarshal(data, &job); e != nil {
		t.Fatal(e)
	}
	spec := job["spec"].(map[string]any)
	pod := spec["template"].(map[string]any)["spec"].(map[string]any)
	if spec["backoffLimit"] != 0 || spec["activeDeadlineSeconds"] != 60 || pod["restartPolicy"] != "Never" || pod["automountServiceAccountToken"] != false {
		t.Fatal("unbounded bootstrap")
	}
	container := pod["containers"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(container["args"], []any{"--config", "/config/broker.json", "--initialize-store-only"}) {
		t.Fatal("bootstrap starts a broker")
	}
	volumes := pod["volumes"].([]any)
	if len(volumes) != 2 {
		t.Fatal("bootstrap received unnecessary mounts")
	}
	for _, v := range volumes {
		name := v.(map[string]any)["name"]
		if name != "state" && name != "config" {
			t.Fatal("bootstrap credentials")
		}
	}
	for _, doc := range javaBrokerDocuments(t) {
		if doc["kind"] == "Job" {
			t.Fatal("bootstrap enabled automatically")
		}
	}
}
