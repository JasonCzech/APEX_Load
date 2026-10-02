package api

// handlers_import_enterprise — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

var enterpriseImportGate = make(chan struct{}, 2)

func acquireEnterpriseImportSlot(r *http.Request, w http.ResponseWriter) bool {
	select {
	case enterpriseImportGate <- struct{}{}:
		return true
	default:
		writeError(w, http.StatusTooManyRequests, "enterprise archive conversion capacity is busy")
		return false
	case <-r.Context().Done():
		writeError(w, http.StatusRequestTimeout, "enterprise archive conversion canceled")
		return false
	}
}

func releaseEnterpriseImportSlot() { <-enterpriseImportGate }

// mergedArchiveEntryKey normalizes an archive entry name for MATCHING only.
// The original name is what gets written back out.
func mergedArchiveEntryKey(name string) string {
	cleaned := path.Clean(strings.TrimPrefix(name, "./"))
	if cleaned == "." || cleaned == "" {
		return ""
	}
	return cleaned
}

// maxSupplementalArchiveFiles bounds how many extra sources one import may carry.
const maxSupplementalArchiveFiles = 64

// hasWindowsDriveLetterPrefix reports whether name begins with a drive
// specifier such as "C:" that would escape a relative archive path.
func hasWindowsDriveLetterPrefix(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	c := name[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func (s *Server) handleImportEnterpriseArchive(w http.ResponseWriter, r *http.Request) {
	if !acquireEnterpriseImportSlot(r, w) {
		return
	}
	defer releaseEnterpriseImportSlot()
	importReq, ok := readEnterpriseArchiveImportRequest(w, r)
	if !ok {
		return
	}
	if s.writeEnterpriseArchiveCollectionResponse(w, r, importReq) {
		return
	}
	// Run the conversion
	result, err := enterprisearchive.ConvertZip(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	for _, script := range result.Scripts {
		if script != nil {
			script.ProjectID = normalizeProjectID(importReq.ProjectID)
		}
	}
	attachSalesforceReviewToEnterpriseArchiveResult(result, importReq)
	preserveEnterpriseArchiveCollectionMember(result, importReq.ArchiveMember, importReq.ArchiveMemberSize)
	preserveEnterpriseSupplementalArchiveManifest(result, importReq.SupplementalManifest)

	s.writeEnterpriseArchiveImportResponse(w, r, http.StatusOK, result, importReq.ReadinessPolicy, importReq.ExpireDraft)
}

func (s *Server) handleValidateEnterpriseArchiveImport(w http.ResponseWriter, r *http.Request) {
	if !acquireEnterpriseImportSlot(r, w) {
		return
	}
	defer releaseEnterpriseImportSlot()
	importReq, ok := readEnterpriseArchiveImportRequest(w, r)
	if !ok {
		return
	}
	if s.writeEnterpriseArchiveCollectionResponse(w, r, importReq) {
		return
	}

	result, err := enterprisearchive.ConvertZip(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	for _, script := range result.Scripts {
		if script != nil {
			script.ProjectID = normalizeProjectID(importReq.ProjectID)
		}
	}
	attachSalesforceReviewToEnterpriseArchiveResult(result, importReq)
	preserveEnterpriseArchiveCollectionMember(result, importReq.ArchiveMember, importReq.ArchiveMemberSize)
	preserveEnterpriseSupplementalArchiveManifest(result, importReq.SupplementalManifest)

	s.writeEnterpriseArchiveImportResponse(w, r, http.StatusOK, result, importReq.ReadinessPolicy, importReq.ExpireDraft)
}

func attachSalesforceReviewToEnterpriseArchiveResult(result *enterprisearchive.ConvertResult, importReq *enterpriseArchiveImportRequest) {
	if result == nil || importReq == nil {
		return
	}
	scripts := result.Scripts
	if len(scripts) == 0 && result.Script != nil {
		scripts = []*dsl.Script{result.Script}
	}
	if !importReq.SalesforceNative && len(importReq.HARData) == 0 {
		return
	}
	reviewScript := result.Script
	if reviewScript == nil && len(scripts) > 0 {
		reviewScript = scripts[0]
	}
	if reviewScript == nil {
		return
	}
	review, err := salesforce.Analyze(reviewScript, salesforce.AnalyzeOptions{
		BuildNative: importReq.SalesforceNative,
		HARData:     importReq.HARData,
	})
	if err != nil {
		result.Warnings = append(result.Warnings, "Salesforce review failed: "+err.Error())
		return
	}
	if review == nil || (!review.Detected && !importReq.SalesforceNative) {
		return
	}
	reviewForResponse := salesforceReviewForResponse(review)
	if importReq.SalesforceNative && review.NativePreview != nil && review.NativePreview.Script != nil {
		preview := review.NativePreview.Script
		preview.ProjectID = normalizeProjectID(importReq.ProjectID)
		attachSourceReviewToScript(preview, "salesforce", reviewScript.Name, result.SourceReview)
		if preview.Metadata != nil && preview.Metadata.Import != nil {
			preview.Metadata.Import.Source = "salesforce"
		}
		ensureScriptImportPreserved(preview, "salesforce")["salesforce_review"] = reviewForResponse
		result.Script = preview
		result.Scripts = nil
	}
	for _, script := range scripts {
		preserved := ensureScriptImportPreserved(script, "enterprise_script_archive")
		preserved["salesforce_review"] = reviewForResponse
	}
}

func (s *Server) handleEnterpriseArchiveSupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "enterprise_script_archive")
}

// normalizeCombinedJavaVuserImport keeps the public import envelope stable for
// Java archives: script and the sole scripts item always identify the same
// combined draft. Import fidelity is computed by the converter, while runtime
// availability is enforced independently by the Java Vuser launch preflight.
func normalizeCombinedJavaVuserImport(result *enterprisearchive.ConvertResult) {
	if result == nil || result.DetectedProtocol == nil ||
		result.DetectedProtocol.ImportMode != enterprisearchive.ImportModeExternalRunnerRequired {
		return
	}

	combined := result.Script
	if !isGovernedJavaVuserDraft(combined) {
		combined = nil
		for _, candidate := range result.Scripts {
			if isGovernedJavaVuserDraft(candidate) {
				combined = candidate
				break
			}
		}
	}
	if combined == nil {
		return
	}

	result.Script = combined
	result.Scripts = []*dsl.Script{combined}
}

func readEnterpriseArchiveImportRequest(w http.ResponseWriter, r *http.Request) (*enterpriseArchiveImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, enterpriseArchiveImportMaxBodyBytes)

	contentType := r.Header.Get("Content-Type")
	var data []byte
	var harData []byte

	if strings.HasPrefix(contentType, "application/zip") ||
		strings.HasPrefix(contentType, "application/x-zip-compressed") ||
		strings.HasPrefix(contentType, "application/octet-stream") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return nil, false
		}
		data = body
	} else {
		if err := r.ParseMultipartForm(importMultipartMemoryBytes); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart form (max 100MB): "+err.Error())
			return nil, false
		}
		defer r.MultipartForm.RemoveAll()
		if _, hasArchive := r.MultipartForm.File["file"]; hasArchive {
			for field := range r.MultipartForm.File {
				if strings.HasPrefix(field, "file:") {
					writeError(w, http.StatusBadRequest, "upload either a ZIP archive or a VuGen folder, not both")
					return nil, false
				}
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				writeError(w, http.StatusBadRequest, "failed to read 'file' field: "+err.Error())
				return nil, false
			}
			defer file.Close()
			data, err = io.ReadAll(file)
			if err != nil {
				writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
				return nil, false
			}
		} else {
			var err error
			data, err = zipEnterpriseFolderUpload(r.MultipartForm.File)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid VuGen folder: "+err.Error())
				return nil, false
			}
		}
		if harFile, _, err := r.FormFile("har_file"); err == nil {
			defer harFile.Close()
			harBody, readErr := io.ReadAll(harFile)
			if readErr != nil {
				writeError(w, http.StatusBadRequest, "failed to read uploaded HAR file: "+readErr.Error())
				return nil, false
			}
			harData = harBody
		}
	}

	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file uploaded")
		return nil, false
	}

	// Deprecated compatibility input. Java archives now always produce one
	// governed java_vuser draft, so true and false intentionally behave alike.
	_ = parseBoolDefault(firstNonEmpty(r.FormValue("allow_java_vuser_runner"), r.URL.Query().Get("allow_java_vuser_runner")), true)
	// Verify ZIP magic bytes for early diagnosis
	if len(data) > 0 && len(data) < 4 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("file is too small (%d bytes); ZIP files must be at least 4 bytes", len(data)))
		return nil, false
	}
	if len(data) >= 2 && (data[0] != 0x50 || data[1] != 0x4b) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("file does not appear to be a valid ZIP archive (magic bytes: %02x %02x); expected PK", data[0], data[1]))
		return nil, false
	}
	archiveMember := firstNonEmpty(r.FormValue("archive_member"), r.URL.Query().Get("archive_member"))
	archiveMemberSize := 0
	if archiveMember != "" {
		selected, err := selectEnterpriseArchiveCollectionMember(data, archiveMember)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return nil, false
		}
		data = selected
		archiveMemberSize = len(selected)
	}

	supplementalData := make([][]byte, 0)
	supplementalNames := make([]string, 0)
	if r.MultipartForm != nil {
		supplementalHeaders := r.MultipartForm.File["supplemental_file"]
		if len(supplementalHeaders) > maxSupplementalArchiveFiles {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d supplemental_file entries are accepted", maxSupplementalArchiveFiles))
			return nil, false
		}
		for _, header := range supplementalHeaders {
			if header == nil {
				continue
			}
			// mime/multipart already reduces Filename to a base name, so this is a
			// defense-in-depth check rather than the primary traversal guard.
			name := path.Clean(strings.TrimSpace(header.Filename))
			name = strings.TrimPrefix(name, "./")
			if name == "." || name == "" || name == ".." || strings.HasPrefix(name, "../") ||
				strings.Contains(name, "\\") || strings.ContainsRune(name, 0) || hasWindowsDriveLetterPrefix(name) {
				writeError(w, http.StatusBadRequest, "supplemental_file names must be relative archive paths")
				return nil, false
			}
			if strings.HasPrefix(name, "/") {
				writeError(w, http.StatusBadRequest, "supplemental_file names must be relative archive paths")
				return nil, false
			}
			file, openErr := header.Open()
			if openErr != nil {
				writeError(w, http.StatusBadRequest, "failed to read supplemental_file: "+openErr.Error())
				return nil, false
			}
			body, readErr := io.ReadAll(io.LimitReader(file, int64(enterprisearchive.MaxArchiveEntryBytes)+1))
			_ = file.Close()
			if readErr != nil {
				writeError(w, http.StatusBadRequest, "failed to read supplemental_file: "+readErr.Error())
				return nil, false
			}
			if len(body) > enterprisearchive.MaxArchiveEntryBytes {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("supplemental_file %q exceeds the %d byte limit", name, enterprisearchive.MaxArchiveEntryBytes))
				return nil, false
			}
			if len(body) == 0 {
				continue
			}
			supplementalData = append(supplementalData, body)
			supplementalNames = append(supplementalNames, name)
		}
	}
	var supplementalManifest *enterpriseSupplementalArchiveManifest
	if len(supplementalData) > 0 {
		if _, collection, _ := readEnterpriseArchiveCollection(data); collection {
			writeError(w, http.StatusBadRequest, "select an archive_member before attaching supplemental sources to a script archive collection")
			return nil, false
		}
		mergedData, manifest, mergeErr := mergeEnterpriseArchiveZipWithSupplementalEvidence(data, supplementalNames, supplementalData)
		if mergeErr != nil {
			writeError(w, http.StatusBadRequest, "failed to merge supplemental_file entries into archive: "+mergeErr.Error())
			return nil, false
		}
		data = mergedData
		supplementalManifest = manifest
	}

	return &enterpriseArchiveImportRequest{
		Data:                 data,
		ArchiveMember:        archiveMember,
		ArchiveMemberSize:    archiveMemberSize,
		HARData:              harData,
		SupplementalData:     supplementalData,
		SupplementalName:     supplementalNames,
		SupplementalManifest: supplementalManifest,
		ProjectID:            authorizedImportProjectID(r),
		SalesforceNative:     parseBoolDefault(firstNonEmpty(r.FormValue("salesforce_native"), r.URL.Query().Get("salesforce_native")), false),
		ExpireDraft:          parseBoolDefault(firstNonEmpty(r.FormValue("expire_draft"), r.URL.Query().Get("expire_draft")), false),
		Opts: enterprisearchive.ConvertOptions{
			ScriptName:           r.FormValue("name"),
			BaseURL:              r.FormValue("base_url"),
			IncludeAssertions:    r.FormValue("include_assertions") == "true",
			PreserveTransactions: parseBoolDefault(r.FormValue("preserve_transactions"), true),
		},
		ReadinessPolicy: enterprisearchive.ParseRuntimeSettingMigrationPolicy(r.FormValue("migration_block_statuses")),
	}, true
}

