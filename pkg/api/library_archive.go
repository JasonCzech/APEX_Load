package api

// library_archive.go — split from library_federation.go (same package, behavior-preserving).

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/zipbounds"
	"github.com/jasonczech/apexload/pkg/storage"
)

type libraryArchiveImport struct {
	ZipPath      string
	SizeBytes    int64
	ManifestHash string
}

const libraryArchiveMultipartMetadataBytes = int64(20 << 20)

const (
	maxLibraryArchiveEntries               = 4_096
	maxLibraryArchiveCentralDirectoryBytes = 8 << 20
	maxLibraryArchiveEntryNameBytes        = 1 << 10
	maxLibraryArchiveEntryExtraBytes       = 4 << 10
	maxLibraryArchiveEntryCommentBytes     = 1 << 10
)

type libraryArchiveRequestReader struct {
	reader    io.Reader
	remaining int64
}

func (r *libraryArchiveRequestReader) Read(buffer []byte) (int, error) {
	if r.remaining <= 0 {
		probe := []byte{0}
		if n, err := r.reader.Read(probe); n > 0 || err == nil {
			return 0, fmt.Errorf("multipart archive import exceeds total request limit")
		} else {
			return 0, err
		}
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	n, err := r.reader.Read(buffer)
	r.remaining -= int64(n)
	return n, err
}

type libraryArchiveRequestBody struct {
	io.Reader
	io.Closer
}

func readLibraryArchiveImportRequest(r *http.Request) (LibraryImportRequest, *libraryArchiveImport, func(), error) {
	originalBody := r.Body
	requestLimit := libraryArchiveMaxBytes()
	if requestLimit > int64(^uint64(0)>>1)-libraryArchiveMultipartMetadataBytes {
		requestLimit = int64(^uint64(0) >> 1)
	} else {
		requestLimit += libraryArchiveMultipartMetadataBytes
	}
	r.Body = &libraryArchiveRequestBody{
		Reader: &libraryArchiveRequestReader{reader: originalBody, remaining: requestLimit},
		Closer: originalBody,
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return LibraryImportRequest{}, nil, nil, fmt.Errorf("invalid multipart archive import: %w", err)
	}
	var req LibraryImportRequest
	req.ProjectID = strings.TrimSpace(r.URL.Query().Get("project_id"))
	var bundleSeen bool
	var projectSeen bool
	var archiveSeen bool
	var archivePath string
	var archiveSize int64
	cleanup := func() {
		if archivePath != "" {
			_ = os.Remove(archivePath)
		}
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return LibraryImportRequest{}, nil, nil, err
		}
		switch part.FormName() {
		case "bundle":
			if bundleSeen {
				_ = part.Close()
				cleanup()
				return LibraryImportRequest{}, nil, nil, fmt.Errorf("duplicate bundle part")
			}
			if err := json.NewDecoder(io.LimitReader(part, 16*1024*1024)).Decode(&req.Bundle); err != nil {
				_ = part.Close()
				cleanup()
				return LibraryImportRequest{}, nil, nil, fmt.Errorf("invalid bundle part: %w", err)
			}
			bundleSeen = true
		case "project_id":
			if projectSeen {
				_ = part.Close()
				cleanup()
				return LibraryImportRequest{}, nil, nil, fmt.Errorf("duplicate project_id part")
			}
			projectSeen = true
			data, err := io.ReadAll(io.LimitReader(part, 4096))
			if err != nil {
				cleanup()
				return LibraryImportRequest{}, nil, nil, err
			}
			formProject := strings.TrimSpace(string(data))
			switch {
			case req.ProjectID == "":
				// No query project_id (only reachable with auth disabled, since the
				// route's project resolver otherwise rejects a missing query value):
				// accept the form value.
				req.ProjectID = formProject
			case formProject != "" && formProject != req.ProjectID:
				// Authorization was checked against the query project_id; a form value
				// that disagrees would let a caller authorized for project A import into
				// project B. Reject the mismatch instead of letting the form override
				// the authorized project.
				cleanup()
				return LibraryImportRequest{}, nil, nil, fmt.Errorf("project_id form field %q does not match the authorized project_id query parameter %q", formProject, req.ProjectID)
			}
		case "archive":
			if archiveSeen {
				_ = part.Close()
				cleanup()
				return LibraryImportRequest{}, nil, nil, fmt.Errorf("duplicate archive part")
			}
			archiveSeen = true
			tmp, err := os.CreateTemp("", "apexload-library-import-*.zip")
			if err != nil {
				cleanup()
				return LibraryImportRequest{}, nil, nil, err
			}
			archivePath = tmp.Name()
			archiveSize, err = copyWithMaxBytes(tmp, part, libraryArchiveMaxBytes())
			closeErr := tmp.Close()
			if err != nil {
				cleanup()
				return LibraryImportRequest{}, nil, nil, err
			}
			if closeErr != nil {
				cleanup()
				return LibraryImportRequest{}, nil, nil, closeErr
			}
		}
		if err := part.Close(); err != nil {
			cleanup()
			return LibraryImportRequest{}, nil, nil, err
		}
	}
	if !bundleSeen {
		cleanup()
		return LibraryImportRequest{}, nil, nil, fmt.Errorf("bundle part is required")
	}
	if libraryBundleHasStreamedArchive(&req.Bundle) && archivePath == "" {
		cleanup()
		return LibraryImportRequest{}, nil, nil, fmt.Errorf("archive part is required for streamed result imports")
	}
	if streamedLibraryResultCount(&req.Bundle) > 1 {
		cleanup()
		return LibraryImportRequest{}, nil, nil, fmt.Errorf("a streamed archive import may contain only one result item")
	}
	if archivePath == "" {
		return req, nil, cleanup, nil
	}
	return req, &libraryArchiveImport{ZipPath: archivePath, SizeBytes: archiveSize}, cleanup, nil
}

