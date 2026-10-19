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
	if !isZIPUpload(data) {
		return data, nil
	}
	files, err := archiveingest.ReadZIP(data, archiveingest.Options{Limits: archiveingest.Limits{
		MaxEntries:    harArchiveMaxEntries,
		MaxEntryBytes: harImportMaxFileBytes,
		MaxTotalBytes: harImportMaxFileBytes,
	}})
	if err != nil {
		if errors.Is(err, archiveingest.ErrLimitExceeded) {
			return nil, &harArchiveLimitError{err: err}
		}
		return nil, fmt.Errorf("read zipped HAR: %w", err)
	}
	var captures []archiveingest.File
	for _, file := range files {
		if strings.EqualFold(archiveingest.LogicalExt(file.Name), ".har") {
			captures = append(captures, file)
		}
	}
	switch len(captures) {
	case 1:
		return captures[0].Data, nil
	case 0:
		return nil, errors.New("the ZIP archive contains no .har file")
	default:
		names := make([]string, 0, len(captures))
		for _, capture := range captures {
			names = append(names, capture.Name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("the ZIP archive contains %d .har files (%s); zip one capture per import", len(names), strings.Join(names, ", "))
	}
}

// harArchiveLimitError is a zipped HAR refused by the size, entry-count or
// compression-ratio bounds rather than for being malformed.
type harArchiveLimitError struct{ err error }

func (e *harArchiveLimitError) Error() string {
	return fmt.Sprintf("zipped HAR exceeds the import limit of %d MiB decompressed: %v", harImportMaxFileBytes>>20, e.err)
}

func (e *harArchiveLimitError) Unwrap() error { return e.err }

// unwrapHARImportArchives replaces zipped captures in an import request with
// the HAR each holds. On failure it writes the response and returns false.
func unwrapHARImportArchives(w http.ResponseWriter, req *harImportRequest) bool {
	for _, target := range []struct {
		data  *[]byte
		label string
	}{{&req.Data, "HAR"}, {&req.SecondaryData, "comparison HAR"}} {
		if len(*target.data) == 0 {
			continue
		}
		unwrapped, err := unwrapHARArchive(*target.data)
		if err != nil {
			status := http.StatusBadRequest
			var limitErr *harArchiveLimitError
			if errors.As(err, &limitErr) {
				status = http.StatusRequestEntityTooLarge
			}
			writeError(w, status, target.label+" upload: "+err.Error())
			return false
		}
		*target.data = unwrapped
	}
	return true
}