// zipEnterpriseFolderUpload turns browser directory selection into the same
// bounded archive consumed by the existing converter. Logical paths travel in
// multipart field names because multipart filenames discard directory paths.
func zipEnterpriseFolderUpload(fields map[string][]*multipart.FileHeader) ([]byte, error) {
	var names []string
	headers := make(map[string]*multipart.FileHeader)
	for field, files := range fields {
		if !strings.HasPrefix(field, "file:") {
			continue
		}
		if len(files) != 1 || files[0] == nil {
			return nil, fmt.Errorf("each folder path must contain exactly one file")
		}
		name, err := url.PathUnescape(strings.TrimPrefix(field, "file:"))
		if err != nil {
			return nil, fmt.Errorf("invalid encoded folder path: %w", err)
		}
		names = append(names, name)
		headers[name] = files[0]
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("select a VuGen folder containing a .usr project file")
	}
	if len(names) > enterprisearchive.MaxArchiveEntries {
		return nil, fmt.Errorf("folder has %d files; limit is %d", len(names), enterprisearchive.MaxArchiveEntries)
	}
	sort.Strings(names)
	validated, err := archiveingest.ValidateFilePaths(names, 33)
	if err != nil {
		return nil, fmt.Errorf("unsafe or duplicate folder path: %w", err)
	}
	root := strings.SplitN(validated[0], "/", 2)[0]
	for _, name := range validated {
		if !strings.HasPrefix(name, root+"/") {
			return nil, fmt.Errorf("folder files must share one root directory")
		}
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	var total int64
	var projectFiles int
	for index, name := range validated {
		if strings.HasSuffix(strings.ToLower(name), ".usr") {
			projectFiles++
		}
		header := headers[names[index]]
		if header.Size > enterprisearchive.MaxArchiveEntryBytes {
			return nil, fmt.Errorf("%q exceeds the per-file size limit", name)
		}
		file, openErr := header.Open()
		if openErr != nil {
			return nil, fmt.Errorf("open %q: %w", name, openErr)
		}
		// Multipart bytes are already bounded. Store them without recompression
		// so repetitive parameter data cannot trip the ZIP bomb ratio guard.
		entry, createErr := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if createErr != nil {
			_ = file.Close()
			return nil, createErr
		}
		n, copyErr := io.Copy(entry, io.LimitReader(file, int64(enterprisearchive.MaxArchiveEntryBytes)+1))
		_ = file.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("read %q: %w", name, copyErr)
		}
		if n > enterprisearchive.MaxArchiveEntryBytes {
			return nil, fmt.Errorf("%q exceeds the per-file size limit", name)
		}
		total += n
		if total > enterprisearchive.MaxArchiveTotalBytes {
			return nil, fmt.Errorf("folder exceeds the total size limit")
		}
	}
	if projectFiles != 1 {
		return nil, fmt.Errorf("expected one .usr project file, found %d", projectFiles)
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// mergeEnterpriseArchiveZipWithSupplementalFiles rewrites the uploaded archive
// with the caller's supplemental sources merged in.
//
// It re-inflates the uploaded entries, so it MUST enforce the same
// decompression-bomb bounds ConvertZip does — otherwise attaching a single
// supplemental file routes an upload through an unguarded path and a ~100 MiB
// body can expand to ~100 GiB before ConvertZip ever sees it.
func mergeEnterpriseArchiveZipWithSupplementalFiles(baseZip []byte, names []string, contents [][]byte) ([]byte, error) {
	merged, _, err := mergeEnterpriseArchiveZipWithSupplementalEvidence(baseZip, names, contents)
	return merged, err
}

func mergeEnterpriseArchiveZipWithSupplementalEvidence(baseZip []byte, names []string, contents [][]byte) ([]byte, *enterpriseSupplementalArchiveManifest, error) {
	merged, manifest, err := prepareEnterpriseSupplementalArchives(baseZip, names, contents)
	if err != nil {
		return nil, nil, err
	}
	data, err := mergeEnterpriseArchiveZipWithPreparedFiles(baseZip, merged)
	if err != nil {
		return nil, nil, err
	}
	return data, manifest, nil
}

func mergeEnterpriseArchiveZipWithPreparedFiles(baseZip []byte, supplemental []enterpriseSupplementalArchiveFile) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(baseZip), int64(len(baseZip)))
	if err != nil {
		return nil, err
	}
	if len(reader.File) > enterprisearchive.MaxArchiveEntries {
		return nil, fmt.Errorf("archive contains %d entries, limit is %d", len(reader.File), enterprisearchive.MaxArchiveEntries)
	}
	// This function deliberately writes entries back under their ORIGINAL names
	// (see the comment on the copy loop below), so it cannot adopt the
	// normalizing archiveingest reader. Apply the shared path policy up front
	// instead: mergedArchiveEntryKey uses path.Clean, which preserves "..", so
	// without this a traversal entry would round-trip through the merge and
	// reach ConvertZip. Fail loudly rather than silently dropping the entry.
	for _, file := range reader.File {
		if file == nil || file.FileInfo().IsDir() {
			continue
		}
		// Validate the matching key, not the raw name: a leading "./" is a
		// benign and common zip spelling that mergedArchiveEntryKey already
		// strips, while ".." survives path.Clean and must be rejected.
		key := mergedArchiveEntryKey(file.Name)
		if key == "" {
			continue
		}
		if _, err := archiveingest.ValidateFilePath(key, 0); err != nil {
			return nil, fmt.Errorf("archive entry %q is not a safe relative path: %w", file.Name, err)
		}
	}

	overrides := make(map[string][]byte, len(supplemental))
	protected := make(map[string]bool, len(supplemental))
	for _, file := range supplemental {
		overrides[file.Name] = file.Data
		protected[file.Name] = file.FromBundle
	}

	// Browser multipart always reduces a filename to its base name, so a
	// supplemental "Action.java" must be matched against the base name of nested
	// entries such as "MyScript/Action.java". Appending it at the archive root
	// instead would make ConvertZip see two entries that normalize to the same
	// name and abort the whole import.
	baseNameCounts := map[string]int{}
	for _, file := range reader.File {
		if file == nil || file.FileInfo().IsDir() {
			continue
		}
		if cleaned := mergedArchiveEntryKey(file.Name); cleaned != "" {
			baseNameCounts[strings.ToLower(path.Base(cleaned))]++
		}
	}

	// Resolve supplemental names to archive entries in a DETERMINISTIC order:
	// ranging a map made the winner of a collision depend on Go's random map
	// iteration. replacements is keyed by the entry's ORIGINAL name because that
	// is what gets written back out.
	supplementalNamesSorted := make([]string, 0, len(overrides))
	for name := range overrides {
		supplementalNamesSorted = append(supplementalNamesSorted, name)
	}
	sort.Strings(supplementalNamesSorted)

	replacements := make(map[string][]byte, len(overrides))
	claimedBy := map[string]string{}
	for _, name := range supplementalNamesSorted {
		if protected[name] {
			continue // Bundle members are additions; they never replace primary source.
		}
		target := ""
		for _, file := range reader.File {
			if file == nil || file.FileInfo().IsDir() {
				continue
			}
			cleaned := mergedArchiveEntryKey(file.Name)
			if cleaned == "" {
				continue
			}
			if cleaned == name {
				target = file.Name
				break
			}
			// Only retarget when the base name is unambiguous inside the archive.
			if strings.EqualFold(path.Base(cleaned), path.Base(name)) &&
				baseNameCounts[strings.ToLower(path.Base(name))] == 1 {
				target = file.Name
			}
		}
		if target == "" {
			continue
		}
		// Two supplemental names that differ only in case resolve to the same
		// entry. Silently dropping one would depend on ordering, so reject.
		if previous, taken := claimedBy[target]; taken {
			return nil, fmt.Errorf("supplemental_file %q and %q both target archive entry %q", previous, name, target)
		}
		claimedBy[target] = name
		replacements[target] = overrides[name]
		delete(overrides, name)
	}

	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	fail := func(err error) ([]byte, error) {
		_ = writer.Close()
		return nil, err
	}

	totalBytes := 0
	writeEntry := func(name string, body []byte, method uint16) error {
		if len(body) > enterprisearchive.MaxArchiveEntryBytes {
			return fmt.Errorf("archive entry %q is %d bytes, limit is %d", name, len(body), enterprisearchive.MaxArchiveEntryBytes)
		}
		totalBytes += len(body)
		if totalBytes > enterprisearchive.MaxArchiveTotalBytes {
			return fmt.Errorf("archive exceeds the %d uncompressed byte limit", enterprisearchive.MaxArchiveTotalBytes)
		}
		dst, createErr := writer.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if createErr != nil {
			return createErr
		}
		_, writeErr := dst.Write(body)
		return writeErr
	}

	for _, file := range reader.File {
		if file == nil || file.FileInfo().IsDir() {
			continue
		}
		if mergedArchiveEntryKey(file.Name) == "" {
			continue
		}
		// Preserve the ORIGINAL entry name. Rewriting it to the cleaned form
		// changes which leading segment ConvertZip strips (it removes the first
		// path segment), so every nested file would silently move up one level
		// purely because a supplemental file was attached.
		fileName := file.Name
		body, ok := replacements[fileName]
		if !ok {
			src, openErr := file.Open()
			if openErr != nil {
				return fail(openErr)
			}
			// Bounded read: never materialize more than one entry's limit, so a
			// zero-filled bomb cannot grow the heap past the cap.
			readBody, readErr := io.ReadAll(io.LimitReader(src, int64(enterprisearchive.MaxArchiveEntryBytes)+1))
			_ = src.Close()
			if readErr != nil {
				return fail(readErr)
			}
			body = readBody
		}
		if err := writeEntry(fileName, body, file.Method); err != nil {
			return fail(err)
		}
	}

	// Any supplemental file that matched no existing entry is added at the root.
	remaining := make([]string, 0, len(overrides))
	for name := range overrides {
		remaining = append(remaining, name)
	}
	sort.Strings(remaining)
	for _, name := range remaining {
		if err := writeEntry(name, overrides[name], zip.Deflate); err != nil {
			return fail(err)
		}
	}

	if closeErr := writer.Close(); closeErr != nil {
		return nil, closeErr
	}
	return out.Bytes(), nil
}

