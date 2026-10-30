package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

const importMultipartMemoryBytes = 1 << 20

// importMultipartValueBytes bounds the non-file fields of a streamed multipart
// upload, matching what ParseMultipartForm keeps in memory for the
// importMultipartMemoryBytes budget (that budget plus 10 MiB).
const importMultipartValueBytes = importMultipartMemoryBytes + (10 << 20)

// importMultipartMaxParts and importMultipartPartOverhead bound a streamed
// multipart upload the way multipart.ReadForm does: at most 1000 parts, and
// each part charged its field name and a fixed overhead against
// importMultipartValueBytes, so a flood of empty fields cannot build an
// unbounded value map.
const (
	importMultipartMaxParts     = 1000
	importMultipartPartOverhead = 200
)

// readImportMultipartFile parses a multipart/form-data import upload and returns
// the bytes and original filename of the "file" field. On any failure it writes
// the appropriate 400 response and returns ok=false. Callers remain responsible
// for deriving a script name and reading format-specific form values (which are
// available via r.FormValue after this call parses the form).
func readImportMultipartFile(w http.ResponseWriter, r *http.Request, maxBytes int64) (data []byte, filename string, ok bool) {
	if err := r.ParseMultipartForm(importMultipartMemoryBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return nil, "", false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field is required")
		return nil, "", false
	}
	defer file.Close()
	data, err = io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
		return nil, "", false
	}
	return data, header.Filename, true
}

// readImportBody reads the full request body for a non-multipart import upload,
// writing a 400 response and returning ok=false if the read fails.
func readImportBody(w http.ResponseWriter, r *http.Request) (data []byte, ok bool) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	return data, true
}

// importUploadLimits bounds an upload whose route accepts more than one
// artifact. Body is the route's whole envelope; File bounds each uploaded
// artifact — the raw body, and every multipart file part kept. Files names
// the multipart file fields kept besides "file", in priority order. Discard
// names file fields the route reads but does not use: they are drained
// without being kept and reported present with a nil value.
type importUploadLimits struct {
	Body    int64
	File    int64
	Files   []string
	Discard []string
}

// importUploadTooLarge writes 413 for an upload over a body or artifact limit.
func importUploadTooLarge(w http.ResponseWriter, what string, limit int64) {
	size := fmt.Sprintf("%d MiB", limit>>20)
	if limit%(1<<20) != 0 {
		size = fmt.Sprintf("%d-byte", limit)
	}
	writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body too large: %s exceeds the %s import limit", what, size))
}

// writeImportReadError maps a failed upload read to 413 when the route's body
// limit stopped it and to 400 otherwise.
func writeImportReadError(w http.ResponseWriter, prefix string, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		importUploadTooLarge(w, "the request", tooLarge.Limit)
		return
	}
	writeError(w, http.StatusBadRequest, prefix+": "+err.Error())
}

// readImportArtifact reads one artifact through a bounded reader. ok is false
// with tooLarge set when it is larger than limit; the caller names it.
func readImportArtifact(reader io.Reader, limit int64) (data []byte, tooLarge bool, err error) {
	data, err = io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return nil, true, nil
	}
	return data, false, nil
}

// decodeImportUploadWithLimits is decodeImportUpload for a route that bounds
// each artifact separately from its envelope. A multipart upload is streamed
// part by part instead of spooled through ParseMultipartForm: each kept file
// part is read through a bounded reader, so an oversized artifact is refused
// with 413 as soon as it passes the limit rather than after the whole body has
// been copied to temporary files. Extra file parts named in limits.Files are
// returned in files, and fields named in limits.Discard map to nil when
// present; other file parts are discarded.
func decodeImportUploadWithLimits(w http.ResponseWriter, r *http.Request, limits importUploadLimits) (upload *importUpload, files map[string][]byte, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, limits.Body)
	contentType := r.Header.Get("Content-Type")
	query := r.URL.Query()

	upload = &importUpload{query: query, values: query}
	var filename string
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		upload.Transport = importTransportJSON
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeImportReadError(w, "failed to read request body", err)
			return nil, nil, false
		}
		upload.Data = data
		filename = query.Get("filename")
	case strings.HasPrefix(contentType, "multipart/form-data"):
		upload.Transport = importTransportMultipart
		var values url.Values
		upload.Data, filename, files, values, ok = readImportMultipartStream(w, r, limits)
		if !ok {
			return nil, nil, false
		}
		merged := url.Values{}
		for key, value := range query {
			merged[key] = value
		}
		for key, value := range values {
			merged[key] = value
		}
		upload.values = merged
	default:
		upload.Transport = importTransportRaw
		data, tooLarge, err := readImportArtifact(r.Body, limits.File)
		switch {
		case err != nil:
			writeImportReadError(w, "failed to read request body", err)
			return nil, nil, false
		case tooLarge:
			importUploadTooLarge(w, "the uploaded file", limits.File)
			return nil, nil, false
		}
		upload.Data = data
		filename = query.Get("filename")
	}
	upload.applyFilename(filename)
	upload.resolveProjectID()
	return upload, files, true
}

