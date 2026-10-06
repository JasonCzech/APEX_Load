package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"time"
)

const browserResourceCursorKeySetting = "security.browser_resource_cursor_hmac_key.v1"
const browserResourceCursorDomain = "apexload.browser_resource_cursor.v1"
const browserResourceCursorTTL = 15 * time.Minute

var errBrowserResourceUnavailable = errors.New("browser resource reader durable capability is unavailable")
var errBrowserResourceCursor = errors.New("browser resource cursor is invalid or expired")

type browserResourceCursorKeyStore interface {
	GetSettingContext(context.Context, string) (string, error)
	CompareAndSwapSettingContext(context.Context, string, string, string) (bool, error)
}

type browserResourceCursorPayload struct {
	Version           int                    `json:"version"`
	TestID            string                 `json:"test_id"`
	ProjectID         string                 `json:"project_id"`
	AuthorityRevision string                 `json:"authority_revision"`
	ManifestSHA256    string                 `json:"manifest_sha256"`
	ArtifactSHA256    string                 `json:"artifact_sha256"`
	ArtifactSize      int64                  `json:"artifact_size"`
	SourceFormat      string                 `json:"source_format"`
	SourceRevision    string                 `json:"source_revision"`
	QuerySHA256       string                 `json:"query_sha256"`
	Boundary          browserResourceSortKey `json:"boundary"`
	ExpiresAt         time.Time              `json:"expires_at"`
}

func browserResourceCursorShape(raw string) bool {
	if len(raw) == 0 || len(raw) > maxBrowserResourceCursorBytes || strings.Count(raw, ".") != 1 {
		return false
	}
	parts := strings.SplitN(raw, ".", 2)
	if len(parts[0]) == 0 || len(parts[1]) != 43 {
		return false
	}
	for _, part := range parts {
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return false
			}
		}
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	return err == nil && len(signature) == sha256.Size
}
func browserResourceReadDigest(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func browserResourceQueryDigest(q BrowserResourcePageQuery) string {
	return browserResourceReadDigest(q)
}

func (s *Server) browserResourceCursorKey(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, errBrowserResourceUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, ok := s.store.(browserResourceCursorKeyStore)
	if !ok {
		return nil, errBrowserResourceUnavailable
	}
	raw, err := store.GetSettingContext(ctx, browserResourceCursorKeySetting)
	if err != nil {
		return nil, browserResourceContextOr(ctx, errBrowserResourceUnavailable)
	}
	if raw == "" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, errBrowserResourceUnavailable
		}
		encoded := base64.RawURLEncoding.EncodeToString(key)
		won, err := store.CompareAndSwapSettingContext(ctx, browserResourceCursorKeySetting, "", encoded)
		if err != nil {
			return nil, browserResourceContextOr(ctx, errBrowserResourceUnavailable)
		}
		if won {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return key, nil
		}
		raw, err = store.GetSettingContext(ctx, browserResourceCursorKeySetting)
		if err != nil {
			return nil, browserResourceContextOr(ctx, errBrowserResourceUnavailable)
		}
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errBrowserResourceUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return key, nil
}
func browserResourceContextOr(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}
func browserResourceSealCursor(key []byte, payload browserResourceCursorPayload) (string, error) {
	if len(key) != 32 {
		return "", errBrowserResourceUnavailable
	}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > 3000 {
		return "", errBrowserResourceCursor
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(browserResourceCursorDomain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(raw)
	cursor := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !browserResourceCursorShape(cursor) {
		return "", errBrowserResourceCursor
	}
	return cursor, nil
}
func browserResourceOpenCursor(key []byte, raw string, q BrowserResourcePageQuery, now time.Time) (*browserResourceCursorPayload, error) {
	if len(key) != 32 || !browserResourceCursorShape(raw) {
		return nil, errBrowserResourceCursor
	}
	parts := strings.SplitN(raw, ".", 2)
	body, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil || len(body) > 3000 {
		return nil, errBrowserResourceCursor
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return nil, errBrowserResourceCursor
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(browserResourceCursorDomain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errBrowserResourceCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	tokens := 4096
	if !browserResourcePageWireValue(decoder, reflect.TypeOf(browserResourceCursorPayload{}), false, 0, &tokens) {
		return nil, errBrowserResourceCursor
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errBrowserResourceCursor
	}
	var payload browserResourceCursorPayload
	if json.Unmarshal(body, &payload) != nil || payload.Version != 1 || payload.TestID == "" || payload.ProjectID == "" || !browserResourceReadText(payload.TestID, 128) || !browserResourceReadText(payload.ProjectID, 128) || payload.SourceFormat != "jsonl" && payload.SourceFormat != "parquet" || payload.ArtifactSize < 0 || payload.ArtifactSize > maxArchiveQueryBytes || payload.QuerySHA256 != browserResourceQueryDigest(q) || !browserResourceSortKeyValid(payload.Boundary, q) || payload.ExpiresAt.Location() != time.UTC || !now.Before(payload.ExpiresAt) || payload.ExpiresAt.After(now.Add(browserResourceCursorTTL)) {
		return nil, errBrowserResourceCursor
	}
	for _, digest := range []string{payload.AuthorityRevision, payload.ManifestSHA256, payload.ArtifactSHA256, payload.SourceRevision, payload.QuerySHA256} {
		if !browserResourceReadSHA(digest) {
			return nil, errBrowserResourceCursor
		}
	}
	return &payload, nil
}