func (s *Server) writeEnterpriseArchiveImportResponse(w http.ResponseWriter, r *http.Request, status int, result *enterprisearchive.ConvertResult, readinessPolicy enterprisearchive.RuntimeSettingMigrationPolicy, expireDraft bool) {
	writeScriptImportResponse(w, status, s.newEnterpriseArchiveImportResponse(r, result, readinessPolicy, expireDraft))
}

func (s *Server) newEnterpriseArchiveImportResponse(r *http.Request, result *enterprisearchive.ConvertResult, readinessPolicy enterprisearchive.RuntimeSettingMigrationPolicy, expireDraft bool) map[string]any {
	if result == nil {
		return s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{})
	}
	protectedRequirements := result.ProtectedRequirements.Take()
	normalizeCombinedJavaVuserImport(result)
	unsupportedElements := uniqueSortedStrings(result.Stats.UnsupportedFuncs)
	warnings := enterpriseArchiveImportWarnings(result)
	summary := buildEnterpriseArchiveImportSummary(result, unsupportedElements, readinessPolicy)
	persistEnterpriseArchiveImportSummary(result, summary)
	reviewSections := buildEnterpriseArchiveImportReviewSections(warnings, result, unsupportedElements, summary)
	repairSuggestions := buildEnterpriseArchiveImportRepairSuggestions(result, unsupportedElements, summary)
	protocolCompat := buildEnterpriseArchiveProtocolCompatReport(result, unsupportedElements, summary)
	persistProtocolCompatReport(result.Script, "enterprise_script_archive", protocolCompat)
	for _, script := range result.Scripts {
		report := protocolCompat
		if len(result.Scripts) > 1 {
			// Import formats that intentionally return multiple scripts get
			// per-script counts instead of the aggregate first-script report.
			// Java imports are normalized to one combined script above.
			report = protocolCompatReportForScript(protocolCompat, script)
		}
		persistProtocolCompatReport(script, "enterprise_script_archive", report)
	}

	scripts := result.Scripts
	if len(scripts) == 0 && result.Script != nil {
		scripts = []*dsl.Script{result.Script}
	}
	parameterization := buildImportParameterization(result.Script, nil)
	if len(scripts) > 1 {
		parameterization = buildMultiScriptParameterization(scripts)
	}
	// The archive handler scopes every produced script before it gets here, so
	// the draft carries that same scope back rather than re-resolving it: this
	// writer also serves the validate route, which has no request-time scope.
	draft := importDraft{
		Source:       "enterprise_script_archive",
		ProjectID:    enterpriseArchiveDraftProjectID(scripts),
		Script:       result.Script,
		Scripts:      scripts,
		SourceReview: summary.SourceReview,
		Sections:     reviewSections,
		Warnings:     warnings,
	}
	augmented := draft.augment(r)
	warnings = augmented.Warnings

	return s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Result:                 result,
		Scripts:                scripts,
		Script:                 result.Script,
		Warnings:               warnings,
		Stats:                  result.Stats,
		UnsupportedElements:    unsupportedElements,
		ReviewSections:         augmented.Sections,
		RepairSuggestions:      repairSuggestions,
		Summary:                summary,
		DetectedProtocol:       result.DetectedProtocol,
		FidelityReport:         summary.FidelityReport,
		ReviewArtifact:         result.ReviewArtifact,
		SourceReview:           summary.SourceReview,
		TrueClient2Compat:      summary.TrueClient2CompatReport,
		ProtocolCompat:         protocolCompat,
		Parameterization:       parameterization,
		ProtectedRequirements:  protectedRequirements,
		Preflight:              augmented.Preflight,
		DisableDraftExpiration: !expireDraft,
	})
}

