package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/api"
)

const maxDesktopAPIErrorBody = 8 << 10

// SaveAPIResourceNative streams a GET from the embedded controller directly to
// a user-selected file. The response never crosses the Wails bridge as a Blob
// or base64 payload, which keeps multi-gigabyte archives bounded by the HTTP and
// filesystem buffers instead of renderer memory.
//
// resourcePath must be an absolute-path reference below /api/v1. Absolute URLs
// are deliberately rejected: the native side constructs the destination from
// its own verified loopback origin and attaches the per-launch capability
// itself, so renderer input cannot exfiltrate that capability to another host.
// The returned path is empty when the user cancels the save dialog.
func (a *App) SaveAPIResourceNative(resourcePath, defaultName, filtersJSON, devViewMode, projectID string) (string, error) {
	origin, err := a.GetAPIOrigin()
	if err != nil {
		return "", err
	}
	resourcePath, err = validateDesktopAPIResourcePath(resourcePath)
	if err != nil {
		return "", err
	}
	preview, err := api.ParseDesktopDevPreviewContext(devViewMode, projectID)
	if err != nil {
		return "", err
	}

	destination, err := a.SaveFileDialogNative("Save", safeDesktopDownloadName(defaultName), filtersJSON)
	if err != nil || destination == "" {
		return destination, err
	}
	grant, ok := a.writeGrant(destination)
	if !ok {
		return "", fmt.Errorf("the selected save destination could not be authorized")
	}

	ctx := a.runtimeContext()
	if ctx == nil {
		return "", fmt.Errorf("desktop runtime is not ready")
	}

	if err := streamDesktopAPIResource(ctx, newDesktopAPIHTTPClient(), origin, a.GetAPICapability(), resourcePath, preview, grant); err != nil {
		return "", err
	}
	if err := a.refreshDownloadedWriteGrant(destination, grant); err != nil {
		return "", err
	}
	return destination, nil
}

func safeDesktopDownloadName(name string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7f, r == '/', r == '\\':
			return '-'
		default:
			return r
		}
	}, name))
	name = strings.ReplaceAll(name, "..", "-")
	name = strings.Trim(name, ". ")
	if name == "" {
		return "download"
	}
	return name
}

func validateDesktopAPIResourcePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("API resource path is required")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return "", fmt.Errorf("invalid API resource path: %w", err)
	}
	if u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("API resource must be a same-controller path")
	}
	if !strings.HasPrefix(u.Path, "/api/v1/") || strings.HasPrefix(u.Path, "//") || strings.Contains(u.Path, "\\") {
		return "", fmt.Errorf("API resource must be below /api/v1")
	}
	// Reject dot-segment spellings instead of relying on ServeMux redirects or
	// normalization. This keeps the exact endpoint reviewed by the renderer the
	// same endpoint fetched by the native process.
	if cleaned := path.Clean(u.Path); cleaned != u.Path {
		return "", fmt.Errorf("API resource path must not contain dot segments")
	}
	return u.RequestURI(), nil
}

func newDesktopAPIHTTPClient() *http.Client {
	return &http.Client{
		// Download endpoints are not expected to redirect. Refusing every redirect
		// also guarantees the desktop capability is never copied to a second URL.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func streamDesktopAPIResource(
	ctx context.Context,
	client *http.Client,
	origin string,
	capability string,
	resourcePath string,
	preview api.DesktopDevPreviewContext,
	grant writePathGrant,
) error {
	if ctx == nil {
		return fmt.Errorf("download context is required")
	}
	if client == nil {
		return fmt.Errorf("HTTP client is required")
	}
	if err := validateLoopbackAPIOrigin(origin); err != nil {
		return err
	}
	resourcePath, err := validateDesktopAPIResourcePath(resourcePath)
	if err != nil {
		return err
	}
	capability = strings.TrimSpace(capability)
	if capability == "" {
		return fmt.Errorf("desktop API capability is unavailable")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(origin, "/")+resourcePath, nil)
	if err != nil {
		return fmt.Errorf("create desktop API download: %w", err)
	}
	req.Header.Set(api.DesktopCapabilityHeader, capability)
	preview.Apply(req.Header)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download desktop API resource: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, maxDesktopAPIErrorBody))
		detail := strings.TrimSpace(string(message))
		if detail == "" {
			return fmt.Errorf("desktop API download returned %s", resp.Status)
		}
		return fmt.Errorf("desktop API download returned %s: %s", resp.Status, detail)
	}
	if err := secureWriteGrantedFileFrom(grant, resp.Body, 0); err != nil {
		return fmt.Errorf("save desktop API download: %w", err)
	}
	return nil
}

func (a *App) refreshDownloadedWriteGrant(path string, previous writePathGrant) error {
	_, refreshed, err := newWritePathGrant(filepath.Join(previous.canonicalParent, previous.name))
	if err != nil {
		return fmt.Errorf("saved file could not be re-verified: %w", err)
	}
	key := cleanBridgePath(path)
	a.pathMu.Lock()
	defer a.pathMu.Unlock()
	current, stillGranted := a.writePaths[key]
	if stillGranted && current.name == previous.name && os.SameFile(current.parentIdentity, previous.parentIdentity) {
		a.writePaths[key] = refreshed
	}
	return nil
}
