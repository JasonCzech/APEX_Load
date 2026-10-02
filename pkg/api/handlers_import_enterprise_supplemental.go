package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const enterpriseSupplementalArchiveManifestKey = "supplemental_archive_manifest"

type enterpriseSupplementalArchiveManifest struct {
	Contract         string                                `json:"contract"`
	PrimarySizeBytes int64                                 `json:"primary_size_bytes"`
	PrimarySHA256    string                                `json:"primary_sha256"`
	Bundles          []enterpriseSupplementalArchiveBundle `json:"bundles"`
}

type enterpriseSupplementalArchiveBundle struct {
	SourceName string                                `json:"source_name"`
	SizeBytes  int64                                 `json:"size_bytes"`
	SHA256     string                                `json:"sha256"`
	Files      []enterpriseSupplementalArchiveMember `json:"files"`
}

type enterpriseSupplementalArchiveMember struct {
	SourcePath  string `json:"source_path"`
	ArchivePath string `json:"archive_path"`
	LogicalPath string `json:"logical_path"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}

type enterpriseSupplementalArchiveFile struct {
	Name       string
	Data       []byte
	FromBundle bool
}

// Supplemental ZIPs are dependency/source containers, not another script
// project. Expand one level only and apply a combined budget before inflation.
// Unlike individually attached replacement sources, bundle members may never
// overwrite another primary or supplemental entry.
func prepareEnterpriseSupplementalArchives(base []byte, names []string, contents [][]byte) ([]enterpriseSupplementalArchiveFile, *enterpriseSupplementalArchiveManifest, error) {
	return prepareEnterpriseSupplementalArchivesWithLimits(base, names, contents, archiveingest.Limits{
		MaxEntries: enterprisearchive.MaxArchiveEntries, MaxEntryBytes: enterprisearchive.MaxArchiveEntryBytes,
		MaxTotalBytes: enterprisearchive.MaxArchiveTotalBytes, MaxDepth: 32,
	})
}

func prepareEnterpriseSupplementalArchivesWithLimits(base []byte, names []string, contents [][]byte, limits archiveingest.Limits) ([]enterpriseSupplementalArchiveFile, *enterpriseSupplementalArchiveManifest, error) {
	if len(names) != len(contents) {
		return nil, nil, fmt.Errorf("supplemental file names and contents must have equal counts")
	}
	reader, err := zip.NewReader(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		return nil, nil, err
	}
	count, total := len(reader.File), int64(0)
	if count > limits.MaxEntries {
		return nil, nil, fmt.Errorf("archive entry count exceeds limit %d", limits.MaxEntries)
	}
	primaryNames := make(map[string]string, count)
	commonRoot := ""
	rootPossible := true
	for _, entry := range reader.File {
		// Merging rewrites headers as regular files. Reject source metadata that
		// the bounded reader would reject instead of laundering it through the
		// rewrite, including when an explicit supplemental file replaces it.
		rawName := strings.TrimPrefix(strings.ReplaceAll(entry.Name, `\`, "/"), "./")
		if entry.NonUTF8 || entry.Flags&1 != 0 {
			return nil, nil, fmt.Errorf("primary archive contains unsupported name encoding or encrypted entries")
		}
		isDirectory := strings.HasSuffix(rawName, "/")
		mode := entry.Mode()
		if isDirectory || mode.IsDir() {
			if !isDirectory || !mode.IsDir() {
				return nil, nil, fmt.Errorf("primary archive directory metadata does not match its path")
			}
			if _, err := archiveingest.ValidateFilePath(strings.TrimSuffix(rawName, "/"), limits.MaxDepth); err != nil {
				return nil, nil, fmt.Errorf("primary archive directory is not a safe relative path: %w", err)
			}
			continue
		}
		if mode.Type() != 0 {
			return nil, nil, fmt.Errorf("primary archive permits only regular files")
		}
		name, err := archiveingest.ValidateFilePath(rawName, limits.MaxDepth)
		if err != nil {
			return nil, nil, fmt.Errorf("primary archive entry is not a safe relative path: %w", err)
		}
		key := supplementalArchivePathKey(name)
		if _, exists := primaryNames[key]; exists {
			return nil, nil, fmt.Errorf("primary archive contains colliding entries")
		}
		primaryNames[key] = name
		if entry.UncompressedSize64 > uint64(limits.MaxEntryBytes) || entry.UncompressedSize64 > uint64(limits.MaxTotalBytes-total) {
			return nil, nil, fmt.Errorf("primary archive exceeds an uncompressed byte limit")
		}
		total += int64(entry.UncompressedSize64)
		parts := strings.SplitN(name, "/", 2)
		if len(parts) != 2 || commonRoot != "" && commonRoot != parts[0] {
			rootPossible = false
		} else if commonRoot == "" {
			commonRoot = parts[0]
		}
	}
	if !rootPossible {
		commonRoot = ""
	}
	var manifest *enterpriseSupplementalArchiveManifest
	var out []enterpriseSupplementalArchiveFile
	seen := make(map[string]string)
	attachments := make(map[string]bool)
	for index, name := range names {
		canonical, err := archiveingest.ValidateFilePath(name, limits.MaxDepth)
		if err != nil || canonical != name {
			return nil, nil, fmt.Errorf("supplemental file names must be canonical relative archive paths")
		}
		attachmentKey := supplementalArchivePathKey(name)
		if attachments[attachmentKey] {
			return nil, nil, fmt.Errorf("supplemental files both target colliding archive paths")
		}
		attachments[attachmentKey] = true
		body := contents[index]
		if !strings.EqualFold(path.Ext(name), ".zip") {
			count++
			if count > limits.MaxEntries || len(body) > int(limits.MaxEntryBytes) || int64(len(body)) > limits.MaxTotalBytes-total {
				return nil, nil, fmt.Errorf("combined supplemental import exceeds an archive limit")
			}
			total += int64(len(body))
			// Keep newly attached files under the primary's disposable container,
			// so adding a helper cannot disable common-root normalization.
			archivePath := name
			if commonRoot != "" && !strings.HasPrefix(name, commonRoot+"/") {
				archivePath = commonRoot + "/" + name
			}
			if _, err := archiveingest.ValidateFilePath(archivePath, limits.MaxDepth); err != nil {
				return nil, nil, fmt.Errorf("merged supplemental file exceeds the archive path limit")
			}
			key := supplementalArchivePathKey(archivePath)
			if _, duplicate := seen[key]; duplicate {
				return nil, nil, fmt.Errorf("supplemental files both target colliding archive paths")
			}
			seen[key] = archivePath
			out = append(out, enterpriseSupplementalArchiveFile{Name: archivePath, Data: body})
			continue
		}
		bundleReader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return nil, nil, fmt.Errorf("supplemental ZIP is invalid: %w", err)
		}
		count += len(bundleReader.File)
		if count > limits.MaxEntries {
			return nil, nil, fmt.Errorf("combined supplemental import exceeds archive entry limit %d", limits.MaxEntries)
		}
		bundleTotal := int64(0)
		for _, entry := range bundleReader.File {
			if entry.FileInfo().IsDir() {
				continue
			}
			if entry.UncompressedSize64 > uint64(limits.MaxEntryBytes) || entry.UncompressedSize64 > uint64(limits.MaxTotalBytes-total-bundleTotal) {
				return nil, nil, fmt.Errorf("combined supplemental import exceeds an uncompressed byte limit")
			}
			bundleTotal += int64(entry.UncompressedSize64)
		}
		files, err := archiveingest.ReadZIP(body, archiveingest.Options{Limits: limits})
		if err != nil {
			return nil, nil, fmt.Errorf("supplemental ZIP is unsafe or exceeds an archive limit: %w", err)
		}
		if len(files) == 0 {
			return nil, nil, fmt.Errorf("supplemental ZIP contains no dependency or source files")
		}
		bundle := enterpriseSupplementalArchiveBundle{SourceName: name, SizeBytes: int64(len(body)), SHA256: supplementalArchiveSHA256(body)}
		for _, file := range files {
			switch strings.ToLower(path.Ext(file.Name)) {
			case ".usr", ".usz":
				return nil, nil, fmt.Errorf("supplemental ZIP contains a script project; import it separately instead of attaching it as dependencies")
			case ".zip":
				return nil, nil, fmt.Errorf("nested supplemental ZIPs are ambiguous; attach their dependency files directly")
			}
			archivePath := file.Name
			if commonRoot != "" {
				archivePath = commonRoot + "/" + file.Name
			}
			if _, err := archiveingest.ValidateFilePath(archivePath, limits.MaxDepth); err != nil {
				return nil, nil, fmt.Errorf("merged supplemental member exceeds the archive path limit")
			}
			key := supplementalArchivePathKey(archivePath)
			if _, collision := primaryNames[key]; collision {
				return nil, nil, fmt.Errorf("supplemental ZIP member collides with primary source; attach an explicit replacement file separately")
			}
			if _, collision := seen[key]; collision {
				return nil, nil, fmt.Errorf("supplemental ZIP members contain conflicting paths")
			}
			seen[key] = archivePath
			out = append(out, enterpriseSupplementalArchiveFile{Name: archivePath, Data: file.Data, FromBundle: true})
			bundle.Files = append(bundle.Files, enterpriseSupplementalArchiveMember{
				SourcePath: file.Name, ArchivePath: archivePath, LogicalPath: file.Name,
				SizeBytes: int64(len(file.Data)), SHA256: supplementalArchiveSHA256(file.Data),
			})
		}
		total += bundleTotal
		if manifest == nil {
			manifest = &enterpriseSupplementalArchiveManifest{Contract: "apexload.enterprise.supplemental_archives.v1", PrimarySizeBytes: int64(len(base)), PrimarySHA256: supplementalArchiveSHA256(base)}
		}
		manifest.Bundles = append(manifest.Bundles, bundle)
	}
	return out, manifest, nil
}

func supplementalArchivePathKey(value string) string {
	return cases.Fold().String(norm.NFC.String(value))
}

func supplementalArchiveSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func preserveEnterpriseSupplementalArchiveManifest(result *enterprisearchive.ConvertResult, manifest *enterpriseSupplementalArchiveManifest) {
	if result == nil || manifest == nil {
		return
	}
	for _, script := range importDraftScriptPointers(result.Script, result.Scripts) {
		ensureScriptImportPreserved(script, "enterprise_script_archive")[enterpriseSupplementalArchiveManifestKey] = manifest
	}
	if result.ReviewArtifact != nil {
		if result.ReviewArtifact.Metadata == nil {
			result.ReviewArtifact.Metadata = make(map[string]any)
		}
		result.ReviewArtifact.Metadata[enterpriseSupplementalArchiveManifestKey] = manifest
	}
}