// enterpriseArchiveDraftProjectID is the scope already applied to the archive's
// scripts. Every script in one archive import shares it.
func enterpriseArchiveDraftProjectID(scripts []*dsl.Script) string {
	for _, script := range scripts {
		if script != nil && script.ProjectID != "" {
			return script.ProjectID
		}
	}
	return ""
}

func enterpriseArchiveImportWarnings(result *enterprisearchive.ConvertResult) []string {
	if result == nil {
		return nil
	}
	warnings := append([]string(nil), result.Warnings...)
	return uniqueSortedStrings(warnings)
}

func buildEnterpriseArchiveImportRepairSuggestions(result *enterprisearchive.ConvertResult, unsupportedElements []string, summary enterpriseArchiveImportSummary) []scriptImportRepairSuggestion {
	suggestions := make([]scriptImportRepairSuggestion, 0)
	if result == nil {
		return suggestions
	}

	for _, element := range unsupportedElements {
		normalized := strings.ToLower(element)
		description := "Review the preserved source construct and replace it with equivalent APEX Load DSL behavior before relying on this script."
		action := "Open the generated script in the visual editor, inspect preserved metadata, and either remove the unsupported construct or model it with supported actions."
		if strings.Contains(normalized, "evaluate javascript") {
			description = "This source JavaScript statement could not be decomposed into a supported browser action."
			action = "Replace the JavaScript with browser_click, browser_fill, browser_select, browser_wait, browser_swipe, or a supported assertion, then validate with browser replay."
			if enterpriseArchiveWarningsContain(result, "window.scrollto(") {
				description = "This source JavaScript scroll statement could not be decomposed safely into a supported browser action."
				action = "Replace the JavaScript with browser_swipe and any required browser_wait actions, then validate the resulting scroll behavior with browser replay."
			}
		}
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "unsupported:" + stableReviewKey(element),
			Category:    "unsupported",
			Severity:    "blocker",
			Title:       "Replace unsupported " + element,
			Description: description,
			Action:      action,
		})
	}

	if result.Script != nil && result.Script.Protocol == "browser" {
		suggestions = append(suggestions, browserImportRepairSuggestions(result.Script)...)
	}

	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		suggestions = append(suggestions, trueClientWeb2RepairSuggestions(summary)...)
	}

	if result.DetectedProtocol != nil && result.DetectedProtocol.ImportMode != enterprisearchive.ImportModeExecutable {
		combinedJavaVuser := result.DetectedProtocol.ImportMode == enterprisearchive.ImportModeExternalRunnerRequired &&
			isGovernedJavaVuserDraft(result.Script)
		fidelityReport := result.FidelityReport
		if fidelityReport == nil {
			fidelityReport = summary.FidelityReport
		}
		hasNamedFidelityBlockers := fidelityReport != nil && len(fidelityReport.Blockers) > 0
		javaVuserApprovalReady := combinedJavaVuser &&
			fidelityReport != nil &&
			fidelityReport.PromotionStatus == enterprisearchive.ImportPromotionPromotable &&
			len(fidelityReport.Blockers) == 0
		if javaVuserApprovalReady {
			suggestions = append(suggestions, scriptImportRepairSuggestion{
				ID:          "protocol:java-vuser-approval",
				Category:    "protocol",
				Severity:    "warning",
				Title:       "Approve the combined Java Vuser draft",
				Description: "The Java archive is represented by one external-runner draft rather than split native lanes. This runner posture is not conversion loss, but launch requires authoring approval and a compatible governed JVM runner.",
				Action:      "Save the combined draft to an authoring session, review and approve it, then enable a runner advertising java_vuser_persistent_lifecycle_v1 and java_vuser_session_frame_token_v1 before launch.",
			})
		} else if combinedJavaVuser && hasNamedFidelityBlockers {
			suggestions = append(suggestions, scriptImportRepairSuggestion{
				ID:          "protocol:java-vuser-fidelity",
				Category:    "protocol",
				Severity:    "blocker",
				Title:       "Resolve combined Java Vuser fidelity blockers",
				Description: "The archive remains preserved as one combined Java Vuser draft, but its source or lifecycle fidelity is blocked and the draft is not ready for authoring approval.",
				Action:      "Save the combined draft as a review artifact, resolve every named fidelity blocker, then re-import and approve the promotable draft.",
			})
		} else if combinedJavaVuser {
			suggestions = append(suggestions, scriptImportRepairSuggestion{
				ID:          "protocol:java-vuser-review",
				Category:    "protocol",
				Severity:    "warning",
				Title:       "Review combined Java Vuser draft readiness",
				Description: "The archive remains represented as one combined Java Vuser draft. No named fidelity blockers were reported, but approval readiness still depends on preserved runtime and governance evidence.",
				Action:      "Save the draft to an authoring session, review preserved runtime/settings evidence, and approve once migration and governed-runner prerequisites are satisfied.",
			})
		} else {
			suggestions = append(suggestions, scriptImportRepairSuggestion{
				ID:          "protocol:review-artifact",
				Category:    "protocol",
				Severity:    "blocker",
				Title:       "Resolve protocol import gate",
				Description: fmt.Sprintf("%s was classified as %s and is not ready to save as an executable APEX Load script.", firstNonEmpty(result.DetectedProtocol.DisplayName, result.DetectedProtocol.CanonicalSourceProtocol), result.DetectedProtocol.ImportMode),
				Action:      "Save the review artifact, collect the required protocol evidence, and rerun import after the protocol is confirmed or a runner bridge is available.",
			})
		}
	}

	if len(summary.RuntimeFiles) > 0 || summary.MappedRuntimeFile != "" {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "runtime:review-preserved-settings",
			Category:    "runtime",
			Severity:    "info",
			Title:       "Review preserved runtime settings",
			Description: "The source archive included source runtime metadata that may not all execute natively in APEX Load yet.",
			Action:      "Compare the preserved runtime file and project settings against the generated APEX Load config before promoting the script.",
		})
	}

	if summary.MigrationReadiness != nil && summary.MigrationReadiness.Blocked {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "runtime:migration-readiness",
			Category:    "runtime",
			Severity:    "blocker",
			Title:       "Resolve blocking runtime settings before promotion",
			Description: "The selected migration block policy matched imported runtime settings that are not fully executable in APEX Load yet.",
			Action:      "Review the blocking runtime settings, adjust the generated APEX Load config where possible, or relax the migration block policy before saving the script.",
		})
	}

	if summary.SalesforceReview != nil && len(summary.SalesforceReview.Blockers) > 0 {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "salesforce:native-preview-blockers",
			Category:    "salesforce",
			Severity:    "blocker",
			Title:       "Resolve Salesforce native preview blockers",
			Description: "The exact replay script was imported, but the Salesforce native preview needs additional evidence or correlation review before promotion.",
			Action:      "Attach a HAR captured from the same source recording, review Salesforce transaction grouping and correlations, then rerun the native preview.",
		})
	}

	return suggestions
}

