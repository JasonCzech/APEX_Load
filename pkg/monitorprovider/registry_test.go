package monitorprovider

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRegistryDescribesEveryGovernedProvider(t *testing.T) {
	for _, pack := range Packs() {
		d, ok := Lookup(pack)
		if !ok || d.Pack != pack || d.ProviderName == "" || d.Label == "" || d.CapabilityPack == "" || d.ContractUnavailableReason == "" {
			t.Fatalf("incomplete descriptor %+v", d)
		}
		if d.Scheduling != SchedulingPolled && d.Scheduling != SchedulingWindowed && d.Scheduling != SchedulingCursor {
			t.Fatalf("%s scheduling %q", pack, d.Scheduling)
		}
		if d.Observation != ObservationInterval && d.Observation != ObservationCumulative && d.Observation != ObservationSnapshot {
			t.Fatalf("%s observation %q", pack, d.Observation)
		}
		if d.MinIntervalSeconds <= 0 || d.DefaultIntervalSeconds < d.MinIntervalSeconds || d.MaxIntervalSeconds < d.DefaultIntervalSeconds {
			t.Fatalf("%s interval bounds %+v", pack, d)
		}
		if byName, ok := ByProviderName(d.ProviderName); !ok || byName.Pack != pack {
			t.Fatalf("%s provider name %q does not map back", pack, d.ProviderName)
		}
	}
	if Canonical("prometheus") || KnownProviderName("prometheus-query") {
		t.Fatal("the legacy prometheus pack and the query pack's name must stay distinct")
	}
}

// Provider dispatch goes through the registry: no other Go source may compare
// a value against a governed provider's literal pack or provider name.
func TestNoProviderNameComparisonsOutsideRegistry(t *testing.T) {
	names := `(appdynamics|dynatrace|prometheus-query|elastic-query)`
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(==|!=)\s*"` + names + `"`),
		regexp.MustCompile(`"` + names + `"\s*(==|!=)`),
		regexp.MustCompile(`\bcase\s[^:]*"` + names + `"`),
		// Provider names on points, diagnostics and rules, including Prometheus.
		regexp.MustCompile(`(Provider|\bm)(\.Name)?\s*(==|!=)\s*"prometheus"`),
	}
	root := filepath.Join("..")
	var findings []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "apidocs" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "registry.go" && filepath.Base(filepath.Dir(path)) == "monitorprovider" {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
		for line := 1; scanner.Scan(); line++ {
			text := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(text, "//") {
				continue
			}
			for _, pattern := range patterns {
				if pattern.MatchString(text) {
					findings = append(findings, path+":"+itoa(line)+": "+text)
					break
				}
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Fatalf("%d provider-name comparisons outside the registry; use monitorprovider constants or a Descriptor:\n%s", len(findings), strings.Join(findings, "\n"))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

func TestByCapabilityPackMapsContractsToProviders(t *testing.T) {
	for _, pack := range Packs() {
		d, _ := Lookup(pack)
		if byContract, ok := ByCapabilityPack(d.CapabilityPack); !ok || byContract.Pack != pack || !IsCapabilityPack(d.CapabilityPack) {
			t.Fatalf("%s capability %q does not map back", pack, d.CapabilityPack)
		}
	}
	if _, ok := ByCapabilityPack("prometheus-scrape.v2"); ok {
		t.Fatal("the scrape contract is not a governed provider capability")
	}
}

func TestRetiredProviderKeepsHistoricalIdentityWithoutActiveDiscovery(t *testing.T) {
	d, ok := Lookup(PackAppDynamics)
	if !ok || !Canonical(PackAppDynamics) || !KnownProviderName(ProviderAppDynamics) {
		t.Fatal("historical identity lost")
	}
	if byName, ok := ByProviderName(ProviderAppDynamics); !ok || byName.Pack != d.Pack {
		t.Fatal("historical provider cannot be decoded")
	}
	for _, pack := range Packs() {
		if pack == PackAppDynamics {
			t.Fatal("retired provider advertised")
		}
	}
	if _, ok := ByCapabilityPack(d.CapabilityPack); ok || IsCapabilityPack(d.CapabilityPack) {
		t.Fatal("retired provider capability accepted")
	}
	for _, pack := range []string{PackDynatrace, PackPrometheusQuery, PackElasticQuery} {
		active, _ := Lookup(pack)
		if _, ok := ByCapabilityPack(active.CapabilityPack); !ok {
			t.Fatalf("supported provider %s removed", pack)
		}
	}
}
