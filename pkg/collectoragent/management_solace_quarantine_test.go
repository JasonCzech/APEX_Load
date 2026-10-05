package collectoragent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/analysis"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func solaceTestIntent(origin string) solaceManagementSessionIntent {
	grant := managementTestGrant("instance")
	return solaceManagementIntent(origin, grant)
}
func TestSolaceManagementQuarantineRestoresUnknownSourceWithoutCookie(t *testing.T) {
	path := filepath.Join(t.TempDir(), solaceManagementQuarantineFile)
	q, err := openSolaceManagementQuarantine(path, newCollectorSpoolBudget())
	if err != nil {
		t.Fatal(err)
	}
	intent := solaceTestIntent("https://broker.example.test:943")
	if err = q.begin(intent); err != nil {
		t.Fatal(err)
	}
	fresh, err := openSolaceManagementQuarantine(path, newCollectorSpoolBudget())
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.blocked(intent.Origin) {
		t.Fatal("restart forgot unresolved provider session")
	}
	changed := intent
	changed.PollID = "fresh-poll"
	changed.CollectorInstanceID = "fresh-instance"
	changed.SourceRevision++
	if fresh.begin(changed) == nil {
		t.Fatal("new source revision/instance bypassed origin quarantine")
	}
	forged := intent
	forged.IdentityHash = strings.Repeat("f", 64)
	if fresh.auditedProviderSessionRelease(forged) == nil || !fresh.blocked(intent.Origin) {
		t.Fatal("mismatching audit released quarantine")
	}
	if err = fresh.auditedProviderSessionRelease(intent); err != nil {
		t.Fatal(err)
	}
	after, err := openSolaceManagementQuarantine(path, newCollectorSpoolBudget())
	if err != nil || after.blocked(intent.Origin) {
		t.Fatal("confirmed durable provider proof did not survive reload", err)
	}
}
func TestSolaceManagementQuarantineRejectsCorruptOrUnprotectedRecords(t *testing.T) {
	intent := solaceTestIntent("https://broker.example.test:943")
	valid, _ := json.Marshal(solaceManagementQuarantineDocument{Version: 1, Intents: map[string]solaceManagementSessionIntent{intent.Origin: intent}})
	for _, test := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"truncated", valid[:len(valid)-1], 0600},
		{"unknown fields", []byte(`{"version":1,"intents":{},"cookie":"secret"}`), 0600},
		{"duplicate version", []byte(`{"version":1,"version":1,"intents":{}}`), 0600},
		{"missing records", []byte(`{"version":1}`), 0600},
		{"foreign version", []byte(`{"version":2,"intents":{}}`), 0600},
		{"public document", valid, 0644},
		{"oversize", []byte(strings.Repeat(" ", solaceManagementQuarantineBytes+1)), 0600},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), solaceManagementQuarantineFile)
			if err := os.WriteFile(path, test.data, test.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := openSolaceManagementQuarantine(path, newCollectorSpoolBudget()); err == nil {
				t.Fatal("invalid quarantine was silently discarded")
			}
		})
	}
	path := filepath.Join(t.TempDir(), solaceManagementQuarantineFile)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, valid, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := openSolaceManagementQuarantine(path, newCollectorSpoolBudget()); err == nil {
		t.Fatal("quarantine followed symlink")
	}
}
func TestSolaceManagementQuarantineCountsQuotaAndRetainsInstalledFailure(t *testing.T) {
	budget := newCollectorSpoolBudget()
	path := filepath.Join(t.TempDir(), solaceManagementQuarantineFile)
	q, err := openSolaceManagementQuarantine(path, budget)
	if err != nil {
		t.Fatal(err)
	}
	intent := solaceTestIntent("https://broker.example.test:943")
	budget.write = func(path string, data []byte) (bool, error) {
		installed, err := writeCollectorSpool(path, data)
		if err != nil {
			return installed, err
		}
		return true, errors.New("controlled directory fsync failed")
	}
	if q.begin(intent) == nil || !q.blocked(intent.Origin) || !q.poisoned {
		t.Fatal("installed persistence failure allowed authentication")
	}
	budget.mu.Lock()
	charged := budget.fileBytes[solaceManagementQuarantineBudgetKey]
	budget.mu.Unlock()
	if charged == 0 {
		t.Fatal("partially installed document was uncharged")
	}
	restored, err := openSolaceManagementQuarantine(path, newCollectorSpoolBudget())
	if err != nil || !restored.blocked(intent.Origin) {
		t.Fatal("installed failure disappeared across restart", err)
	}
	budget.write = writeCollectorSpool
	if q.auditedProviderSessionRelease(intent) != nil {
		t.Fatal("explicit provider release could not recover installed failure")
	}
	budget.mu.Lock()
	budget.fileBytes["other"] = maxCollectorSpoolBytes
	budget.mu.Unlock()
	second := intent
	second.Origin = "https://second.example.test:943"
	if q.begin(second) == nil || !q.blocked(second.Origin) {
		t.Fatal("combined quota failure permitted Basic admission")
	}
}
func TestSolaceManagementQuarantineRecordCapacityHasNoOriginEviction(t *testing.T) {
	budget := newCollectorSpoolBudget()
	budget.write = func(string, []byte) (bool, error) { return true, nil }
	q, err := openSolaceManagementQuarantine(filepath.Join(t.TempDir(), solaceManagementQuarantineFile), budget)
	if err != nil {
		t.Fatal(err)
	}
	first := solaceTestIntent("https://first.example.test:943")
	for i := 0; i < solaceManagementQuarantineRecords; i++ {
		entry := first
		entry.Origin = "https://broker" + strconv.Itoa(i) + ".example.test:943"
		if err = q.begin(entry); err != nil {
			t.Fatal("bounded record capacity ended early", err)
		}
	}
	if q.begin(first) == nil || !q.blocked("https://broker0.example.test:943") {
		t.Fatal("capacity pressure evicted unresolved origin")
	}
}
func TestSolaceManagementStartupLockSurvivesControllerAndCollectorChanges(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	target := solaceConfigTestTarget(t)
	cfg := Config{SpoolDirectory: directory, ControllerURL: "http://old-controller", CollectorID: "old", Packs: []string{"solace-semp"}, AdapterTargets: []AdapterTarget{{Pack: "solace-semp", Management: &target}}}
	first := &Agent{cfg: cfg, spoolBudget: newCollectorSpoolBudget()}
	first.management = newManagementScheduler(first, "old-instance")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := first.lockSolaceManagementQuarantine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var unlocked bool
	defer func() {
		if !unlocked {
			unlock()
		}
	}()
	state := first.management.origins.origins[managementOriginKey(target.Endpoints[0])].solace
	intent := solaceTestIntent(target.Endpoints[0])
	if err = state.quarantine.begin(intent); err != nil {
		t.Fatal(err)
	}
	cfg.ControllerURL = "http://new-controller"
	cfg.CollectorID = "new"
	cfg.Packs = nil
	second := &Agent{cfg: cfg, spoolBudget: newCollectorSpoolBudget()}
	second.management = newManagementScheduler(second, "new-instance")
	queued, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err = second.lockSolaceManagementQuarantine(queued)
	stop()
	if err == nil {
		t.Fatal("fresh agent ignored shared quarantine lock")
	}
	unlock()
	unlocked = true
	release, err := second.lockSolaceManagementQuarantine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	restored := second.management.origins.origins[managementOriginKey(target.Endpoints[0])].solace
	if restored == nil || !restored.quarantine.blocked(intent.Origin) {
		t.Fatal("disabled pack/controller or collector change discarded unresolved intent")
	}
	second.spoolBudget.mu.Lock()
	bytes := second.spoolBudget.fileBytes[solaceManagementQuarantineBudgetKey]
	second.spoolBudget.mu.Unlock()
	if bytes == 0 {
		t.Fatal("disabled pack omitted quarantine quota")
	}
}
func TestSolaceManagementStartupRequiresProtectedDurableSpool(t *testing.T) {
	target := solaceConfigTestTarget(t)
	cfg := Config{Packs: []string{"solace-semp"}, AdapterTargets: []AdapterTarget{{Pack: "solace-semp", Management: &target}}}
	for _, directory := range []string{"", "relative", "/private/tmp/../tmp/quarantine"} {
		cfg.SpoolDirectory = directory
		if validateSolaceManagementSpool(cfg) == nil {
			t.Fatal("selected Solace pack accepted missing or noncanonical spool")
		}
	}
	cfg.SpoolDirectory = t.TempDir()
	if err := os.Chmod(cfg.SpoolDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	agent := &Agent{cfg: cfg, spoolBudget: newCollectorSpoolBudget()}
	if _, err := agent.lockSolaceManagementQuarantine(context.Background()); err == nil {
		t.Fatal("Session quarantine opened public spool directory")
	}
}
func TestSolaceManagementRotatesStableApprovedEntityVisitOrder(t *testing.T) {
	entities := []analysis.ManagementEntity{{Solace: &analysis.SolaceManagementEntity{VPN: "orders", Queue: "z"}}, {Solace: &analysis.SolaceManagementEntity{VPN: "orders", Queue: "a"}}, {Solace: &analysis.SolaceManagementEntity{VPN: "orders", TopicEndpoint: "events"}}}
	first, second := solaceManagementEntityOrder(entities, "1"), solaceManagementEntityOrder(entities, "2")
	seen := map[int]bool{}
	for _, i := range first {
		if i < 0 || i >= len(entities) || seen[i] {
			t.Fatal("rotation escaped or duplicated approved entity")
		}
		seen[i] = true
	}
	if len(first) != len(entities) || first[0] == second[0] {
		t.Fatal("fixed slow prefix can starve later inventory")
	}
}
