package qa_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// Provider qualification records (docs/qa/qualification/<provider>.json) say,
// per capability, whether a provider is qualified against a real
// non-production environment or only fixture-tested. Gate C requires a record
// for every provider the RCA evaluation corpus uses; every governed provider
// must have one.

const qualificationSchema = "apexload.provider_qualification.v1"

var qualificationOutcomes = map[string]string{
	"qualified":      "qualified",
	"fixture_tested": "fixture-tested",
	"failed":         "failed",
	"unsupported":    "unsupported",
}

type qualificationRecord struct {
	Schema              string                     `json:"schema"`
	Provider            string                     `json:"provider"`
	Pack                string                     `json:"pack"`
	Updated             string                     `json:"updated"`
	RequiredPermissions []string                   `json:"required_permissions"`
	Environments        []qualificationEnvironment `json:"environments"`
	Capabilities        []qualificationCapability  `json:"capabilities"`
}

type qualificationEnvironment struct {
	ID             string         `json:"id"`
	Product        string         `json:"product"`
	ProductVersion string         `json:"product_version"`
	APIVersions    []string       `json:"api_versions"`
	TenantType     string         `json:"tenant_type"`
	Permissions    []string       `json:"permissions"`
	ObservedLimits map[string]any `json:"observed_limits"`
	Date           string         `json:"date"`
	Evidence       []string       `json:"evidence"`
}

type qualificationCapability struct {
	Name         string   `json:"name"`
	Summary      string   `json:"summary"`
	Outcome      string   `json:"outcome"`
	Environments []string `json:"environments"`
	Evidence     []string `json:"evidence"`
	Remaining    string   `json:"remaining"`
}

var (
	qualificationName          = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	qualificationEnvironmentID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
)

func loadQualificationRecords(t *testing.T) map[string]qualificationRecord {
	t.Helper()
	paths, err := filepath.Glob("qualification/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no qualification records: %v", err)
	}
	records := map[string]qualificationRecord{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var record qualificationRecord
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if want := strings.TrimSuffix(filepath.Base(path), ".json"); record.Provider != want {
			t.Fatalf("%s names provider %q", path, record.Provider)
		}
		records[record.Provider] = record
	}
	return records
}

// qualificationEvidenceExists resolves "path" or "path#TestName" from the
// repository root. A named function must be a test in a _test.go file.
func qualificationEvidenceExists(ref string) error {
	file, name, _ := strings.Cut(ref, "#")
	if file == "" || path.IsAbs(file) || path.Clean(file) != file || strings.HasPrefix(file, "../") || strings.Contains(file, "\\") {
		return fmt.Errorf("evidence %s: not a repository path", ref)
	}
	if name != "" && (!strings.HasSuffix(file, "_test.go") || !strings.HasPrefix(name, "Test")) {
		return fmt.Errorf("evidence %s: a named function must be a test", ref)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(file)))
	if err != nil {
		return fmt.Errorf("evidence %s: %w", ref, err)
	}
	if name != "" && !bytes.Contains(data, []byte("\nfunc "+name+"(t *testing.T)")) {
		return fmt.Errorf("evidence %s: no test %s", ref, name)
	}
	return nil
}

// validDay accepts a date no later than tomorrow in UTC, so an author east of
// UTC can record their local today.
func validDay(day string) bool {
	parsed, err := time.Parse("2006-01-02", day)
	return err == nil && !parsed.After(time.Now().UTC().Add(24*time.Hour))
}

func TestEveryGovernedProviderHasAQualificationRecord(t *testing.T) {
	records := loadQualificationRecords(t)
	governed := map[string]bool{}
	for _, pack := range monitorprovider.Packs() {
		d, _ := monitorprovider.Lookup(pack)
		governed[d.ProviderName] = true
		record, ok := records[d.ProviderName]
		if !ok {
			t.Errorf("provider %s (pack %s) has no docs/qa/qualification/%s.json", d.ProviderName, pack, d.ProviderName)
			continue
		}
		if record.Pack != pack {
			t.Errorf("%s record names pack %q, want %q", d.ProviderName, record.Pack, pack)
		}
		// The registry's declared capabilities must each carry an outcome.
		required := []string{"metrics"}
		if d.SupportsDiagnostics {
			required = append(required, "diagnostics")
		}
		if d.SupportsTraceLookup {
			required = append(required, "trace_lookup")
		}
		if d.SupportsRunHeader {
			required = append(required, "run_header")
		}
		for _, name := range required {
			found := false
			for _, c := range record.Capabilities {
				found = found || c.Name == name
			}
			if !found {
				t.Errorf("%s record lacks the %s capability", d.ProviderName, name)
			}
		}
	}
	for provider := range records {
		if !governed[provider] {
			t.Errorf("qualification record for unknown provider %s", provider)
		}
	}
}

