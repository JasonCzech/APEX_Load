package generatorrpc

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/archivequota"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestGeneratorArchiveBacklogRetainsCompletedSources(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	first, err := newArchiveRecorder("quota-generator", "retained", "retained")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.RecordResult(&protocol.Result{ActionName: "accepted", Success: true}); err != nil {
		t.Fatal(err)
	}
	record, err := first.Finalize("COMPLETE", time.Now().UTC(), "")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var retained int64
	if err := filepath.WalkDir(record.ArchivePath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			retained += info.Size()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_BACKLOG_MAX_BYTES", strconv.FormatInt(retained, 10))
	next, err := newArchiveRecorder("quota-generator", "rejected", "rejected")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Discard()
	if err := next.RecordResult(&protocol.Result{ActionName: "over-quota", Success: true}); err == nil {
		t.Fatal("completed archives were not charged against the backlog limit")
	}
	after, err := os.ReadFile(record.ManifestPath)
	if err != nil || string(after) != string(before) {
		t.Fatal("completed source changed", err)
	}
}

func TestGeneratorArchiveBacklogRejectsInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{"0", "-1", "x", "1.5", "1125899906842625"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_BACKLOG_MAX_BYTES", raw)
			writer, err := newArchiveRecorder("quota-generator", "invalid", "invalid")
			if writer != nil {
				writer.Discard()
			}
			if err == nil {
				t.Fatal("invalid backlog configuration accepted")
			}
		})
	}
}

func TestGeneratorArchiveBacklogReservesItsNamespace(t *testing.T) {
	for _, name := range []string{archivequota.DirectoryName, ".APEXLOAD-BACKLOG", ".apexload-backlog.", ".apexload-backlog:stream"} {
		if err := validateArchiveTestID(name); err == nil {
			t.Fatal("backlog ledger can be reused as a run ID", name)
		}
		// Generator names sanitize colons to underscores before namespace use;
		// run IDs preserve their spelling and must reject NTFS stream aliases.
		if !strings.Contains(name, ":") {
			if namespace, err := prepareGeneratorArchiveNamespace(name, true); err == nil {
				namespace.close()
				t.Fatal("backlog ledger can be reused as generator namespace", name)
			}
		}
	}
}

func TestGeneratorArchiveBacklogConfigurationIsFrozen(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envGeneratorArchiveMinFree, "1")
	t.Setenv(envGeneratorArchiveBacklogMaxBytes, "1048576")
	writer, err := newArchiveRecorder("quota-generator", "frozen", "frozen")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Discard()
	t.Setenv(envGeneratorArchiveBacklogMaxBytes, "1")
	if err := writer.RecordResult(&protocol.Result{ActionName: "accepted"}); err != nil {
		t.Fatal("existing writer configuration changed", err)
	}
}

func TestGeneratorArchiveBacklogSurvivesRetentionSweep(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newArchiveRecorder("quota-generator", "initialize-ledger", "initialize-ledger")
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(writer.namespace.path, archivequota.DirectoryName)
	if err := writer.Discard(); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(ledger, "lease-"+strings.Repeat("a", 32))
	if err := os.WriteFile(marker, []byte("64\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(ledger, old, old); err != nil {
		t.Fatal(err)
	}
	service := NewService("quota-generator", 10, WithGeneratorStateLimits(10, 1, time.Hour))
	defer service.Shutdown(context.Background())
	service.sweepGeneratorArchiveDirectories(time.Now())
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "64\n" {
		t.Fatal("retention removed a crash reservation", err)
	}
}
