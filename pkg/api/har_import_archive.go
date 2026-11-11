package api

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

// harArchiveMaxEntries bounds the ZIP directory a HAR upload may carry. A
// zipped capture holds one .har file, plus the folders and OS metadata a
// desktop zip tool adds.
const harArchiveMaxEntries = 64

// zipLocalFileMagic and zipEmptyArchiveMagic start every ZIP file. A HAR is
// JSON, so it can never begin with either.
var (
	zipLocalFileMagic    = []byte("PK\x03\x04")
	zipEmptyArchiveMagic = []byte("PK\x05\x06")
)

// isZIPUpload reports whether an uploaded HAR is a ZIP archive, judged by its
// leading bytes rather than its filename so a renamed archive is still read.
func isZIPUpload(data []byte) bool {
	return bytes.HasPrefix(data, zipLocalFileMagic) || bytes.HasPrefix(data, zipEmptyArchiveMagic)
}

// unwrapHARArchive returns the HAR inside a zipped upload, or data unchanged
// when it is not a ZIP. The archive must hold exactly one .har file; the
// decompressed capture is held to the same limit as an uploaded HAR.
func unwrapHARArchive(data []byte) ([]byte, error) {
	primary, _, err := unwrapHARArchiveUpload(data, false)
	return primary, err
}

// unwrapHARArchiveUpload returns the HAR capture(s) inside a zipped upload, or
// data unchanged when it is not a ZIP. When allowPair is true, a 2-HAR ZIP
// unpacks into (primary, secondary) sorted deterministically by entry path
// under a combined 2 * harImportMaxFileBytes decompression budget.
func unwrapHARArchiveUpload(data []byte, allowPair bool) ([]byte, []byte, error) {
	if !isZIPUpload(data) {
		return data, nil, nil
	}
	maxTotalBytes := int64(harImportMaxFileBytes)
	if allowPair {
		maxTotalBytes = 2 * int64(harImportMaxFileBytes)
	}
	files, err := archiveingest.ReadZIP(data, archiveingest.Options{Limits: archiveingest.Limits{
		MaxEntries:    harArchiveMaxEntries,
		MaxEntryBytes: harImportMaxFileBytes,
		MaxTotalBytes: maxTotalBytes,
	}})
	if err != nil {
		if errors.Is(err, archiveingest.ErrLimitExceeded) {
			return nil, nil, &harArchiveLimitError{maxBytes: maxTotalBytes, err: err}
		}
		return nil, nil, fmt.Errorf("read zipped HAR: %w", err)
	}
	var captures []archiveingest.File
	for _, file := range files {
		if strings.EqualFold(archiveingest.LogicalExt(file.Name), ".har") {
			captures = append(captures, file)
		}
	}
	sort.Slice(captures, func(i, j int) bool {
		leftLower := strings.ToLower(captures[i].Name)
		rightLower := strings.ToLower(captures[j].Name)
		if leftLower != rightLower {
			return leftLower < rightLower
		}
		return captures[i].Name < captures[j].Name
	})
	switch {
	case len(captures) == 1:
		return captures[0].Data, nil, nil
	case len(captures) == 2 && allowPair:
		return captures[0].Data, captures[1].Data, nil
	case len(captures) == 0:
		return nil, nil, errors.New("the ZIP archive contains no .har file")
	default:
		names := make([]string, 0, len(captures))
		for _, capture := range captures {
			names = append(names, capture.Name)
		}
		sort.Strings(names)
		if allowPair {
			return nil, nil, fmt.Errorf("the ZIP archive contains %d .har files (%s); zip one or two captures per import", len(names), strings.Join(names, ", "))
		}
		return nil, nil, fmt.Errorf("the ZIP archive contains %d .har files (%s); zip one capture per import", len(names), strings.Join(names, ", "))
	}
}

// harArchiveLimitError is a zipped HAR refused by the size, entry-count or
// compression-ratio bounds rather than for being malformed.
type harArchiveLimitError struct {
	maxBytes int64
	err      error
}

func (e *harArchiveLimitError) Error() string {
	limitBytes := e.maxBytes
	if limitBytes <= 0 {
		limitBytes = harImportMaxFileBytes
	}
	return fmt.Sprintf("zipped HAR exceeds the import limit of %d MiB decompressed: %v", limitBytes>>20, e.err)
}

func (e *harArchiveLimitError) Unwrap() error { return e.err }

// unwrapHARImportArchives replaces a zipped baseline capture (or a 2-HAR
// baseline+comparison archive when allowPair is true) in an import request with
// the HAR bytes it holds. On failure it writes the response and returns false.
func unwrapHARImportArchives(w http.ResponseWriter, req *harImportRequest, allowPair bool) (pairedFromArchive bool, ok bool) {
	if len(req.Data) == 0 {
		return false, true
	}
	primary, secondary, err := unwrapHARArchiveUpload(req.Data, allowPair)
	if err != nil {
		status := http.StatusBadRequest
		var limitErr *harArchiveLimitError
		if errors.As(err, &limitErr) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, "HAR upload: "+err.Error())
		return false, false
	}
	req.Data = primary
	if len(secondary) > 0 {
		req.SecondaryData = secondary
		req.Opts.ArtifactMode = normalizeHARArtifactMode(req.Opts.ArtifactMode, true)
		return true, true
	}
	return false, true
}