func enterpriseArchiveWarningsContain(result *enterprisearchive.ConvertResult, needle string) bool {
	if result == nil {
		return false
	}
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	for _, warning := range result.Warnings {
		if strings.Contains(strings.ToLower(warning), needle) {
			return true
		}
	}
	return false
}

func buildEnterpriseArchiveImportReviewSections(warnings []string, result *enterprisearchive.ConvertResult, unsupportedElements []string, summary enterpriseArchiveImportSummary) []scriptImportReviewSection {
	sections := make([]scriptImportReviewSection, 0, 4)
	if result == nil {
		return sections
	}

	if result.DetectedProtocol != nil {
		items := []string{
			"Source protocol: " + firstNonEmpty(result.DetectedProtocol.DisplayName, result.DetectedProtocol.CanonicalSourceProtocol),
			"Import mode: " + string(result.DetectedProtocol.ImportMode),
			"Confidence: " + string(result.DetectedProtocol.Confidence),
		}
		if result.DetectedProtocol.APEXLoadProtocol != "" {
			items = append(items, "APEX Load protocol: "+result.DetectedProtocol.APEXLoadProtocol)
		}
		if result.DetectedProtocol.Bundle != "" {
			items = append(items, "Bundle: "+result.DetectedProtocol.Bundle)
		}
		for _, signal := range result.DetectedProtocol.Signals {
			items = append(items, "Signal: "+signal)
		}
		for _, conflict := range result.DetectedProtocol.Conflicts {
			items = append(items, "Conflict: "+conflict)
		}
		severity := "info"
		if result.DetectedProtocol.ImportMode != enterprisearchive.ImportModeExecutable || len(result.DetectedProtocol.Conflicts) > 0 {
			severity = "warning"
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "detected_protocol",
			Title:    "Detected Source Protocol",
			Severity: severity,
			Summary:  "Protocol-aware routing classified this archive before conversion.",
			Items:    items,
		})
	}

	if result.ReviewArtifact != nil {
		items := []string{
			"Artifact ID: " + result.ReviewArtifact.ID,
			"Status: " + result.ReviewArtifact.Status,
		}
		for _, evidence := range result.ReviewArtifact.RequiredEvidence {
			items = append(items, "Required evidence: "+evidence)
		}
		for _, action := range result.ReviewArtifact.NextActions {
			items = append(items, "Next action: "+action)
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "review_artifact",
			Title:    "Protocol Review Artifact",
			Severity: "warning",
			Summary:  result.ReviewArtifact.Summary,
			Items:    items,
		})
	}

	if summary.FidelityReport != nil {
		severity := "info"
		if summary.FidelityReport.PromotionStatus == enterprisearchive.ImportPromotionBlocked || len(summary.FidelityReport.Blockers) > 0 {
			severity = "blocker"
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "import_fidelity",
			Title:    "Import Fidelity",
			Severity: severity,
			Summary:  enterpriseArchiveFidelitySummary(summary.FidelityReport),
			Items:    enterpriseArchiveFidelityItems(summary.FidelityReport),
		})
	}

	if len(warnings) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "warnings",
			Title:    "Conversion Warnings",
			Severity: "warning",
			Summary:  "Review converter warnings before saving or replaying the imported draft.",
			Items:    warnings,
		})
	}

	if len(unsupportedElements) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "unsupported_elements",
			Title:    "Unsupported Source Elements",
			Severity: "blocker",
			Summary:  "These source constructs were preserved for review but were not converted into executable APEX Load actions.",
			Items:    unsupportedElements,
		})
	}

	preservedItems := make([]string, 0)
	if summary.ProjectFile != "" {
		preservedItems = append(preservedItems, "Project file: "+summary.ProjectFile)
	}
	if summary.MappedRuntimeFile != "" {
		preservedItems = append(preservedItems, "Mapped runtime file: "+summary.MappedRuntimeFile)
	}
	for _, parameterFile := range summary.ParameterFiles {
		preservedItems = append(preservedItems, "Parameter file: "+parameterFile)
	}
	for _, runtimeFile := range summary.RuntimeFiles {
		preservedItems = append(preservedItems, "Runtime file: "+runtimeFile)
	}
	for _, projectSection := range summary.ProjectSections {
		preservedItems = append(preservedItems, "Project section: "+projectSection)
	}
	if len(preservedItems) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "preserved_metadata",
			Title:    "Preserved Source Metadata",
			Severity: "info",
			Summary:  "Source project metadata was saved with the generated script for audit and repair workflows.",
			Items:    preservedItems,
		})
	}

	if len(summary.RuntimeSettingsParity) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "runtime_settings_parity",
			Title:    "Runtime Settings Parity",
			Severity: runtimeSettingsParitySeverity(summary.RuntimeSettingsParity),
			Summary:  runtimeSettingsParitySummary(summary.RuntimeSettingsStatusCounts),
			Items:    runtimeSettingsParityItems(summary.RuntimeSettingsParity),
		})
	}

	if summary.MigrationReadiness != nil {
		sections = append(sections, scriptImportReviewSection{
			Key:      "runtime_settings_migration_readiness",
			Title:    "Runtime Settings Coverage",
			Severity: migrationReadinessSeverity(summary.MigrationReadiness),
			Summary:  summary.MigrationReadiness.Summary,
			Items:    migrationReadinessItems(summary.MigrationReadiness),
		})
	}

	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		sections = append(sections, trueClientWeb2ReviewSections(summary)...)
	}

	if result.Script != nil && result.Script.Protocol == "browser" {
		sections = append(sections, scriptImportReviewSection{
			Key:      "browser_import_assumptions",
			Title:    "Browser Import Assumptions",
			Severity: "info",
			Summary:  "Mobile browser-profile archives map supported steps into browser DSL and require replay validation for selectors, waits, and device behavior; Web and Web 2.0 browser profiles are tracked as separate review profiles.",
			Items: []string{
				"Legacy desktop browser-profile archives remain out of scope; Web and Web 2.0 source profiles require guided review.",
				"Simple JavaScript fill/click statements are decomposed when possible; other JavaScript is preserved as review guidance.",
				"Selector fidelity should be checked with browser replay before production use.",
			},
		})
	}

	if result.DetectedProtocol != nil && (result.DetectedProtocol.CanonicalSourceProtocol == "oracle_web" || result.DetectedProtocol.CanonicalSourceProtocol == "siebel_web") {
		sections = append(sections, scriptImportReviewSection{
			Key:      "oracle_ebusiness_review",
			Title:    "Oracle E-Business Review Pack",
			Severity: "warning",
			Summary:  "Oracle-Web and Siebel-Web imports execute through HTTP but require enterprise correlation and session-state review before promotion.",
			Items: []string{
				"Review ICX/OA Framework or Siebel session identifiers and correlation candidates.",
				"Validate login, navigation, and form-post replay evidence against the target environment.",
				"Confirm Oracle E-Business runtime settings, data fit, and environment-specific tokens before saving reusable assets.",
			},
		})
	}

	if summary.SalesforceReview != nil {
		severity := "info"
		if len(summary.SalesforceReview.Blockers) > 0 {
			severity = "blocker"
		} else if summary.SalesforceReview.HARMatchCoverage.HARProvided && summary.SalesforceReview.HARMatchCoverage.CoveragePct < 80 {
			severity = "warning"
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "salesforce_review",
			Title:    "Salesforce Native Review",
			Severity: severity,
			Summary:  salesforceReviewSummary(summary.SalesforceReview),
			Items:    salesforceReviewItems(summary.SalesforceReview),
		})
	}
	if summary.MuleSoftReview != nil {
		sections = append(sections, mulesoftReviewSection(summary.MuleSoftReview))
	}
	if summary.AgentforceReview != nil {
		sections = append(sections, agentforceReviewSection(summary.AgentforceReview, nil))
	}

	return sections
}