// readImportMultipartStream reads a multipart/form-data upload part by part:
// the first "file" part is the artifact, the first part of each field in
// limits.Files is kept, and scalar fields become option values (bounded like
// ParseMultipartForm bounds them). As with r.FormFile, only parts that carry
// a filename are files.
func readImportMultipartStream(w http.ResponseWriter, r *http.Request, limits importUploadLimits) (data []byte, filename string, files map[string][]byte, values url.Values, ok bool) {
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return nil, "", nil, nil, false
	}
	kept := map[string]bool{"file": true}
	for _, field := range limits.Files {
		kept[field] = true
	}
	discarded := map[string]bool{}
	for _, field := range limits.Discard {
		discarded[field] = true
	}
	values = url.Values{}
	files = map[string][]byte{}
	haveFile := false
	var valueBytes int64
	for parts := 0; ; parts++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeImportReadError(w, "invalid multipart form", err)
			return nil, "", nil, nil, false
		}
		if parts >= importMultipartMaxParts {
			_ = part.Close()
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body too large: the multipart form has more than %d parts", importMultipartMaxParts))
			return nil, "", nil, nil, false
		}
		name := part.FormName()
		if name == "" {
			_ = part.Close()
			continue
		}
		valueBytes += int64(len(name)+len(part.FileName())) + importMultipartPartOverhead
		if valueBytes > importMultipartValueBytes {
			_ = part.Close()
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large: multipart form fields exceed their limit")
			return nil, "", nil, nil, false
		}
		if part.FileName() == "" {
			value, tooLarge, err := readImportArtifact(part, importMultipartValueBytes-valueBytes)
			_ = part.Close()
			switch {
			case err != nil:
				writeImportReadError(w, "invalid multipart form", err)
				return nil, "", nil, nil, false
			case tooLarge:
				writeError(w, http.StatusRequestEntityTooLarge, "request body too large: multipart form fields exceed their limit")
				return nil, "", nil, nil, false
			}
			valueBytes += int64(len(value))
			values.Add(name, string(value))
			continue
		}
		_, seen := files[name]
		if !kept[name] || seen || (name == "file" && haveFile) {
			// Unused file parts are drained, not kept; the envelope still bounds them.
			if _, err := io.Copy(io.Discard, part); err != nil {
				writeImportReadError(w, "invalid multipart form", err)
				return nil, "", nil, nil, false
			}
			_ = part.Close()
			if discarded[name] {
				files[name] = nil
			}
			continue
		}
		content, tooLarge, err := readImportArtifact(part, limits.File)
		_ = part.Close()
		switch {
		case err != nil:
			writeImportReadError(w, "failed to read uploaded file", err)
			return nil, "", nil, nil, false
		case tooLarge:
			importUploadTooLarge(w, fmt.Sprintf("the %q file", name), limits.File)
			return nil, "", nil, nil, false
		}
		if name == "file" {
			data, filename, haveFile = content, part.FileName(), true
			continue
		}
		files[name] = content
	}
	if !haveFile {
		writeError(w, http.StatusBadRequest, "file field is required")
		return nil, "", nil, nil, false
	}
	return data, filename, files, values, true
}

// importUploadTransport names the wire shape an import arrived on.
type importUploadTransport string

const (
	// importTransportJSON is an application/json envelope. Data holds the raw
	// body; the format decodes its own request type from it.
	importTransportJSON importUploadTransport = "json"
	// importTransportMultipart is a multipart/form-data upload with the artifact
	// in the "file" field and options in sibling form values.
	importTransportMultipart importUploadTransport = "multipart"
	// importTransportRaw is a bare body — the artifact itself — with options and
	// the logical filename supplied as query parameters.
	importTransportRaw importUploadTransport = "raw"
)

// importUpload is the transport-independent view of an import upload. Every
// format-specific reader gets one from decodeImportUpload instead of repeating
// the three-branch content-type walk, so body-size enforcement, project scope
// resolution, and filename handling stay identical across formats.
type importUpload struct {
	// Transport is the wire shape the artifact arrived on.
	Transport importUploadTransport
	// Data is the artifact bytes. On importTransportJSON it is the raw envelope,
	// which the caller decodes; on the other transports it is the artifact.
	Data []byte
	// Filename is the client-supplied logical filename, empty when none was
	// sent. It is already reduced to its base name.
	Filename string
	// Name is Filename with its logical extension trimmed — the default script
	// name for formats that do not carry one internally.
	Name string
	// ProjectID is the resolved project scope. It is trimmed on every transport;
	// JSON callers layer their body's own project_id on top via jsonProjectID.
	ProjectID string
	// values carries the option source for this transport: the parsed form
	// merged over the query string for a multipart upload, the query string
	// alone otherwise.
	values url.Values
	// query is the raw query string, kept separately because project scope
	// resolves query-first while every other option resolves form-first.
	query url.Values
}

