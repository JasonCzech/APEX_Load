package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"
)

// Full source text for the Source Mapping review.
//
// A review keeps at most sourceview.MaxFileChars of each file, so the saved
// script, and the destination pane that renders all of it, stay bounded. A
// mapping can point anywhere in its file, though. The import response
// therefore stores the complete text of each truncated file a mapping opens
// beside the project's import assets, addressed by its SHA-256, and the review
// records that hash (full_sha256). The review loads the text on demand from
// GET /api/v1/projects/{id}/source-review/texts/{sha256}.
//
// Storage is content-addressed per project, so importing the same source
// again reuses the stored text. Text from an import that is never saved stays
// behind, as other import assets do; import assets have no collector yet.

const (
	sourceReviewTextKind = "source-review"
	// sourceReviewTextMaxPerImport bounds the text one import response stores.
	sourceReviewTextMaxPerImport = 64 << 20
)

var (
	sourceReviewTextDigest      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	errSourceReviewTextNotFound = errors.New("source review text not found")
)

// sourceReviewTextResponse is the body of the full source text endpoint.
type sourceReviewTextResponse struct {
	SHA256    string `json:"sha256"`
	SizeBytes int    `json:"size_bytes"`
	Lines     int    `json:"lines"`
	Content   string `json:"content"`
}

// sourceReviewTextLines counts lines the way the review's code view does:
// "\r\n", "\r" and "\n" each end a line.
func sourceReviewTextLines(text string) int {
	lines := 1
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case '\n':
			lines++
		case '\r':
			lines++
			if index+1 < len(text) && text[index+1] == '\n' {
				index++
			}
		}
	}
	return lines
}

// retainImportResponseSourceTexts stores the full text the converters kept on
// this response's source reviews. It runs before the drafts are fingerprinted,
// because the reviews it updates are part of those drafts.
func (s *Server) retainImportResponseSourceTexts(r *http.Request, opts *scriptImportResponseOptions) {
	if s == nil || r == nil || opts == nil {
		return
	}
	reviews := make([]*sourceview.Review, 0, 2)
	if review := mutableSourceReview(&opts.SourceReview); review != nil {
		reviews = append(reviews, review)
	}
	for _, script := range importDraftScriptPointers(opts.Script, opts.Scripts) {
		if review := scriptSourceReviewForUpdate(script); review != nil {
			reviews = append(reviews, review)
		}
	}
	if len(reviews) == 0 {
		return
	}
	// A response that skips attestation describes scripts the server saved
	// before building it, so a hash recorded now would never reach them. Such
	// an import has to store its text before it saves.
	if opts.SkipDraftAttestation {
		clearSourceReviewFullText(reviews)
		return
	}
	projectID, matches := sourceSecretProjectIDForRequest(r, *opts)
	if !matches || strings.TrimSpace(projectID) == "" {
		clearSourceReviewFullText(reviews)
		return
	}
	s.retainSourceReviewTexts(r.Context(), projectID, reviews)
}

// mutableSourceReview returns the typed review held in an untyped slot,
// replacing a by-value review with a pointer so updates reach the response.
func mutableSourceReview(slot *any) *sourceview.Review {
	switch typed := (*slot).(type) {
	case *sourceview.Review:
		return typed
	case sourceview.Review:
		review := typed
		*slot = &review
		return &review
	default:
		return nil
	}
}

func scriptSourceReviewForUpdate(script *dsl.Script) *sourceview.Review {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil
	}
	value, ok := script.Metadata.Import.Preserved["source_review"]
	if !ok {
		return nil
	}
	review := mutableSourceReview(&value)
	if review != nil {
		script.Metadata.Import.Preserved["source_review"] = value
	}
	return review
}

func clearSourceReviewFullText(reviews []*sourceview.Review) {
	for _, review := range reviews {
		for index := range review.SourceFiles {
			review.SourceFiles[index].FullText = ""
		}
	}
}