func TestQualificationRecordsAreComplete(t *testing.T) {
	for provider, record := range loadQualificationRecords(t) {
		if record.Schema != qualificationSchema || !validDay(record.Updated) || len(record.RequiredPermissions) == 0 {
			t.Errorf("%s: schema, updated date or required permissions missing", provider)
		}
		environments := map[string]bool{}
		for _, env := range record.Environments {
			if !qualificationEnvironmentID.MatchString(env.ID) || environments[env.ID] {
				t.Errorf("%s: invalid or duplicate environment %q", provider, env.ID)
			}
			environments[env.ID] = true
			// A qualification names the exact product, API and permissions on a
			// non-production tenant, with its date and evidence.
			if env.Product == "" || env.ProductVersion == "" || len(env.APIVersions) == 0 || !strings.Contains(env.TenantType, "non_production") || len(env.Permissions) == 0 || !validDay(env.Date) || len(env.Evidence) == 0 {
				t.Errorf("%s environment %s lacks product, version, API, non-production tenant, permissions, date or evidence", provider, env.ID)
			}
			for _, ref := range env.Evidence {
				if err := qualificationEvidenceExists(ref); err != nil {
					t.Errorf("%s environment %s: %v", provider, env.ID, err)
				}
			}
		}
		capabilities := map[string]bool{}
		for _, c := range record.Capabilities {
			if !qualificationName.MatchString(c.Name) || capabilities[c.Name] || c.Summary == "" {
				t.Errorf("%s: invalid, duplicate or undescribed capability %q", provider, c.Name)
			}
			capabilities[c.Name] = true
			if _, ok := qualificationOutcomes[c.Outcome]; !ok {
				t.Errorf("%s %s: unknown outcome %q", provider, c.Name, c.Outcome)
			}
			if c.Outcome == "qualified" {
				if len(c.Environments) == 0 {
					t.Errorf("%s %s: qualified without an environment", provider, c.Name)
				}
				for _, id := range c.Environments {
					if !environments[id] {
						t.Errorf("%s %s: unknown environment %q", provider, c.Name, id)
					}
				}
			} else if len(c.Environments) != 0 || c.Remaining == "" {
				t.Errorf("%s %s: an unqualified capability names no environment and says what remains", provider, c.Name)
			}
			if len(c.Evidence) == 0 {
				t.Errorf("%s %s: no evidence", provider, c.Name)
			}
			for _, ref := range c.Evidence {
				if err := qualificationEvidenceExists(ref); err != nil {
					t.Errorf("%s %s: %v", provider, c.Name, err)
				}
			}
		}
	}
}

const (
	qualificationTableDoc   = "../OBSERVABILITY_INTEGRATIONS.md"
	qualificationTableBegin = "<!-- provider-qualification:begin -->"
	qualificationTableEnd   = "<!-- provider-qualification:end -->"
)

// qualificationTable renders the per-capability statement the docs carry.
func qualificationTable(records map[string]qualificationRecord) string {
	providers := make([]string, 0, len(records))
	for provider := range records {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	var b strings.Builder
	b.WriteString("| Provider | Capability | Status | Qualified on |\n| --- | --- | --- | --- |\n")
	for _, provider := range providers {
		record := records[provider]
		label := provider
		if d, ok := monitorprovider.Lookup(record.Pack); ok {
			label = d.Label
		}
		environments := map[string]qualificationEnvironment{}
		for _, env := range record.Environments {
			environments[env.ID] = env
		}
		for _, c := range record.Capabilities {
			on := "—"
			if c.Outcome == "qualified" {
				var parts []string
				for _, id := range c.Environments {
					env := environments[id]
					parts = append(parts, fmt.Sprintf("%s %s (%s)", env.Product, env.ProductVersion, env.Date))
				}
				on = strings.Join(parts, "; ")
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", label, strings.ReplaceAll(c.Name, "_", " "), qualificationOutcomes[c.Outcome], on)
		}
	}
	return b.String()
}

func TestQualificationTableMatchesRecords(t *testing.T) {
	want := qualificationTable(loadQualificationRecords(t))
	data, err := os.ReadFile(qualificationTableDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start, end := strings.Index(doc, qualificationTableBegin), strings.Index(doc, qualificationTableEnd)
	if start < 0 || end < start {
		t.Fatalf("%s lacks the provider qualification markers", qualificationTableDoc)
	}
	got := strings.TrimPrefix(doc[start+len(qualificationTableBegin):end], "\n")
	if got == want {
		return
	}
	if os.Getenv("APEXLOAD_UPDATE_QUALIFICATION_TABLE") != "" {
		updated := doc[:start+len(qualificationTableBegin)] + "\n" + want + doc[end:]
		if err := os.WriteFile(qualificationTableDoc, []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("%s provider qualification table is stale; run APEXLOAD_UPDATE_QUALIFICATION_TABLE=1 go test ./docs/qa -run TestQualificationTableMatchesRecords", qualificationTableDoc)
}