// Value reads a single format option. It resolves against the multipart form for
// a file upload and against the query string for JSON and raw bodies, so a
// format's option table is written once regardless of how the artifact arrived.
func (u *importUpload) Value(key string) string {
	if u == nil {
		return ""
	}
	return u.values.Get(key)
}

// Values exposes the whole option set for formats whose option parsing takes a
// multi-value map rather than a single-value accessor.
func (u *importUpload) Values() url.Values {
	if u == nil || u.values == nil {
		return url.Values{}
	}
	return u.values
}

// jsonProjectID resolves the project scope for a JSON envelope. The body's own
// project_id wins when it carries one; otherwise the query-string scope stands.
// Both sides are trimmed, so a whitespace-only body field falls through to the
// query rather than resolving to the empty default.
func (u *importUpload) jsonProjectID(bodyProjectID string) string {
	if trimmed := strings.TrimSpace(bodyProjectID); trimmed != "" {
		return trimmed
	}
	if u == nil {
		return ""
	}
	return u.ProjectID
}

// decodeImportUpload performs the transport walk every import route shares:
// enforce the body limit, classify the content type, read the artifact, and
// resolve the project scope and logical filename. On any failure it writes the
// response and returns ok=false.
//
// The project scope is resolved the same way on every branch — trimmed, query
// string first, multipart form as a fallback when one was parsed. Before this
// existed each reader inlined its own resolution and the raw-body branch used an
// untrimmed query value, so a padded ?project_id= landed in a scope no lookup
// could match.
func decodeImportUpload(w http.ResponseWriter, r *http.Request, maxBytes int64) (*importUpload, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	contentType := r.Header.Get("Content-Type")
	query := r.URL.Query()

	upload := &importUpload{query: query, values: query}
	var filename string
	var ok bool
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		upload.Transport = importTransportJSON
		upload.Data, ok = readImportBody(w, r)
		filename = query.Get("filename")
	case strings.HasPrefix(contentType, "multipart/form-data"):
		upload.Transport = importTransportMultipart
		upload.Data, filename, ok = readImportMultipartFile(w, r, maxBytes)
		// Read options from the form, falling back to the query string, which is
		// how the per-format readers resolved them before this was shared.
		upload.values = importUploadFormValues(r)
	default:
		upload.Transport = importTransportRaw
		upload.Data, ok = readImportBody(w, r)
		filename = query.Get("filename")
	}
	if !ok {
		return nil, false
	}
	upload.applyFilename(filename)
	upload.resolveProjectID()
	return upload, true
}

// applyFilename records the logical base name and the extension-trimmed script
// name derived from a client-supplied filename.
func (u *importUpload) applyFilename(filename string) {
	if strings.TrimSpace(filename) == "" {
		// path.Base turns the empty string into ".", which would travel onward
		// as a nonsense filename. An upload that named no file has no filename.
		return
	}
	u.Filename = archiveingest.LogicalBase(filename)
	u.Name = strings.TrimSuffix(u.Filename, archiveingest.LogicalExt(u.Filename))
}

// resolveProjectID records the trimmed project scope this upload targets: the
// query string first, then any parsed multipart form field. That is the order
// the per-format readers used before this was shared, and it keeps a scope
// pinned in the URL authoritative over one a form body also happens to carry.
//
// The request body is never re-parsed here. On a JSON or raw upload it has
// already been consumed, and asking net/http for a form value at this point
// would silently read an empty one.
func (u *importUpload) resolveProjectID() {
	if scope := strings.TrimSpace(u.query.Get("project_id")); scope != "" {
		u.ProjectID = scope
		return
	}
	u.ProjectID = strings.TrimSpace(u.values.Get("project_id"))
}

// importUploadFormValues merges the parsed multipart values with the query
// string so a format option can be supplied either way, matching how the
// per-format readers already resolved options before this was shared.
func importUploadFormValues(r *http.Request) url.Values {
	values := url.Values{}
	for key, value := range r.URL.Query() {
		values[key] = value
	}
	if r.PostForm != nil {
		for key, value := range r.PostForm {
			values[key] = value
		}
	}
	if r.MultipartForm != nil {
		for key, value := range r.MultipartForm.Value {
			values[key] = value
		}
	}
	return values
}

// bareArtifactName is the default script name for an upload whose body is the
// artifact itself rather than an envelope carrying a name. A JSON body has no
// filename to derive from, so the ?name= override stands in; every other
// transport uses the uploaded filename with its extension trimmed.
func (u *importUpload) bareArtifactName() string {
	if u == nil {
		return ""
	}
	if u.Transport == importTransportJSON {
		return u.Value("name")
	}
	return u.Name
}