// retainSourceReviewTexts stores each review file's full text and records its
// hash. A file whose text cannot be stored keeps only its preview; the review
// says so when a mapping points past it.
func (s *Server) retainSourceReviewTexts(ctx context.Context, projectID string, reviews []*sourceview.Review) {
	budget := sourceReviewTextMaxPerImport
	stored := make(map[string]bool)
	for _, review := range reviews {
		if review == nil {
			continue
		}
		for index := range review.SourceFiles {
			file := &review.SourceFiles[index]
			text := file.FullText
			file.FullText = ""
			if text == "" || len(text) > sourceview.MaxFullTextBytes {
				continue
			}
			sum := sha256.Sum256([]byte(text))
			digest := hex.EncodeToString(sum[:])
			if ok, seen := stored[digest]; seen {
				if ok {
					setSourceReviewFullText(file, digest, text)
				}
				continue
			}
			if len(text) > budget {
				stored[digest] = false
				continue
			}
			if err := s.storeSourceReviewText(ctx, projectID, digest, text); err != nil {
				slog.Warn("source review full text was not stored; the review keeps its preview",
					"project_id", projectID, "path", file.Path, "error", err)
				stored[digest] = false
				continue
			}
			budget -= len(text)
			stored[digest] = true
			setSourceReviewFullText(file, digest, text)
		}
	}
}

func setSourceReviewFullText(file *sourceview.SourceFile, digest, text string) {
	file.FullSHA256 = digest
	file.FullBytes = len(text)
	file.FullLines = sourceReviewTextLines(text)
}

func (s *Server) sourceReviewTextObjectKey(projectID, digest string) (string, error) {
	return s.projectTextObjectKey(sourceReviewTextKind, projectID, digest)
}

func (s *Server) sourceReviewTextPath(projectID, digest string) (string, error) {
	return s.projectTextPath(sourceReviewTextKind, projectID, digest)
}

func (s *Server) storeSourceReviewText(ctx context.Context, projectID, digest, text string) error {
	return s.storeProjectText(ctx, sourceReviewTextKind, projectID, digest, text)
}

func (s *Server) sourceReviewTextExists(ctx context.Context, projectID, digest string) (bool, error) {
	return s.projectTextExists(ctx, sourceReviewTextKind, projectID, digest)
}

func (s *Server) loadSourceReviewText(ctx context.Context, projectID, digest string) (string, error) {
	return s.loadProjectText(ctx, sourceReviewTextKind, projectID, digest)
}

// Project texts are content-addressed per project and kind beside the import
// assets. Only the source-review kind is served to readers.

func (s *Server) projectTextObjectKey(kind, projectID, digest string) (string, error) {
	namespace, err := fileasset.ProjectAssetNamespace(projectID)
	if err != nil {
		return "", err
	}
	return joinArchiveURI(currentArchiveBackend().Prefix, "import-assets", "v1", namespace, kind, digest), nil
}

func (s *Server) projectTextPath(kind, projectID, digest string) (string, error) {
	dir, err := fileasset.ProjectAssetDir(s.importAssetDir, projectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, kind, digest[:2], digest), nil
}

func (s *Server) storeProjectText(ctx context.Context, kind, projectID, digest, text string) error {
	if !sourceReviewTextDigest.MatchString(digest) {
		return fmt.Errorf("invalid source text digest")
	}
	backend := currentArchiveBackend()
	if backend.Backend != archiveBackendFilesystem {
		key, err := s.projectTextObjectKey(kind, projectID, digest)
		if err != nil {
			return err
		}
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return err
		}
		defer bucket.Close()
		if exists, err := bucket.Exists(ctx, key); err == nil && exists {
			return nil
		}
		return bucket.WriteAll(ctx, key, []byte(text), &blob.WriterOptions{ContentType: "text/plain; charset=utf-8"})
	}
	path, err := s.projectTextPath(kind, projectID, digest)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("prepare source text store: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".source-text-*")
	if err != nil {
		return fmt.Errorf("create source text: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := io.WriteString(temp, text); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write source text: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close source text: %w", err)
	}
	// The name is the content's hash, so a concurrent writer of the same
	// text produces the same file and either rename wins.
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish source text: %w", err)
	}
	return nil
}