func enterpriseArchiveFidelitySummary(report *enterprisearchive.ImportFidelityReport) string {
	if report == nil {
		return ""
	}
	status := firstNonEmpty(report.PromotionStatus, "unknown")
	return fmt.Sprintf("Promotion status: %s; accounted for %d of %d detected source function(s) through converted actions or preserved contributors, with %d dropped body/bodies and %d secret-gated payload(s).", status, report.ConvertedFunctions, report.ExpectedFunctions, report.DroppedBodies, report.SecretGated)
}

func enterpriseArchiveFidelityItems(report *enterprisearchive.ImportFidelityReport) []string {
	if report == nil {
		return nil
	}
	items := []string{
		fmt.Sprintf("Source files inventoried: %d", len(report.SourceFiles)),
		fmt.Sprintf("Converted actions: %d", report.ConvertedActions),
		fmt.Sprintf("Dropped request bodies: %d", report.DroppedBodies),
		fmt.Sprintf("Secret-gated payloads: %d", report.SecretGated),
	}
	if len(report.ParameterParity) > 0 {
		items = append(items, fmt.Sprintf("Parameter parity: expected=%d converted=%d data_sources=%d", report.ParameterParity["expected_parameters"], report.ParameterParity["converted_parameters"], report.ParameterParity["converted_data_sources"]))
	}
	runtimeStatuses := make([]string, 0, len(report.RuntimeParity))
	for status := range report.RuntimeParity {
		runtimeStatuses = append(runtimeStatuses, status)
	}
	sort.Strings(runtimeStatuses)
	for _, status := range runtimeStatuses {
		count := report.RuntimeParity[status]
		items = append(items, fmt.Sprintf("Runtime parity %s: %d", status, count))
	}
	for _, blocker := range report.Blockers {
		items = append(items, "Blocker: "+blocker)
	}
	for _, warning := range report.Warnings {
		items = append(items, "Warning: "+warning)
	}
	return items
}

