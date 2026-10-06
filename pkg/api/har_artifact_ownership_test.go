package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/filelock"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestHARArtifactOwnershipControllerCannotPruneOtherProcessWriter(t *testing.T) {
	const generation = "aabbccddeeff00112233445566778899"
	const fixture = `{"log":{"version":"1.2","creator":{"name":"synthetic","version":"1"},"entries":[]}}`
	if dir := os.Getenv("APEX_HAR_CONTROLLER_LEASE_FIXTURE"); dir != "" {
		var lease, writer *os.File
		base := filepath.Join(".apexload-har-work", generation)
		if err := browserprotocol.WithRuntimeHARArtifactNamespace(context.Background(), dir, func(root *os.Root) error {
			if err := root.Mkdir(base, 0o700); err != nil {
				return err
			}
			var err error
			lease, err = root.OpenFile(filepath.Join(base, "owner.lock"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if err != nil {
				return err
			}
			manifest, err := root.OpenFile(filepath.Join(base, "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			if _, err := manifest.WriteString(`{"version":1,"generation":"` + generation + `","state":"sealed"}`); err != nil {
				return err
			}
			if err := manifest.Sync(); err != nil {
				return err
			}
			if err := manifest.Close(); err != nil {
				return err
			}
			writer, err = root.OpenFile(filepath.Join(base, "working.har"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		unlock, err := filelock.LockContextOwned(context.Background(), lease)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		if _, err := writer.WriteString(fixture); err != nil {
			t.Fatal(err)
		}
		if err := writer.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		var command [1]byte
		if _, err := io.ReadFull(os.Stdin, command[:]); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(dir, base, "writer-joined.json")
		if err := os.WriteFile(marker, []byte(`{"version":1,"generation":"`+generation+`","state":"writer_joined"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "joined"), []byte("joined"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(os.Stdin, command[:]); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // The second process must acquire this exact existing lease.
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHARArtifactOwnershipControllerCannotPruneOtherProcessWriter$")
	cmd.Env = append(os.Environ(), "APEX_HAR_CONTROLLER_LEASE_FIXTURE="+dir)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	log, err := os.Create(filepath.Join(dir, "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var waitOnce sync.Once
	var waitErr error
	waitChild := func() error { waitOnce.Do(func() { waitErr = cmd.Wait() }); return waitErr }
	defer func() { cancel(); _ = waitChild() }()
	waitMarker := func(name string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("child fixture did not reach", name)
	}
	waitMarker("ready")
	raw := filepath.Join(dir, ".apexload-har-work", generation, "working.har")
	settings := ArchiveRetentionSettings{Enabled: false, HARArtifactDir: dir}
	if plans := planHARArtifactRetention(time.Now().UTC(), settings); len(plans) != 0 {
		t.Fatal("controller planned another process's live raw writer", plans)
	}
	if _, err := os.Stat(raw); err != nil {
		t.Fatal("controller deleted live raw", err)
	}
	if _, err := input.Write([]byte{'j'}); err != nil {
		t.Fatal(err)
	}
	waitMarker("joined")
	plans := planHARArtifactRetention(time.Now().UTC(), settings)
	if len(plans) != 1 || plans[0].Sensitivity != scrub.SensitivityRaw || plans[0].SizeBytes < int64(len(fixture)) || plans[0].ModifiedAt.IsZero() || plans[0].ExpiredAt.IsZero() {
		t.Fatal("writer-joined provenance not represented", plans)
	}
	_, pruned, failed := applyHARArtifactRetention(plans)
	if pruned != 0 || failed != 1 {
		t.Fatal("controller bypassed another process's held lease", pruned, failed)
	}
	if _, err := os.Stat(raw); err != nil {
		t.Fatal("held joined lease deleted", err)
	}
	if _, err := input.Write([]byte{'e'}); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	if err := waitChild(); err != nil {
		t.Fatal("child fixture", err)
	}
	_, pruned, failed = applyHARArtifactRetention(plans)
	if pruned != 1 || failed != 0 {
		t.Fatal("controller did not reclaim proven joined abandoned generation", pruned, failed)
	}
	if _, err := os.Stat(raw); !os.IsNotExist(err) {
		t.Fatal("joined abandoned raw survived", err)
	}
}

func TestHARArtifactOwnershipRetiresEntireRecordedCompletedBatch(t *testing.T) {
	for _, renamed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_rename", true: "after_rename"}[renamed], func(t *testing.T) {
			dir := t.TempDir()
			var tombstones []string
			if err := browserprotocol.WithRuntimeHARArtifactNamespace(context.Background(), dir, func(root *os.Root) error {
				records := make([]map[string]any, 0, 2)
				for i := 1; i <= 2; i++ {
					generation := fmt.Sprintf("%032x", i)
					name := "apexload_browser_" + generation + "_scrubbed.har"
					data, err := scrub.HAR([]byte("{\"log\":{\"version\":\"1.2\",\"creator\":{\"name\":\"synthetic\",\"version\":\"1\"},\"entries\":[]}}"), scrub.Policy{})
					if err != nil {
						return err
					}
					file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
					if err != nil {
						return err
					}
					if _, err := file.Write(data); err != nil {
						_ = file.Close()
						return err
					}
					if err := file.Sync(); err != nil {
						_ = file.Close()
						return err
					}
					if err := file.Close(); err != nil {
						return err
					}
					info, err := root.Stat(name)
					if err != nil {
						return err
					}
					stat := reflect.Indirect(reflect.ValueOf(info.Sys()))
					field := func(name string) uint64 {
						t.Helper()
						if stat.Kind() != reflect.Struct {
							t.Skip("host has no qualified persistent HAR file identity")
						}
						value := stat.FieldByName(name)
						switch value.Kind() {
						case reflect.Int, reflect.Int32, reflect.Int64:
							return uint64(value.Int())
						case reflect.Uint, reflect.Uint32, reflect.Uint64:
							return value.Uint()
						default:
							t.Skip("host has no qualified persistent HAR file identity")
							return 0
						}
					}
					records = append(records, map[string]any{"kind": "completed", "generation": generation, "name": name, "identity": map[string]uint64{"device": field("Dev"), "inode": field("Ino"), "uid": field("Uid")}})
					tombstones = append(tombstones, ".har-retired-"+generation)
				}
				// Persist the exact production ledger contract before deliberately
				// interrupting both completed-file retirements after their rename.
				ledger, err := root.OpenFile(filepath.Join(".apexload-har-work", "retirements.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if err != nil {
					return err
				}
				data, err := json.Marshal(map[string]any{"version": 1, "records": records})
				if err != nil {
					_ = ledger.Close()
					return err
				}
				if _, err := ledger.Write(data); err != nil {
					_ = ledger.Close()
					return err
				}
				if err := ledger.Sync(); err != nil {
					_ = ledger.Close()
					return err
				}
				if err := ledger.Close(); err != nil {
					return err
				}
				work, err := root.OpenRoot(".apexload-har-work")
				if err != nil {
					return err
				}
				workDir, err := work.Open(".")
				if err != nil {
					_ = work.Close()
					return err
				}
				workSyncErr := errors.Join(workDir.Sync(), workDir.Close(), work.Close())
				if workSyncErr != nil {
					return workSyncErr
				}
				if renamed {
					for i, record := range records {
						if err := root.Rename(record["name"].(string), tombstones[i]); err != nil {
							return err
						}
					}
				}
				directory, err := root.Open(".")
				if err != nil {
					return err
				}
				syncErr := directory.Sync()
				closeErr := directory.Close()
				return errors.Join(syncErr, closeErr)
			}); err != nil {
				t.Fatal(err)
			}
			settings := ArchiveRetentionSettings{Enabled: false, HARArtifactDir: dir}
			now := time.Now().UTC()
			if !renamed {
				// Both original flat basenames are expired as well as present in the
				// durable retirement plan. They must each appear and retire once.
				settings.Enabled, settings.HARRetentionDays = true, 1
				now = now.Add(48 * time.Hour)
			}
			plans := planHARArtifactRetention(now, settings)
			if len(plans) != 2 {
				t.Fatal("controller did not plan both recorded completed terminal payloads", len(plans))
			}
			results, pruned, failed := applyHARArtifactRetention(plans)
			if len(results) != 2 || pruned != 2 || failed != 0 {
				t.Fatal("controller invalidated its own unchanged retirement batch", pruned, failed)
			}
			for _, name := range tombstones {
				if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Fatal("completed terminal payload survived", err)
				}
				generation := strings.TrimPrefix(name, ".har-retired-")
				if _, err := os.Stat(filepath.Join(dir, "apexload_browser_"+generation+"_scrubbed.har")); !os.IsNotExist(err) {
					t.Fatal("completed original payload survived", err)
				}
			}

		})
	}
}

func TestHARArtifactOwnershipCorruptLedgerReportsUnavailableAndPreservesState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".apexload-har-work", "retirements.json")
	const invalid = "{\"version\":2,\"records\":[]}"
	if err := browserprotocol.WithRuntimeHARArtifactNamespace(context.Background(), dir, func(root *os.Root) error {
		file, err := root.OpenFile(filepath.Join(".apexload-har-work", "retirements.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := file.WriteString(invalid); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	}); err != nil {
		t.Fatal(err)
	}
	plans := planHARArtifactRetention(time.Now().UTC(), ArchiveRetentionSettings{Enabled: false, HARArtifactDir: dir})
	if len(plans) != 1 || plans[0].planningError == nil {
		t.Fatal("corrupt terminal proof became an empty successful plan")
	}
	results, pruned, failed := applyHARArtifactRetention(plans)
	if len(results) != 1 || pruned != 0 || failed != 1 || results[0].Error == "" {
		t.Fatal("unavailable maintenance was not observable", pruned, failed)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != invalid {
		t.Fatal("corrupt terminal state was overwritten", err)
	}
}