// projectTextExists reports whether the project holds the text, without
// reading it.
func (s *Server) projectTextExists(ctx context.Context, kind, projectID, digest string) (bool, error) {
	if !sourceReviewTextDigest.MatchString(digest) {
		return false, nil
	}
	backend := currentArchiveBackend()
	if backend.Backend != archiveBackendFilesystem {
		key, err := s.projectTextObjectKey(kind, projectID, digest)
		if err != nil {
			return false, err
		}
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return false, err
		}
		defer bucket.Close()
		return bucket.Exists(ctx, key)
	}
	path, err := s.projectTextPath(kind, projectID, digest)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil && info.Mode().IsRegular(), err
}

func (s *Server) loadProjectText(ctx context.Context, kind, projectID, digest string) (string, error) {
	if !sourceReviewTextDigest.MatchString(digest) {
		return "", errSourceReviewTextNotFound
	}
	var data []byte
	backend := currentArchiveBackend()
	if backend.Backend != archiveBackendFilesystem {
		key, err := s.projectTextObjectKey(kind, projectID, digest)
		if err != nil {
			return "", err
		}
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return "", err
		}
		defer bucket.Close()
		attributes, err := bucket.Attributes(ctx, key)
		if gcerrors.Code(err) == gcerrors.NotFound {
			return "", errSourceReviewTextNotFound
		}
		if err != nil {
			return "", err
		}
		if attributes.Size > sourceview.MaxFullTextBytes {
			return "", fmt.Errorf("stored source text exceeds %d bytes", sourceview.MaxFullTextBytes)
		}
		if data, err = bucket.ReadAll(ctx, key); err != nil {
			return "", err
		}
	} else {
		path, err := s.projectTextPath(kind, projectID, digest)
		if err != nil {
			return "", err
		}
		file, err := os.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			return "", errSourceReviewTextNotFound
		}
		if err != nil {
			return "", err
		}
		defer file.Close()
		if data, err = io.ReadAll(io.LimitReader(file, sourceview.MaxFullTextBytes+1)); err != nil {
			return "", err
		}
		if len(data) > sourceview.MaxFullTextBytes {
			return "", fmt.Errorf("stored source text exceeds %d bytes", sourceview.MaxFullTextBytes)
		}
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return "", fmt.Errorf("stored source text does not match its digest")
	}
	return string(data), nil
}

// handleGetSourceReviewText returns the full text of a source file a review
// in this project names by hash, as authored. Script reads return the review's
// preview the same way (imports preserve their source), so the two agree line
// for line.
func (s *Server) handleGetSourceReviewText(w http.ResponseWriter, r *http.Request) {
	projectID := normalizeProjectID(r.PathValue("id"))
	digest := strings.ToLower(strings.TrimSpace(r.PathValue("sha256")))
	if !sourceReviewTextDigest.MatchString(digest) {
		writeError(w, http.StatusBadRequest, "sha256 must be 64 lowercase hexadecimal characters")
		return
	}
	// The text never changes for its hash, but access can, so a browser
	// revalidates each read and an unchanged text costs a 304.
	etag := `"` + digest + `"`
	w.Header().Set("Cache-Control", "private, no-cache")
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		if exists, err := s.sourceReviewTextExists(r.Context(), projectID, digest); err == nil && exists {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	text, err := s.loadSourceReviewText(r.Context(), projectID, digest)
	if errors.Is(err, errSourceReviewTextNotFound) {
		writeError(w, http.StatusNotFound, "full source text is not available; the review shows its preview")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read full source text: "+err.Error())
		return
	}
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, sourceReviewTextResponse{
		SHA256:    digest,
		SizeBytes: len(text),
		Lines:     sourceReviewTextLines(text),
		Content:   text,
	})
}
