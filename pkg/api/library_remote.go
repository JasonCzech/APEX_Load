package api

// library_remote.go — split from library_federation.go (same package, behavior-preserving).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Server) remoteLibraryCapabilities(ctx context.Context, remote LibraryRemote) (*LibraryCapabilitiesResponse, error) {
	var caps LibraryCapabilitiesResponse
	if err := s.remoteJSON(ctx, remote, http.MethodGet, "/api/v1/library/capabilities", nil, &caps); err != nil {
		return nil, err
	}
	return &caps, nil
}

func (s *Server) remoteLibraryCatalog(ctx context.Context, remote LibraryRemote, projectID string, kind string) (*LibraryCatalogResponse, error) {
	values := url.Values{}
	if projectID != "" {
		values.Set("project_id", projectID)
	}
	if kind != "" {
		values.Set("kind", kind)
	}
	path := "/api/v1/library/catalog"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var catalog LibraryCatalogResponse
	if err := s.remoteJSON(ctx, remote, http.MethodGet, path, nil, &catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func (s *Server) remoteLibraryExport(ctx context.Context, remote LibraryRemote, req LibraryTransferRequest) (*LibraryBundle, error) {
	var bundle LibraryBundle
	if err := s.remoteJSON(ctx, remote, http.MethodPost, "/api/v1/library/export", req, &bundle); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (s *Server) remoteLibraryImport(ctx context.Context, remote LibraryRemote, req LibraryImportRequest) (*LibraryImportResponse, error) {
	var imported LibraryImportResponse
	if _, err := s.remoteJSONAccepted(ctx, remote, http.MethodPost, "/api/v1/library/import", req, &imported, map[int]bool{http.StatusConflict: true}); err != nil {
		return nil, err
	}
	return &imported, nil
}

func (s *Server) remoteLibraryImportArchive(ctx context.Context, remote LibraryRemote, req LibraryImportRequest, resultID string) (*LibraryImportResponse, error) {
	record, ok := s.runner.GetRunArchive(resultID)
	if !ok || record == nil {
		return s.remoteLibraryImport(ctx, remote, req)
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil, fmt.Errorf("materialize local archive: %w", err)
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	errCh := make(chan error, 1)
	go func() {
		defer writer.Close()
		bundlePart, err := multipartWriter.CreateFormField("bundle")
		if err != nil {
			errCh <- err
			_ = multipartWriter.Close()
			return
		}
		if err := json.NewEncoder(bundlePart).Encode(req.Bundle); err != nil {
			errCh <- err
			_ = multipartWriter.Close()
			return
		}
		if strings.TrimSpace(req.ProjectID) != "" {
			if err := multipartWriter.WriteField("project_id", req.ProjectID); err != nil {
				errCh <- err
				_ = multipartWriter.Close()
				return
			}
		}
		archivePart, err := multipartWriter.CreateFormFile("archive", sanitizeFilename(firstNonEmpty(resultID, "result"))+".zip")
		if err != nil {
			errCh <- err
			_ = multipartWriter.Close()
			return
		}
		if err := zipDirectory(record.ArchivePath, archivePart); err != nil {
			errCh <- err
			_ = multipartWriter.Close()
			return
		}
		if err := multipartWriter.Close(); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	base, err := url.Parse(remote.BaseURL)
	if err != nil {
		_ = reader.Close()
		return nil, err
	}
	values := url.Values{}
	if strings.TrimSpace(req.ProjectID) != "" {
		values.Set("project_id", req.ProjectID)
	}
	endpoint := base.ResolveReference(&url.URL{Path: "/api/v1/library/import/archive", RawQuery: values.Encode()})
	timeout := libraryRemoteArchiveTimeout()
	reqCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint.String(), reader)
	if err != nil {
		_ = reader.Close()
		return nil, err
	}
	httpReq.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	if err := s.authorizeRemoteRequest(httpReq, remote); err != nil {
		_ = reader.Close()
		return nil, err
	}
	resp, err := libraryRemoteArchiveHTTPClient(timeout).Do(httpReq)
	if err != nil {
		_ = reader.Close()
		if writeErr := <-errCh; writeErr != nil {
			return nil, writeErr
		}
		return nil, err
	}
	defer resp.Body.Close()
	writeErr := <-errCh
	if writeErr != nil {
		return nil, writeErr
	}
	if (resp.StatusCode < 200 || resp.StatusCode >= 300) && resp.StatusCode != http.StatusConflict {
		return nil, fmt.Errorf("remote library returned %s", resp.Status)
	}
	var imported LibraryImportResponse
	if err := json.NewDecoder(resp.Body).Decode(&imported); err != nil {
		return nil, err
	}
	return &imported, nil
}

func (s *Server) remoteLibraryDownloadArchive(ctx context.Context, remote LibraryRemote, resultID string) (*libraryArchiveImport, func(), error) {
	base, err := url.Parse(remote.BaseURL)
	if err != nil {
		return nil, nil, err
	}
	path := "/api/v1/library/results/" + url.PathEscape(strings.TrimSpace(resultID)) + "/archive"
	endpoint := base.ResolveReference(&url.URL{Path: path})
	timeout := libraryRemoteArchiveTimeout()
	reqCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	if err := s.authorizeRemoteRequest(req, remote); err != nil {
		return nil, nil, err
	}
	resp, err := libraryRemoteArchiveHTTPClient(timeout).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("remote library archive returned %s", resp.Status)
	}
	tmp, err := os.CreateTemp("", "apexload-library-archive-*.zip")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	written, err := copyWithMaxBytes(tmp, resp.Body, libraryArchiveMaxBytes())
	closeErr := tmp.Close()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if closeErr != nil {
		cleanup()
		return nil, nil, closeErr
	}
	return &libraryArchiveImport{ZipPath: tmp.Name(), SizeBytes: written}, cleanup, nil
}

func (s *Server) remoteJSON(ctx context.Context, remote LibraryRemote, method string, path string, body any, out any) error {
	_, err := s.remoteJSONAccepted(ctx, remote, method, path, body, out, nil)
	return err
}

func (s *Server) remoteJSONAccepted(ctx context.Context, remote LibraryRemote, method string, path string, body any, out any, accepted map[int]bool) (int, error) {
	base, err := url.Parse(remote.BaseURL)
	if err != nil {
		return 0, err
	}
	rel, err := url.Parse(path)
	if err != nil {
		return 0, err
	}
	endpoint := base.ResolveReference(rel)
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	timeout := libraryRemoteJSONTimeout()
	reqCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, endpoint.String(), reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := s.authorizeRemoteRequest(req, remote); err != nil {
		return 0, err
	}
	resp, err := libraryRemoteJSONHTTPClient(timeout).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if (resp.StatusCode < 200 || resp.StatusCode >= 300) && !accepted[resp.StatusCode] {
		return resp.StatusCode, fmt.Errorf("remote library returned %s", resp.Status)
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func libraryRemoteJSONTimeout() time.Duration {
	return firstConfiguredDuration(defaultLibraryRemoteJSONTimeout, "APEXLOAD_LIBRARY_REMOTE_JSON_TIMEOUT", "APEXLOAD_LIBRARY_REMOTE_JSON_TIMEOUT_SECONDS")
}

func libraryRemoteArchiveTimeout() time.Duration {
	return firstConfiguredDuration(defaultLibraryRemoteArchiveTimeout, "APEXLOAD_LIBRARY_REMOTE_ARCHIVE_TIMEOUT", "APEXLOAD_LIBRARY_REMOTE_ARCHIVE_TIMEOUT_SECONDS")
}

func libraryRemoteJSONHTTPClient(timeout time.Duration) *http.Client {
	return newGuardedHTTPClient(timeout)
}

func libraryRemoteArchiveHTTPClient(timeout time.Duration) *http.Client {
	return newGuardedHTTPClient(timeout)
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (s *Server) authorizeRemoteRequest(req *http.Request, remote LibraryRemote) error {
	apiKey, err := s.remoteLibraryAPIKey(remote)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return nil
}

func buildLibraryRemote(req LibraryRemoteRequest) (*LibraryRemote, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("remote name is required")
	}
	baseURL, err := normalizeLibraryRemoteBaseURL(req.BaseURL)
	if err != nil {
		return nil, err
	}
	authMethod := strings.TrimSpace(req.AuthMethod)
	if authMethod == "" {
		authMethod = "service_account"
	}
	now := time.Now().UTC()
	return &LibraryRemote{
		ID:               uuid.New().String(),
		Name:             name,
		BaseURL:          baseURL,
		DefaultProjectID: normalizeProjectID(req.DefaultProjectID),
		AuthMethod:       authMethod,
		CredentialRef:    strings.TrimSpace(req.CredentialRef),
		Status:           "unknown",
		APIKey:           strings.TrimSpace(req.APIKey),
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

func normalizeLibraryRemoteBaseURL(raw string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("remote base_url must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("remote base_url must use http or https")
	}
	if err := validateOutboundRemoteHost(parsed.Hostname()); err != nil {
		return "", fmt.Errorf("remote base_url rejected: %w", err)
	}
	return baseURL, nil
}

func (s *Server) libraryRemote(id string) (LibraryRemote, bool) {
	s.libraryMu.Lock()
	defer s.libraryMu.Unlock()
	remote, ok := s.libraryRemotes[id]
	if !ok || remote == nil {
		return LibraryRemote{}, false
	}
	copied := *remote
	return copied, true
}

func (s *Server) storeRemoteCredential(remote *LibraryRemote, apiKey string, credentialRef string) error {
	if remote == nil {
		return fmt.Errorf("remote library is required")
	}
	apiKey = strings.TrimSpace(apiKey)
	credentialRef = strings.TrimSpace(credentialRef)
	if strings.EqualFold(remote.AuthMethod, "none") {
		remote.CredentialRef = ""
		remote.APIKey = ""
		remote.CredentialConfigured = true
		return nil
	}
	if apiKey != "" {
		if s.libraryCreds == nil {
			return fmt.Errorf("remote credential store is not configured")
		}
		ref, err := s.libraryCreds.PutRemoteAPIKey(remote.ID, apiKey)
		if err != nil {
			return err
		}
		remote.CredentialRef = strings.TrimSpace(ref)
		remote.APIKey = ""
		remote.CredentialConfigured = true
		return nil
	}
	if credentialRef != "" {
		remote.CredentialRef = credentialRef
	}
	remote.APIKey = ""
	remote.CredentialConfigured = s.remoteCredentialConfigured(*remote)
	return nil
}

func (s *Server) remoteLibraryAPIKey(remote LibraryRemote) (string, error) {
	if strings.EqualFold(remote.AuthMethod, "none") {
		return "", nil
	}
	if strings.TrimSpace(remote.APIKey) != "" {
		return strings.TrimSpace(remote.APIKey), nil
	}
	if s.libraryCreds == nil {
		return "", fmt.Errorf("remote library credential store is not configured")
	}
	apiKey, err := s.libraryCreds.GetRemoteAPIKey(remote)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("remote library credential is not configured")
	}
	return strings.TrimSpace(apiKey), nil
}

func (s *Server) remoteCredentialConfigured(remote LibraryRemote) bool {
	if strings.EqualFold(remote.AuthMethod, "none") {
		return true
	}
	if strings.TrimSpace(remote.APIKey) != "" {
		return true
	}
	if strings.TrimSpace(remote.CredentialRef) == "" || s.libraryCreds == nil {
		return false
	}
	secret, err := s.libraryCreds.GetRemoteAPIKey(remote)
	return err == nil && strings.TrimSpace(secret) != ""
}

func (s *Server) sanitizeLibraryRemote(remote *LibraryRemote) LibraryRemote {
	if remote == nil {
		return LibraryRemote{}
	}
	copied := *remote
	copied.APIKey = ""
	copied.CredentialConfigured = s.remoteCredentialConfigured(copied)
	return copied
}
