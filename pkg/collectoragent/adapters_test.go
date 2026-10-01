package collectoragent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAdapterTargetsStrictDocumentAndBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adapters.json")
	for _, test := range []struct {
		body  string
		valid bool
	}{
		{`[{"pack":"otel-metrics","destination":"otlp://app"}]`, true},
		{`[{"pack":"otel-metrics","unknown":true}]`, false},
		{`[] []`, false}, {`{`, false}, {string(make([]byte, (64<<10)+1)), false},
	} {
		if err := os.WriteFile(path, []byte(test.body), 0600); err != nil {
			t.Fatal(err)
		}
		targets, err := LoadAdapterTargets(path)
		if (err == nil) != test.valid {
			t.Fatalf("valid=%v: %v %+v", test.valid, err, targets)
		}
	}
}

func TestAdapterApprovalRejectsCredentialDestinationsAndUnknownPacks(t *testing.T) {
	for _, target := range []AdapterTarget{
		{Pack: "shell", Destination: "ssh://host"},
		{Pack: "ssh-linux", Destination: "ssh://user:password@host"},
		{Pack: "ssh-linux", Destination: "ssh://host/command"},
		{Pack: "ssh-linux", Destination: "ssh://host", HostKey: "invalid"},
		{Pack: "otel-metrics", Destination: "otlp://app"},
		{Pack: "jdbc", Destination: "postgresql://host/db", JavaExecutable: "java", HelperClassPath: "relative", QueryPack: "connection-health-v1"},
	} {
		if err := validateAdapterTargets(Config{AdapterTargets: []AdapterTarget{target}}); err == nil {
			t.Fatalf("unsafe approval accepted: %s", target.Pack)
		}
	}
}
