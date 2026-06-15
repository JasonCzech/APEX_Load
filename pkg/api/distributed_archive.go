package api

// distributed_archive.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

func (b *distributedExecutionBackend) stageRemoteArchive(tr *TestRunner, test *ManagedTest, requireAll bool) (*storage.RunArchiveRecord, error) {
	bundles, err := b.fetchRemoteArchiveBundles(tr, test, requireAll)
	if err != nil {
		return nil, err
	}
	if len(bundles) == 0 {
		return nil, nil
	}
	manifest := mergeRemoteArchiveManifest(test, bundles)
	archiveDir := filepath.Join(resolveArchiveRoot(), "remote", test.ID)
	if err := os.RemoveAll(archiveDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return nil, err
	}
	if err := writeMergedRemoteArchiveFiles(archiveDir, &manifest, bundles); err != nil {
		return nil, err
	}
	if err := writeArchiveParquetFiles(archiveDir, &manifest); err != nil {
		return nil, err
	}
	manifestContent, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "manifest.json"), manifestContent, 0o644); err != nil {
		return nil, err
	}
	if err := uploadArchiveDirectory(context.Background(), archiveDir, manifest.TestID); err != nil {
		return nil, err
	}
	archiveBackend := currentArchiveBackend()
	return &storage.RunArchiveRecord{
		TestID:               manifest.TestID,
		SchemaVersion:        manifest.SchemaVersion,
		ArchiveBackend:       archiveBackend.Backend,
		ArchivePath:          archiveDir,
		ManifestPath:         filepath.Join(archiveDir, "manifest.json"),
		ArchiveURI:           archiveBackend.runURI(manifest.TestID),
		ManifestURI:          archiveBackend.manifestURI(manifest.TestID),
		TransactionCount:     manifest.TransactionCount,
		ResourceSampleCount:  manifest.ResourceSampleCount,
		IntervalSummaryCount: manifest.IntervalSummaryCount,
		CreatedAt:            manifest.CreatedAt,
		CompletedAt:          manifest.CompletedAt,
	}, nil
}

type remoteArchiveBundle struct {
	target       string
	manifest     RunArchiveManifest
	transactions []byte
	resources    []byte
	summaries    []byte
}

func (b *distributedExecutionBackend) fetchRemoteArchiveBundles(tr *TestRunner, test *ManagedTest, requireAll bool) ([]remoteArchiveBundle, error) {
	clients, err := b.resolveClientsLocked(tr, test)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	bundles := make([]remoteArchiveBundle, 0, len(clients))
	missingTargets := make([]string, 0)
	for _, target := range orderedGeneratorTargets(clients, b.configuredTargets()) {
		client := clients[target]
		manifestContent, err := client.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: test.ID, Name: "manifest.json"})
		if err != nil {
			code := grpcStatus.Code(err)
			if code == grpcCodes.NotFound || code == grpcCodes.Unimplemented || (!requireAll && (code == grpcCodes.Canceled || code == grpcCodes.Unavailable)) {
				missingTargets = append(missingTargets, target)
				continue
			}
			return nil, fmt.Errorf("fetch archive manifest from %s: %w", target, err)
		}
		var manifest RunArchiveManifest
		if err := json.Unmarshal(manifestContent, &manifest); err != nil {
			return nil, fmt.Errorf("decode archive manifest from %s: %w", target, err)
		}
		transactions, err := client.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: test.ID, Name: manifest.TransactionsFile})
		if err != nil {
			return nil, fmt.Errorf("fetch transaction archive from %s: %w", target, err)
		}
		resources, err := client.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: test.ID, Name: manifest.ResourceSamplesFile})
		if err != nil {
			return nil, fmt.Errorf("fetch resource archive from %s: %w", target, err)
		}
		summaries, err := client.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: test.ID, Name: manifest.IntervalSummariesFile})
		if err != nil {
			return nil, fmt.Errorf("fetch interval summaries archive from %s: %w", target, err)
		}
		bundles = append(bundles, remoteArchiveBundle{
			target:       target,
			manifest:     manifest,
			transactions: transactions,
			resources:    resources,
			summaries:    summaries,
		})
	}
	if len(bundles) == 0 {
		return nil, nil
	}
	if requireAll && len(missingTargets) > 0 {
		return nil, fmt.Errorf("archive not available from targets: %s", strings.Join(missingTargets, ", "))
	}
	return bundles, nil
}

func mergeRemoteArchiveManifest(test *ManagedTest, bundles []remoteArchiveBundle) RunArchiveManifest {
	manifest := RunArchiveManifest{
		SchemaVersion:                archiveSchemaVersion,
		TestID:                       test.ID,
		TestName:                     test.Name,
		CreatedAt:                    test.CreatedAt,
		TransactionsFile:             "transactions.jsonl",
		TransactionsParquetFile:      "transactions.parquet",
		ResourceSamplesFile:          "resources.jsonl",
		ResourceSamplesParquetFile:   "resources.parquet",
		IntervalSummariesFile:        "interval_summaries.jsonl",
		IntervalSummariesParquetFile: "interval_summaries.parquet",
	}
	errors := make([]string, 0)
	for index, bundle := range bundles {
		if index == 0 || bundle.manifest.CreatedAt.Before(manifest.CreatedAt) {
			manifest.CreatedAt = bundle.manifest.CreatedAt
		}
		if bundle.manifest.CompletedAt.After(manifest.CompletedAt) {
			manifest.CompletedAt = bundle.manifest.CompletedAt
		}
		manifest.TransactionCount += bundle.manifest.TransactionCount
		manifest.ResourceSampleCount += bundle.manifest.ResourceSampleCount
		manifest.IntervalSummaryCount += bundle.manifest.IntervalSummaryCount
		if bundle.manifest.Status == "FAILED" {
			manifest.Status = "FAILED"
		}
		if bundle.manifest.Error != "" {
			errors = append(errors, bundle.manifest.Error)
		}
	}
	if manifest.Status == "" {
		manifest.Status = "COMPLETE"
	}
	manifest.Error = strings.Join(errors, "; ")
	return manifest
}

func writeMergedRemoteArchiveFiles(rootDir string, manifest *RunArchiveManifest, bundles []remoteArchiveBundle) error {
	transactionsFile, err := os.Create(filepath.Join(rootDir, manifest.TransactionsFile))
	if err != nil {
		return err
	}
	defer transactionsFile.Close()
	resourcesFile, err := os.Create(filepath.Join(rootDir, manifest.ResourceSamplesFile))
	if err != nil {
		return err
	}
	defer resourcesFile.Close()
	summariesFile, err := os.Create(filepath.Join(rootDir, manifest.IntervalSummariesFile))
	if err != nil {
		return err
	}
	defer summariesFile.Close()
	for _, bundle := range bundles {
		if err := appendArchiveContent(transactionsFile, bundle.transactions); err != nil {
			return err
		}
		if err := appendArchiveContent(resourcesFile, bundle.resources); err != nil {
			return err
		}
		if err := appendArchiveContent(summariesFile, bundle.summaries); err != nil {
			return err
		}
	}
	return nil
}

func appendArchiveContent(file *os.File, content []byte) error {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return nil
	}
	if _, err := file.Write(trimmed); err != nil {
		return err
	}
	if _, err := file.Write([]byte("\n")); err != nil {
		return err
	}
	return nil
}