func salesforceReviewSummary(review *salesforce.Review) string {
	if review == nil {
		return ""
	}
	if !review.Detected {
		return "Salesforce native review ran but did not detect Salesforce traffic."
	}
	coverage := review.HARMatchCoverage
	return fmt.Sprintf("Detected Salesforce traffic across %d domain(s); HAR matched %d of %d imported request(s) (%.1f%%).", len(review.Domains), coverage.MatchedRequests, coverage.ImportedRequests, coverage.CoveragePct)
}

func salesforceReviewItems(review *salesforce.Review) []string {
	if review == nil {
		return nil
	}
	items := make([]string, 0)
	for _, domain := range review.Domains {
		items = append(items, fmt.Sprintf("Domain: %s (%s, %d request(s))", domain.Host, domain.Kind, domain.Count))
	}
	if len(review.AuraEndpoints) > 0 {
		items = append(items, fmt.Sprintf("Aura endpoints detected: %d", len(review.AuraEndpoints)))
	}
	if len(review.Transactions) > 0 {
		items = append(items, fmt.Sprintf("Transaction candidates: %d", len(review.Transactions)))
		flowKinds := map[string]bool{}
		for _, transaction := range review.Transactions {
			for _, flowKind := range transaction.FlowKinds {
				if strings.TrimSpace(flowKind) != "" {
					flowKinds[flowKind] = true
				}
			}
		}
		if len(flowKinds) > 0 {
			ordered := make([]string, 0, len(flowKinds))
			for flowKind := range flowKinds {
				ordered = append(ordered, flowKind)
			}
			sort.Strings(ordered)
			items = append(items, "Salesforce flow kinds: "+strings.Join(ordered, ", "))
		}
	}
	if len(review.Parameterizations) > 0 {
		keySet := map[string]bool{}
		for _, param := range review.Parameterizations {
			if strings.TrimSpace(param.ParamKey) != "" {
				keySet[param.ParamKey] = true
			}
		}
		keys := make([]string, 0, len(keySet))
		for key := range keySet {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items = append(items, fmt.Sprintf("Aura message parameterization candidates: %d (keys: %s)", len(review.Parameterizations), strings.Join(keys, ", ")))
	}
	if review.CorrelationConfidence != "" {
		items = append(items, "Correlation confidence: "+review.CorrelationConfidence)
	}
	for _, blocker := range review.Blockers {
		items = append(items, "Blocker: "+blocker)
	}
	for _, action := range review.NextActions {
		items = append(items, "Next action: "+action)
	}
	return items
}

func buildEnterpriseArchiveImportSummary(result *enterprisearchive.ConvertResult, unsupportedElements []string, readinessPolicy enterprisearchive.RuntimeSettingMigrationPolicy) enterpriseArchiveImportSummary {
	summary := enterpriseArchiveImportSummary{
		UnsupportedFunctionCount: len(unsupportedElements),
	}
	if result != nil && result.DetectedProtocol != nil {
		summary.DetectedProtocol = result.DetectedProtocol
		summary.ImportMode = string(result.DetectedProtocol.ImportMode)
	}
	if result != nil && result.ReviewArtifact != nil {
		summary.ReviewArtifact = result.ReviewArtifact
	}
	if result != nil && result.SourceReview != nil {
		summary.SourceReview = result.SourceReview
	}
	if result != nil && result.FidelityReport != nil {
		summary.FidelityReport = result.FidelityReport
	}

	if result == nil || result.Script == nil || result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		return summary
	}

	preserved := result.Script.Metadata.Import.Preserved
	if preserved == nil {
		return summary
	}

	summary.ProjectFile = stringValue(preserved["project_file"])
	summary.MappedRuntimeFile = stringValue(preserved["mapped_runtime_file"])
	summary.ProjectSections = sortedRecordKeys(preserved["project_settings"])
	summary.RuntimeFiles = sortedRecordKeys(preserved["runtime_files"])
	summary.ParameterFiles = stringSliceValue(preserved["parameter_files"])
	summary.RuntimeSettingsParity = runtimeSettingsParityValue(preserved["runtime_settings_parity"])
	summary.RuntimeSettingsStatusCounts = intMapValue(preserved["runtime_settings_status_counts"])
	summary.MigrationReadiness = enterpriseArchiveMigrationReadinessValue(preserved["runtime_settings_migration_readiness"])
	summary.TrueClientInspection = trueClientArchiveInspectionValue(preserved["trueclient_archive_inspection"])
	summary.SourceScriptType = stringValue(preserved["source_script_type"])
	summary.SourceProfile = stringValue(preserved["source_profile"])
	if summary.SourceReview == nil {
		summary.SourceReview = sourceReviewValue(preserved["source_review"])
	}
	if summary.MigrationReadiness == nil && len(summary.RuntimeSettingsParity) > 0 {
		summary.MigrationReadiness = enterprisearchive.BuildRTSMigrationReadiness(summary.RuntimeSettingsParity, readinessPolicy)
	}
	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		summary.Web2MigrationReadiness = buildWeb2MigrationReadinessReport(result, summary)
		summary.TrueClient2CompatReport = buildTrueClient2CompatReport(result, summary)
	}
	summary.SalesforceReview = salesforceReviewValue(preserved["salesforce_review"])
	summary.AgentforceReview = agentforceReviewValue(preserved["agentforce_review"])
	summary.MuleSoftReview = mulesoftReviewValue(preserved["mulesoft_review"])
	if summary.SalesforceReview != nil && summary.SalesforceReview.NativePreview != nil && summary.SalesforceReview.NativePreview.Script == nil && result != nil && result.Script != nil && result.Script.GeneratedBy == "apexload-salesforce-builder" {
		summary.SalesforceReview.NativePreview.Script = result.Script
	}
	if summary.SalesforceReview != nil && summary.SalesforceReview.NativePreview != nil && result != nil && result.Script != nil && result.Script.GeneratedBy == "apexload-salesforce-builder" {
		preserved["generation_mode"] = "salesforce_native_http"
		preserved["protocol_scope"] = "salesforce_http_family"
	}
	if summary.FidelityReport == nil {
		summary.FidelityReport = fidelityReportValue(preserved["fidelity_report"])
	}

	return summary
}

