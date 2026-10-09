package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type sanitizedRunArchivePlan struct {
	manifest         *RunArchiveManifest
	replacements     map[string][]byte
	stream           map[string]func(io.Writer) error
	sizes            map[string]int64
	omit             map[string]struct{}
	transactionStats sanitizedTransactionStats
}

// buildSanitizedRunArchivePlan prepares disclosure-safe representations for
// every structured archive artifact that can carry runtime credentials. It is
// intentionally used at read time so archives produced by older controllers
// receive the same protection as new writes.
func buildSanitizedRunArchivePlan(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (*sanitizedRunArchivePlan, error) {
	return buildSanitizedRunArchivePlanWith(ctx, record, manifest, false)
}

// buildSanitizedRunArchivePlanWith is buildSanitizedRunArchivePlan that, with
// includeHAR, keeps the run's native HARs (for a requester who may download
// them); without it the export omits them as safe_export.
func buildSanitizedRunArchivePlanWith(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, includeHAR bool) (*sanitizedRunArchivePlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key := sanitizedArchivePlanCacheKey(record)
	if key == "" {
		prepared, err := prepareSanitizedRunArchivePlanWith(ctx, record, manifest, includeHAR)
		if err != nil {
			return nil, err
		}
		return sanitizedArchivePlanForRequest(ctx, record, manifest, prepared), nil
	}
	if includeHAR {
		key += "|har"
	}
	prepared, err := runArchiveSanitizedPlanCache.getOrLoadContext(ctx, key, func() (*sanitizedRunArchivePlan, error) {
		loadCtx, cancelLoad := context.WithTimeout(context.Background(), configuredArchiveMaterializationTimeout())
		defer cancelLoad()
		return prepareSanitizedRunArchivePlanWith(loadCtx, record, manifest, includeHAR)
	})
	if err != nil {
		return nil, err
	}
	return sanitizedArchivePlanForRequest(ctx, record, manifest, prepared), nil
}

func prepareSanitizedRunArchivePlan(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (*sanitizedRunArchivePlan, error) {
	return prepareSanitizedRunArchivePlanWith(ctx, record, manifest, false)
}

func prepareSanitizedRunArchivePlanWith(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, includeHAR bool) (*sanitizedRunArchivePlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if record == nil || strings.TrimSpace(record.ArchivePath) == "" {
		return nil, fmt.Errorf("archive record is required")
	}
	if manifest == nil {
		return nil, fmt.Errorf("archive manifest is required")
	}

	stats, err := writeSanitizedArchiveTransactions(ctx, record, manifest, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("sanitize transaction archive: %w", err)
	}
	maskedManifest := maskRunArchiveManifest(manifest)
	maskedManifest.TransactionCount = stats.Count
	maskedManifest.Artifacts = make([]RunArchiveArtifact, 0, len(manifest.Artifacts))
	foundTransactions := false
	// Native HARs hold scrubbed response content, so a safe export carries
	// them only for a requester who may download HARs (includeHAR, the
	// editor-only ?include=har export). Otherwise the masked manifest does not
	// list them and says they were omitted, so an imported export never claims
	// complete HAR evidence it does not hold.
	var harNames []string
	if omitted := int64(len(manifest.BrowserHARArtifacts)); omitted > 0 && !includeHAR {
		reasons := maps.Clone(manifest.BrowserHAROmitted)
		if reasons == nil {
			reasons = map[string]int64{}
		}
		reasons[protocol.BrowserHAROmittedSafeExport] += omitted
		maskedManifest.BrowserHAROmitted, maskedManifest.BrowserHARIncomplete = reasons, true
	}
	if !includeHAR {
		maskedManifest.BrowserHARArtifacts = nil
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.Name == strings.TrimSpace(manifest.TransactionsParquetFile) && artifact.Name != "" {
			continue
		}
		if artifact.RecordType == runArchiveHARRecordType && !includeHAR {
			harNames = append(harNames, artifact.Name)
			continue
		}
		if artifact.Name == strings.TrimSpace(manifest.TransactionsFile) {
			foundTransactions = true
			artifact.SizeBytes = stats.SizeBytes
			artifact.SHA256 = stats.SHA256
			artifact.Count = stats.Count
		}
		maskedManifest.Artifacts = append(maskedManifest.Artifacts, artifact)
	}
	if !foundTransactions {
		maskedManifest.Artifacts = append(maskedManifest.Artifacts, RunArchiveArtifact{
			Name:       strings.TrimSpace(manifest.TransactionsFile),
			Kind:       "dataset",
			Format:     "jsonl",
			RecordType: "transactions",
			Count:      stats.Count,
			SizeBytes:  stats.SizeBytes,
			SHA256:     stats.SHA256,
		})
	}

	transactionName := strings.TrimSpace(manifest.TransactionsFile)
	plan := &sanitizedRunArchivePlan{
		manifest:         maskedManifest,
		replacements:     make(map[string][]byte, 2),
		stream:           nil,
		sizes:            map[string]int64{transactionName: stats.SizeBytes},
		omit:             make(map[string]struct{}, 1+len(harNames)),
		transactionStats: stats,
	}
	for _, name := range harNames {
		plan.omit[name] = struct{}{}
	}
	if parquetName := strings.TrimSpace(manifest.TransactionsParquetFile); parquetName != "" {
		// Legacy parquet embeds extractions/snapshots and cannot be safely patched
		// in place. Omit it from safe exports; clients can regenerate it from the
		// sanitized JSONL stream.
		plan.omit[parquetName] = struct{}{}
		maskedManifest.TransactionsParquetFile = ""
	}

	if testManifestName := strings.TrimSpace(maskedManifest.TestManifestFile); testManifestName != "" {
		root, err := openArchiveDirectoryNoSymlink(record.ArchivePath)
		if err != nil {
			return nil, err
		}
		data, _, readErr := readArchiveFileAtRootLimitedContext(ctx, root, testManifestName, maxArchiveManifestDownloadBytes)
		closeErr := root.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read test manifest for safe export: %w", readErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		var testManifest TestRunManifest
		if err := json.Unmarshal(data, &testManifest); err != nil {
			return nil, fmt.Errorf("decode test manifest for safe export: %w", err)
		}
		maskedTestManifest, err := json.MarshalIndent(maskTestRunManifest(&testManifest), "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode test manifest for safe export: %w", err)
		}
		plan.replacements[testManifestName] = maskedTestManifest
		for index := range maskedManifest.Artifacts {
			if maskedManifest.Artifacts[index].Name == testManifestName {
				maskedManifest.Artifacts[index].SizeBytes = int64(len(maskedTestManifest))
				maskedManifest.Artifacts[index].SHA256 = sha256BytesHex(maskedTestManifest)
			}
		}
	}

	manifestData, err := json.MarshalIndent(maskedManifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode archive manifest for safe export: %w", err)
	}
	plan.replacements[runArchiveManifestFileName] = manifestData
	return plan, nil
}

func sanitizedArchivePlanCacheKey(record *storage.RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	testID := strings.TrimSpace(record.TestID)
	hash := strings.TrimSpace(record.ManifestSHA256)
	if testID == "" || hash == "" {
		return ""
	}
	return testID + "|sanitized|" + strings.ToLower(hash)
}

func sanitizedArchivePlanWeight(plan *sanitizedRunArchivePlan) int64 {
	if plan == nil {
		return 0
	}
	var size int64
	for name, data := range plan.replacements {
		size += int64(len(name) + len(data))
	}
	for name := range plan.sizes {
		size += int64(len(name) + 16)
	}
	for name := range plan.omit {
		size += int64(len(name))
	}
	return size
}

func sanitizedArchivePlanForRequest(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, prepared *sanitizedRunArchivePlan) *sanitizedRunArchivePlan {
	if prepared == nil {
		return nil
	}
	plan := &sanitizedRunArchivePlan{
		manifest:         prepared.manifest,
		replacements:     prepared.replacements,
		sizes:            prepared.sizes,
		omit:             prepared.omit,
		stream:           make(map[string]func(io.Writer) error, 1),
		transactionStats: prepared.transactionStats,
	}
	transactionName := strings.TrimSpace(manifest.TransactionsFile)
	plan.stream[transactionName] = func(destination io.Writer) error {
		written, err := writeSanitizedArchiveTransactions(ctx, record, manifest, destination)
		if err != nil {
			return err
		}
		if written != prepared.transactionStats {
			return fmt.Errorf("transaction archive changed while preparing sanitized export")
		}
		return nil
	}
	return plan
}

func zipSanitizedRunArchiveContext(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, dst io.Writer) error {
	return zipSanitizedRunArchiveWith(ctx, record, manifest, false, dst)
}

// zipSanitizedRunArchiveWith writes the safe export, keeping the run's native
// HARs when includeHAR.
func zipSanitizedRunArchiveWith(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, includeHAR bool, dst io.Writer) error {
	plan, err := buildSanitizedRunArchivePlanWith(ctx, record, manifest, includeHAR)
	if err != nil {
		return err
	}
	return zipDirectoryContextWithSanitizers(ctx, record.ArchivePath, dst, plan.replacements, plan.stream, plan.omit)
}

func sanitizedRunArchiveManifest(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (*RunArchiveManifest, error) {
	plan, err := buildSanitizedRunArchivePlan(ctx, record, manifest)
	if err != nil {
		return nil, err
	}
	return plan.manifest, nil
}

func sanitizedRunArchiveMetadata(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (string, int64, error) {
	plan, err := buildSanitizedRunArchivePlan(ctx, record, manifest)
	if err != nil {
		return "", 0, err
	}
	manifestData, ok := plan.replacements[runArchiveManifestFileName]
	if !ok {
		return "", 0, fmt.Errorf("safe archive manifest is missing")
	}
	root, err := openArchiveDirectoryNoSymlink(record.ArchivePath)
	if err != nil {
		return "", 0, err
	}
	sizeBytes, sizeErr := sanitizedArchiveDirectorySize(ctx, root, "", plan)
	closeErr := root.Close()
	if sizeErr != nil {
		return "", 0, sizeErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	return "sha256:" + sha256BytesHex(manifestData), sizeBytes, nil
}

func sanitizedArchiveDirectorySize(ctx context.Context, root *os.Root, prefix string, plan *sanitizedRunArchivePlan) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	directory, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return 0, readErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		name := entry.Name()
		if name == archiveMaterializationCacheMarkerSuffix {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil {
			return 0, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("archive contains symbolic link %s", name)
		}
		archiveName := name
		if prefix != "" {
			archiveName = prefix + "/" + name
		}
		if _, omitted := plan.omit[archiveName]; omitted {
			continue
		}
		if info.IsDir() {
			child, err := openArchiveChildDirectoryAt(root, name, false, 0)
			if err != nil {
				return 0, err
			}
			childSize, sizeErr := sanitizedArchiveDirectorySize(ctx, child, archiveName, plan)
			childCloseErr := child.Close()
			if sizeErr != nil {
				return 0, sizeErr
			}
			if childCloseErr != nil {
				return 0, childCloseErr
			}
			total += childSize
			continue
		}
		if !info.Mode().IsRegular() {
			return 0, fmt.Errorf("archive contains non-regular artifact %s", archiveName)
		}
		if replacement, ok := plan.replacements[archiveName]; ok {
			total += int64(len(replacement))
		} else if size, ok := plan.sizes[archiveName]; ok {
			total += size
		} else {
			total += info.Size()
		}
	}
	return total, nil
}

func sanitizedRunArchiveArtifact(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, name string) ([]byte, os.FileInfo, bool, error) {
	if name != runArchiveManifestFileName && name != strings.TrimSpace(manifest.TestManifestFile) {
		return nil, nil, false, nil
	}
	plan, err := buildSanitizedRunArchivePlan(ctx, record, manifest)
	if err != nil {
		return nil, nil, true, err
	}
	data, ok := plan.replacements[name]
	if !ok {
		return nil, nil, true, fmt.Errorf("archive artifact %q has no safe representation", name)
	}
	file, info, err := openArchiveArtifactForRead(record.ArchivePath, name)
	if err != nil {
		return nil, nil, true, err
	}
	if err := file.Close(); err != nil {
		return nil, nil, true, err
	}
	return data, info, true, nil
}
