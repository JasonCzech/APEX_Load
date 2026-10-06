package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fixtureHARJoinedRetirement(t *testing.T, dir string) (*harCaptureOwner, *harNamespace, harRetirementRecord) {
	t.Helper()
	o := fixtureHAROwner(t, dir)
	writeOwnedHARFixture(t, o)
	o.startClose()
	<-o.closeDone
	if o.closeErr != nil {
		t.Fatal(o.closeErr)
	}
	n, err := lockHARNamespace(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := persistentHARIdentity(o.identity)
	if err != nil {
		t.Fatal(err)
	}
	r := harRetirementRecord{Kind: "working", Generation: o.generation, Identity: id}
	if err := registerHARRetirement(n.work, r); err != nil {
		t.Fatal(err)
	}
	return o, n, r
}

func TestHAROwnershipRetirementRecoversEveryUnlinkBoundary(t *testing.T) {
	for step := 0; step <= 6; step++ {
		t.Run(fmt.Sprintf("after-%d-removals", step), func(t *testing.T) {
			dir := t.TempDir()
			o, n, r := fixtureHARJoinedRetirement(t, dir)
			_, tomb := r.names()
			if err := n.work.Rename(o.generation, tomb); err != nil {
				t.Fatal(err)
			}
			// These are exact persisted production retirement stages, deliberately
			// interrupted before any subsequent stage. No PID/age inference.
			names := []string{"working.har", "scrubbed.part", "owner.lock", "writer-joined.json", "manifest.json"}
			for i, name := range names {
				if i >= step {
					break
				}
				if err := o.root.Remove(name); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
			if step == 6 {
				if err := n.work.Remove(tomb); err != nil {
					t.Fatal(err)
				}
			}
			if err := syncHARRoot(n.work); err != nil {
				t.Fatal(err)
			}
			if err := n.close(); err != nil {
				t.Fatal(err)
			}
			if err := o.closeDescriptors(); err != nil {
				t.Fatal(err)
			}
			o.releaseReservation()
			plans, err := PlanRuntimeHARWorkingCleanupContext(context.Background(), dir, 1)
			if err != nil || len(plans) != 1 {
				t.Fatal("synced terminal record became unrecognizable", err, len(plans))
			}
			if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err != nil {
				t.Fatal("interrupted joined retirement was not recoverable", err)
			}
			if _, err := os.Stat(filepath.Join(dir, runtimeHARWorkingDirectory, tomb)); !os.IsNotExist(err) {
				t.Fatal("terminal payload survived", err)
			}
			n, err = lockHARNamespace(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			ledger, _, err := readHARRetirementLedger(n.work)
			_ = n.close()
			if err != nil || len(ledger.Records) != 0 {
				t.Fatal("completed terminal proof was not durably reconciled", err)
			}
		})
	}
}

func TestHAROwnershipTerminalLedgerPreservesReplacedPayloadAndLedger(t *testing.T) {
	for _, kind := range []string{"payload", "absent_payload", "ledger"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			o, n, r := fixtureHARJoinedRetirement(t, dir)
			_, tomb := r.names()
			if err := n.work.Rename(o.generation, tomb); err != nil {
				t.Fatal(err)
			}
			if kind == "absent_payload" {
				for _, name := range []string{"working.har", "owner.lock", "writer-joined.json", "manifest.json"} {
					if err := o.root.Remove(name); err != nil {
						t.Fatal(err)
					}
				}
				if err := n.work.Remove(tomb); err != nil {
					t.Fatal(err)
				}
			}
			if err := n.close(); err != nil {
				t.Fatal(err)
			}
			if err := o.closeDescriptors(); err != nil {
				t.Fatal(err)
			}
			o.releaseReservation()
			plans, err := PlanRuntimeHARWorkingCleanupContext(context.Background(), dir, 1)
			if err != nil || len(plans) != 1 {
				t.Fatal(err, len(plans))
			}
			path := filepath.Join(dir, runtimeHARWorkingDirectory, tomb)
			if kind == "ledger" {
				p := filepath.Join(dir, runtimeHARWorkingDirectory, harRetirementLedgerName)
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(p, p+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, data, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "payload" {
					if err := os.Rename(path, path+"-unowned"); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "working.har"), []byte("unproven replacement"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err == nil {
				t.Fatal("terminal receipt authorized replacement deletion")
			}
			if kind != "ledger" {
				if _, err := os.Stat(filepath.Join(path, "working.har")); err != nil {
					t.Fatal("replacement was deleted", err)
				}
			}
		})
	}
}

func TestHAROwnershipCompletedRetirementRecoversRenameAndRemoval(t *testing.T) {
	for _, kind := range []string{"renamed", "absent", "replaced", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			n, err := lockHARNamespace(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			gen, err := harRandomGeneration()
			if err != nil {
				t.Fatal(err)
			}
			name := "apexload_browser_" + gen + "_scrubbed.har"
			if err := writeHARPrivateDurable(n.root, name, []byte(ownedHARFixture)); err != nil {
				t.Fatal(err)
			}
			info, err := n.root.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			id, err := persistentHARIdentity(info)
			if err != nil {
				t.Fatal(err)
			}
			r := harRetirementRecord{Kind: "completed", Generation: gen, Name: name, Identity: id}
			if kind != "unknown" {
				if err := registerHARRetirement(n.work, r); err != nil {
					t.Fatal(err)
				}
			}
			_, tomb := r.names()
			if err := n.root.Rename(name, tomb); err != nil {
				t.Fatal(err)
			}
			if kind == "absent" {
				if err := n.root.Remove(tomb); err != nil {
					t.Fatal(err)
				}
			}
			if err := syncHARRoot(n.root); err != nil {
				t.Fatal(err)
			}
			if err := n.close(); err != nil {
				t.Fatal(err)
			}
			plans, err := PlanRuntimeHARWorkingCleanupContext(context.Background(), dir, 1)
			if kind == "unknown" {
				if err != nil || len(plans) != 0 {
					t.Fatal("unknown completed tombstone gained proof", err)
				}
				if _, err := os.Stat(filepath.Join(dir, tomb)); err != nil {
					t.Fatal("unknown completed tombstone removed", err)
				}
				return
			}
			if err != nil || len(plans) != 1 || !plans[0].Completed {
				t.Fatal("completed receipt not represented separately", err, len(plans))
			}
			if kind == "replaced" {
				if err := os.Rename(filepath.Join(dir, tomb), filepath.Join(dir, tomb+"-old")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, tomb), []byte("replacement"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err == nil {
					t.Fatal("completed replacement was deleted")
				}
				return
			}
			if err := PruneRuntimeHARWorkingCandidate(context.Background(), plans[0]); err != nil {
				t.Fatal("completed interrupted rename not resumed", err)
			}
			if _, err := os.Stat(filepath.Join(dir, tomb)); !os.IsNotExist(err) {
				t.Fatal("completed tombstone survived", err)
			}
		})
	}
}

func TestHAROwnershipLedgerCorruptionAndAdmissionFailClosed(t *testing.T) {
	uid := os.Geteuid()
	for name, data := range map[string]string{
		"version":     `{"version":2,"records":[]}`,
		"duplicate":   `{"version":1,"version":1,"records":[]}`,
		"case_alias":  `{"version":1,"Version":1,"records":[]}`,
		"unknown":     `{"version":1,"records":[],"extra":true}`,
		"utf8":        "{\"version\":1,\"records\":[],\"bad\":\"\xff\"}",
		"trailing":    `{"version":1,"records":[]} {}`,
		"null":        `{"version":1,"records":null}`,
		"path":        fmt.Sprintf(`{"version":1,"records":[{"kind":"completed","generation":"aabbccddeeff00112233445566778899","name":"../apexload_browser_escape_scrubbed.har","identity":{"device":1,"inode":2,"uid":%d}}]}`, uid),
		"null_device": fmt.Sprintf("{\"version\":1,\"records\":[{\"kind\":\"working\",\"generation\":\"aabbccddeeff00112233445566778899\",\"identity\":{\"device\":null,\"inode\":2,\"uid\":%d}}]}", uid),
		"null_name":   fmt.Sprintf("{\"version\":1,\"records\":[{\"kind\":\"working\",\"generation\":\"aabbccddeeff00112233445566778899\",\"name\":null,\"identity\":{\"device\":1,\"inode\":2,\"uid\":%d}}]}", uid),
		"too_large":   strings.Repeat(" ", maxHARRetirementLedgerBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			n, err := lockHARNamespace(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeHARPrivateDurable(n.work, harRetirementLedgerName, []byte(data)); err != nil {
				t.Fatal(err)
			}
			_ = n.close()
			before, err := os.ReadFile(filepath.Join(dir, runtimeHARWorkingDirectory, harRetirementLedgerName))
			if err != nil {
				t.Fatal(err)
			}
			if owner, err := newHARCaptureOwner(context.Background(), dir); err == nil {
				_ = owner.closeDescriptors()
				owner.releaseReservation()
				t.Fatal("malformed ledger admitted new native work")
			}
			if _, err := PlanRuntimeHARWorkingCleanupContext(context.Background(), dir, 1); err == nil {
				t.Fatal("malformed ledger returned an empty successful plan")
			}
			after, _ := os.ReadFile(filepath.Join(dir, runtimeHARWorkingDirectory, harRetirementLedgerName))
			if string(before) != string(after) {
				t.Fatal("corrupt ledger was overwritten")
			}
		})
	}
	t.Run("record_count", func(t *testing.T) {
		dir := t.TempDir()
		n, err := lockHARNamespace(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		ledger := harRetirementLedger{Version: 1, Records: []harRetirementRecord{}}
		for i := 0; i < maxHARRetirementRecords; i++ {
			ledger.Records = append(ledger.Records, harRetirementRecord{Kind: "working", Generation: fmt.Sprintf("%032x", i+1), Identity: harPersistentIdentity{Device: 1, Inode: uint64(i + 1), UID: uint32(uid)}})
		}
		data, _ := json.Marshal(ledger)
		if err := writeHARPrivateDurable(n.work, harRetirementLedgerName, data); err != nil {
			t.Fatal(err)
		}
		_ = n.close()
		if owner, err := newHARCaptureOwner(context.Background(), dir); err == nil {
			_ = owner.closeDescriptors()
			owner.releaseReservation()
			t.Fatal("full retirement ledger silently admitted native work")
		}
	})
}

// Cleanup helpers are exact fixture ownership, never an operator crash policy.
func TestHAROwnershipDocumentCallbackGoexitNeverAuthorizesRetirement(t *testing.T) {
	o := fixtureHAROwner(t, t.TempDir())
	writeOwnedHARFixture(t, o)
	o.startDocumentJoin(func() { runtime.Goexit() })
	o.startFinish(false, nil)
	if _, err := o.waitFinish(context.Background()); !errors.Is(err, errHAROwnershipUnproven) {
		t.Fatal("document callback exit invented a joined worker", err)
	}
	if o.retiredSuccessfully() {
		t.Fatal("failed document receipt retired owner")
	}

}

func TestHAROwnershipStrictManifestRejectsActualInvalidUTF8(t *testing.T) {
	data := []byte("{\"version\":1,\"generation\":\"aabbccddeeff00112233445566778899\",\"state\":\"\xff\"}")
	var m harOwnershipManifest
	if err := strictHARJSON(data, &m); err == nil {
		t.Fatal("invalid UTF-8 was replaced and admitted as an ownership contract")
	}
}