func persistEnterpriseArchiveImportSummary(result *enterprisearchive.ConvertResult, summary enterpriseArchiveImportSummary) {
	if result == nil {
		return
	}
	scripts := result.Scripts
	if len(scripts) == 0 && result.Script != nil {
		scripts = []*dsl.Script{result.Script}
	}
	for _, script := range scripts {
		if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
			continue
		}
		if script.Metadata.Import.Preserved == nil {
			script.Metadata.Import.Preserved = make(map[string]any)
		}
		if summary.MigrationReadiness != nil {
			script.Metadata.Import.Preserved["runtime_settings_migration_readiness"] = summary.MigrationReadiness
		}
		if summary.Web2MigrationReadiness != nil {
			script.Metadata.Import.Preserved["web2_migration_readiness"] = summary.Web2MigrationReadiness
		}
		if summary.TrueClient2CompatReport != nil {
			script.Metadata.Import.Preserved["trueclient2_compat_report"] = summary.TrueClient2CompatReport
		}
		if summary.SalesforceReview != nil {
			script.Metadata.Import.Preserved["salesforce_review"] = salesforceReviewForResponse(summary.SalesforceReview)
		}
		if summary.AgentforceReview != nil {
			script.Metadata.Import.Preserved["agentforce_review"] = summary.AgentforceReview
		}
		if summary.MuleSoftReview != nil {
			script.Metadata.Import.Preserved["mulesoft_review"] = summary.MuleSoftReview
		}
		if summary.SourceReview != nil {
			script.Metadata.Import.Preserved["source_review"] = summary.SourceReview
		}
		if summary.FidelityReport != nil {
			script.Metadata.Import.Preserved["fidelity_report"] = summary.FidelityReport
		}
	}
}

// protocolCompatReportForScript returns a copy of the aggregate report with the
// per-script-shaped fields (step / data-source / runtime-parity counts) recomputed
// from the given script so multi-script formats do not copy the first script's
// counts into every protocol-compatibility report.
func protocolCompatReportForScript(base *protocolCompatReport, script *dsl.Script) *protocolCompatReport {
	if base == nil || script == nil {
		return base
	}
	clone := *base
	clone.MappedSteps = len(script.Init) + len(script.Actions) + len(script.Teardown)
	clone.DataSources = protocolDataSourceCount(script)
	if parity := protocolRuntimeParityFromScript(script); len(parity) > 0 {
		clone.RuntimeParity = parity
	}
	return &clone
}

func buildEnterpriseArchiveProtocolCompatReport(result *enterprisearchive.ConvertResult, unsupported []string, summary enterpriseArchiveImportSummary) *protocolCompatReport {
	if result == nil {
		return nil
	}
	importMode := "executable"
	canonical := "web_http_html"
	if result.DetectedProtocol != nil {
		importMode = string(result.DetectedProtocol.ImportMode)
		canonical = result.DetectedProtocol.CanonicalSourceProtocol
	}
	reviewGatedSteps := len(unsupported) + result.Stats.ReviewOnlyFunctions +
		result.Stats.CustomCallFunctions + result.Stats.ExternalRequiredFunctions
	status := protocolPromotionStatus(importMode, reviewGatedSteps, len(result.Warnings))
	nextActions := protocolNextActions(status, enterpriseArchiveProtocolValidationAction(canonical))
	if result.ReviewArtifact != nil && len(result.ReviewArtifact.NextActions) > 0 {
		nextActions = result.ReviewArtifact.NextActions
	}
	runtimeParity := summary.RuntimeSettingsStatusCounts
	if len(runtimeParity) == 0 {
		runtimeParity = protocolRuntimeParityFromScript(result.Script)
	}
	unsupportedFragments := enterpriseArchiveUnsupportedFragments(result, unsupported)
	return &protocolCompatReport{
		SourceFormat:         enterpriseArchiveProtocolSourceFormat(canonical),
		CanonicalProtocol:    canonical,
		ImportMode:           importMode,
		MappedSteps:          result.Stats.ConvertedActions,
		ReviewGatedSteps:     reviewGatedSteps,
		UnsupportedFragments: unsupportedFragments,
		RuntimeParity:        runtimeParity,
		DataSources:          protocolDataSourceCount(result.Script),
		CorrelationCoverage:  protocolCorrelationCoverage(result.Stats.Extractors, result.Stats.RawRequestActions),
		PromotionStatus:      status,
		NextActions:          nextActions,
	}
}

func enterpriseArchiveUnsupportedFragments(result *enterprisearchive.ConvertResult, unsupported []string) []string {
	fragments := append([]string(nil), unsupported...)
	if result != nil && result.ReviewArtifact != nil && len(result.ReviewArtifact.PreservedFunctions) > 0 {
		fragments = append(fragments, result.ReviewArtifact.PreservedFunctions...)
	}
	return uniqueSortedStrings(fragments)
}

func enterpriseArchiveProtocolSourceFormat(canonical string) string {
	switch canonical {
	case "devweb":
		return "DevWeb"
	case "jmeter":
		return "JMeter JMX"
	default:
		return "Enterprise Script Archive"
	}
}

func enterpriseArchiveProtocolValidationAction(canonical string) string {
	switch canonical {
	case "devweb":
		return "Validate converted DevWeb replay and review JavaScript/helper-module blockers before promotion."
	case "jmeter":
		return "Validate converted JMeter thread groups and scenario draft before promotion."
	default:
		return "Validate converted enterprise script archive replay and correlation behavior before promotion."
	}
}
