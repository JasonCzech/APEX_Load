package k8s_test

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type http3RenderedEnv struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type http3RenderedPort struct {
	Protocol string `yaml:"protocol"`
	Port     int    `yaml:"port"`
}

type http3RenderedPeer struct {
	IPBlock *struct {
		CIDR   string   `yaml:"cidr"`
		Except []string `yaml:"except"`
	} `yaml:"ipBlock"`
	PodSelector *struct {
		MatchLabels map[string]string `yaml:"matchLabels"`
	} `yaml:"podSelector"`
	NamespaceSelector *struct {
		MatchLabels map[string]string `yaml:"matchLabels"`
	} `yaml:"namespaceSelector"`
}

type http3RenderedRule struct {
	To    []http3RenderedPeer `yaml:"to"`
	Ports []http3RenderedPort `yaml:"ports"`
}

type http3RenderedDocument struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		PolicyTypes []string            `yaml:"policyTypes"`
		Egress      []http3RenderedRule `yaml:"egress"`
		Template    struct {
			Spec struct {
				Containers []struct {
					Name string             `yaml:"name"`
					Env  []http3RenderedEnv `yaml:"env"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func renderHTTP3Chart(t *testing.T, values ...string) ([]http3RenderedDocument, string, error) {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required")
	}
	args := archiveStagingHelmArgs()
	for _, value := range values {
		// A json: prefix sets a JSON value, such as an empty list.
		if raw, ok := strings.CutPrefix(value, "json:"); ok {
			args = append(args, "--set-json", raw)
			continue
		}
		args = append(args, "--set", value)
	}
	out, err := exec.Command(helm, args...).CombinedOutput()
	if err != nil {
		return nil, string(out), err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(out))
	var docs []http3RenderedDocument
	for {
		var doc http3RenderedDocument
		if err := decoder.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered chart: %v", err)
		}
		if doc.Kind != "" {
			docs = append(docs, doc)
		}
	}
	return docs, string(out), nil
}

// http3ContainerEnv returns the env of the named workload's main container.
func http3ContainerEnv(t *testing.T, docs []http3RenderedDocument, kind, name, container string) map[string]string {
	t.Helper()
	for _, doc := range docs {
		if doc.Kind != kind || doc.Metadata.Name != name {
			continue
		}
		for _, c := range doc.Spec.Template.Spec.Containers {
			if c.Name == container {
				env := make(map[string]string, len(c.Env))
				for _, item := range c.Env {
					env[item.Name] = item.Value
				}
				return env
			}
		}
	}
	t.Fatalf("%s %s container %s not rendered", kind, name, container)
	return nil
}

func http3Policy(docs []http3RenderedDocument, name string) *http3RenderedDocument {
	for index := range docs {
		if docs[index].Kind == "NetworkPolicy" && docs[index].Metadata.Name == name {
			return &docs[index]
		}
	}
	return nil
}

func http3RuleHasPort(rule http3RenderedRule, protocol string, port int) bool {
	for _, candidate := range rule.Ports {
		if candidate.Protocol == protocol && candidate.Port == port {
			return true
		}
	}
	return false
}

func TestHelmHTTP3SettingsReachTheControllerAndEveryGenerator(t *testing.T) {
	docs, out, err := renderHTTP3Chart(t)
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
	controller := http3ContainerEnv(t, docs, "Deployment", "apexload-controller", "controller")
	generator := http3ContainerEnv(t, docs, "StatefulSet", "apexload-generator", "generator")
	for name, env := range map[string]map[string]string{"controller": controller, "generator": generator} {
		if env["APEXLOAD_HTTP3"] != "false" || env["APEXLOAD_UDP_COUNTER_SCOPE"] != "pod" {
			t.Fatalf("%s default env = %v", name, env)
		}
		for key := range env {
			if strings.HasPrefix(key, "APEXLOAD_QUIC_") {
				t.Fatalf("%s sets %s although every QUIC limit keeps its default", name, key)
			}
		}
	}
	if controller["APEXLOAD_HTTP3_PREFLIGHT"] != "report" {
		t.Fatalf("controller preflight = %q, want report", controller["APEXLOAD_HTTP3_PREFLIGHT"])
	}
	if _, ok := generator["APEXLOAD_HTTP3_PREFLIGHT"]; ok {
		t.Fatal("the preflight policy is a controller setting")
	}
	if http3Policy(docs, "apexload-generator-egress") != nil || http3Policy(docs, "apexload-controller-egress") != nil {
		t.Fatal("egress profiles must be opt-in")
	}

	docs, out, err = renderHTTP3Chart(t, "http3.enabled=true", "http3.preflight=block", "http3.quic.maxStreamReceiveWindow=8388608",
		"http3.quic.initialPacketSize=1350", "http3.quic.disablePathMTUDiscovery=true")
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
	controller = http3ContainerEnv(t, docs, "Deployment", "apexload-controller", "controller")
	generator = http3ContainerEnv(t, docs, "StatefulSet", "apexload-generator", "generator")
	for name, env := range map[string]map[string]string{"controller": controller, "generator": generator} {
		if env["APEXLOAD_HTTP3"] != "true" || env["APEXLOAD_QUIC_MAX_STREAM_RECEIVE_WINDOW"] != "8388608" ||
			env["APEXLOAD_QUIC_INITIAL_PACKET_SIZE"] != "1350" || env["APEXLOAD_QUIC_DISABLE_PATH_MTU_DISCOVERY"] != "true" {
			t.Fatalf("%s env = %v", name, env)
		}
		if _, ok := env["APEXLOAD_QUIC_INITIAL_STREAM_RECEIVE_WINDOW"]; ok {
			t.Fatalf("%s sets a QUIC limit left at its default", name)
		}
	}
	if controller["APEXLOAD_HTTP3_PREFLIGHT"] != "block" {
		t.Fatalf("controller preflight = %q", controller["APEXLOAD_HTTP3_PREFLIGHT"])
	}
}

func TestHelmEgressProfilesOpenUDP443ForGeneratorsByDefault(t *testing.T) {
	docs, out, err := renderHTTP3Chart(t, "networkPolicy.egress.enabled=true", "networkPolicy.egress.targets.exceptCidrs={169.254.169.254/32,fd00::/8}",
		"networkPolicy.egress.controllerExtraRules[0].to[0].ipBlock.cidr=10.20.0.0/24", "networkPolicy.egress.controllerExtraRules[0].ports[0].port=5432")
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
	generator := http3Policy(docs, "apexload-generator-egress")
	controller := http3Policy(docs, "apexload-controller-egress")
	if generator == nil || controller == nil {
		t.Fatalf("egress profiles not rendered:\n%s", out)
	}
	for _, policy := range []*http3RenderedDocument{generator, controller} {
		if len(policy.Spec.PolicyTypes) != 1 || policy.Spec.PolicyTypes[0] != "Egress" {
			t.Fatalf("%s policy types = %v", policy.Metadata.Name, policy.Spec.PolicyTypes)
		}
		dns := policy.Spec.Egress[0]
		if !http3RuleHasPort(dns, "UDP", 53) || !http3RuleHasPort(dns, "TCP", 53) || dns.To[0].NamespaceSelector == nil || dns.To[0].PodSelector.MatchLabels["k8s-app"] != "kube-dns" {
			t.Fatalf("%s DNS rule = %+v", policy.Metadata.Name, dns)
		}
		targets := policy.Spec.Egress[1]
		if !http3RuleHasPort(targets, "UDP", 443) || !http3RuleHasPort(targets, "TCP", 443) || !http3RuleHasPort(targets, "TCP", 80) {
			t.Fatalf("%s target ports = %+v", policy.Metadata.Name, targets.Ports)
		}
		if len(targets.To) != 2 || targets.To[0].IPBlock.CIDR != "0.0.0.0/0" || strings.Join(targets.To[0].IPBlock.Except, ",") != "169.254.169.254/32" ||
			targets.To[1].IPBlock.CIDR != "::/0" || strings.Join(targets.To[1].IPBlock.Except, ",") != "fd00::/8" {
			t.Fatalf("%s target peers = %+v", policy.Metadata.Name, targets.To)
		}
	}
	if len(generator.Spec.Egress) != 2 {
		t.Fatalf("generator egress = %+v, want DNS and targets only", generator.Spec.Egress)
	}
	if len(controller.Spec.Egress) != 5 || !http3RuleHasPort(controller.Spec.Egress[2], "TCP", 9090) ||
		controller.Spec.Egress[2].To[0].PodSelector.MatchLabels["app.kubernetes.io/name"] != "apexload-generator" ||
		controller.Spec.Egress[4].To[0].IPBlock.CIDR != "10.20.0.0/24" {
		t.Fatalf("controller egress = %+v", controller.Spec.Egress)
	}

	docs, out, err = renderHTTP3Chart(t, "networkPolicy.egress.enabled=true", "json:networkPolicy.egress.targets.udpPorts=[]")
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
	for _, port := range http3Policy(docs, "apexload-generator-egress").Spec.Egress[1].Ports {
		if port.Protocol == "UDP" {
			t.Fatalf("UDP stays closed when udpPorts is emptied: %+v", port)
		}
	}
}

func TestHelmRejectsInvalidHTTP3AndEgressValues(t *testing.T) {
	for _, tc := range []struct {
		name, failure string
		values        []string
	}{
		{name: "preflight policy", values: []string{"http3.preflight=strict"}, failure: "http3/preflight"},
		{name: "window below a packet", values: []string{"http3.quic.maxStreamReceiveWindow=100"}, failure: "maxStreamReceiveWindow"},
		{name: "packet size", values: []string{"http3.quic.initialPacketSize=9000"}, failure: "initialPacketSize"},
		{name: "UDP port", values: []string{"networkPolicy.egress.targets.udpPorts={70000}"}, failure: "udpPorts"},
		{name: "CIDR", values: []string{"networkPolicy.egress.targets.cidrs={internet}"}, failure: "cidrs"},
		{name: "egress without network policies", values: []string{"networkPolicy.enabled=false", "networkPolicy.egress.enabled=true"}, failure: "networkPolicy.egress.enabled requires networkPolicy.enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := renderHTTP3Chart(t, tc.values...)
			if err == nil || !strings.Contains(out, tc.failure) {
				t.Fatalf("error=%v output=%s", err, out)
			}
		})
	}
}