func streamedLibraryResultCount(bundle *LibraryBundle) int {
	if bundle == nil {
		return 0
	}
	count := 0
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) == "result" && item.Result != nil && strings.EqualFold(item.Result.ArchiveTransfer, "stream") {
			count++
		}
	}
	return count
}

func archiveImportSize(archiveImport *libraryArchiveImport) int64 {
	if archiveImport == nil {
		return 0
	}
	return archiveImport.SizeBytes
}

func libraryBundleHasStreamedArchive(bundle *LibraryBundle) bool {
	if bundle == nil {
		return false
	}
	for _, item := range bundle.Items {
		if normalizeLibraryKind(item.Kind) == "result" && item.Result != nil && strings.EqualFold(item.Result.ArchiveTransfer, "stream") {
			return true
		}
	}
	return false
}

func libraryArchiveMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_LIBRARY_ARCHIVE_MAX_BYTES"))
	if raw == "" {
		return defaultLibraryArchiveMaxBytes
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return defaultLibraryArchiveMaxBytes
	}
	return value
}

func copyWithMaxBytes(dst io.Writer, src io.Reader, maxBytes int64) (int64, error) {
	if maxBytes <= 0 {
		maxBytes = defaultLibraryArchiveMaxBytes
	}
	limited := &io.LimitedReader{R: src, N: maxBytes + 1}
	written, err := io.Copy(dst, limited)
	if err != nil {
		return written, err
	}
	if written > maxBytes {
		return written, fmt.Errorf("archive exceeds maximum size of %d bytes", maxBytes)
	}
	return written, nil
}

func unzipArchiveFile(zipPath string, destination string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = defaultLibraryArchiveMaxBytes
	}
	if err := zipbounds.ValidateFile(zipPath, zipbounds.Limits{
		MaxArchiveBytes:          maxBytes,
		MaxEntries:               maxLibraryArchiveEntries,
		MaxCentralDirectoryBytes: maxLibraryArchiveCentralDirectoryBytes,
		MaxNameBytes:             maxLibraryArchiveEntryNameBytes,
		MaxExtraBytes:            maxLibraryArchiveEntryExtraBytes,
		MaxCommentBytes:          maxLibraryArchiveEntryCommentBytes,
	}); err != nil {
		return fmt.Errorf("validate archive zip structure: %w", err)
	}
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open archive zip: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > maxLibraryArchiveEntries {
		return fmt.Errorf("archive contains %d entries, limit is %d", len(reader.File), maxLibraryArchiveEntries)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	var total int64
	seen := make(map[string]struct{}, len(reader.File))
	for _, file := range reader.File {
		cleanName := filepath.Clean(file.Name)
		if cleanName == "." || strings.Contains(file.Name, "\\") || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanName) {
			return fmt.Errorf("archive contains unsafe path %q", file.Name)
		}
		if _, duplicate := seen[cleanName]; duplicate {
			return fmt.Errorf("archive contains duplicate path %q", file.Name)
		}
		seen[cleanName] = struct{}{}
		target := filepath.Join(destination, cleanName)
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = src.Close()
			return err
		}
		remaining := maxBytes - total
		if remaining <= 0 {
			_ = dst.Close()
			_ = src.Close()
			return fmt.Errorf("archive exceeds maximum size of %d bytes", maxBytes)
		}
		written, copyErr := copyWithMaxBytes(dst, src, remaining)
		closeDstErr := dst.Close()
		closeSrcErr := src.Close()
		total += written
		if copyErr != nil {
			return copyErr
		}
		if closeDstErr != nil {
			return closeDstErr
		}
		if closeSrcErr != nil {
			return closeSrcErr
		}
		if total > maxBytes {
			return fmt.Errorf("archive exceeds maximum size of %d bytes", maxBytes)
		}
	}
	return nil
}

func importLegacyArchiveZipB64(encoded string, destination string, maxBytes int64) error {
	tmp, err := os.CreateTemp("", "apexload-library-legacy-*.zip")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	_, err = copyWithMaxBytes(tmp, base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)), maxBytes)
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return unzipArchiveFile(tmpPath, destination, maxBytes)
}

func libraryImportedArchiveDir(importID string) string {
	return filepath.Join(resolveArchiveRoot(), "library-imports", strings.TrimSpace(importID))
}

func archiveDirectorySize(dir string) int64 {
	var total int64
	if strings.TrimSpace(dir) == "" {
		return 0
	}
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil {
			total += info.Size()
		} else if info, statErr := os.Stat(path); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func hashArchiveManifest(record *storage.RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	if strings.TrimSpace(record.ManifestPath) != "" {
		return hashArchiveManifestPath(record.ManifestPath)
	}
	if strings.TrimSpace(record.ArchivePath) != "" {
		return hashArchiveManifestPath(filepath.Join(record.ArchivePath, "manifest.json"))
	}
	return ""
}

func hashArchiveManifestPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	digest, err := sha256FileContext(context.Background(), path)
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}
