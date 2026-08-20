package api

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

const importMultipartMemoryBytes = 1 << 20

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
