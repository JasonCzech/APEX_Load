package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func resourceCursorTestPayload(q BrowserResourcePageQuery, now time.Time) browserResourceCursorPayload {
	return browserResourceCursorPayload{Version: 1, TestID: "run-1", ProjectID: "project-1", AuthorityRevision: strings.Repeat("a", 64), ManifestSHA256: strings.Repeat("b", 64), ArtifactSHA256: strings.Repeat("c", 64), ArtifactSize: 1024, SourceFormat: "jsonl", SourceRevision: strings.Repeat("d", 64), QuerySHA256: browserResourceQueryDigest(q), Boundary: browserResourceSortKey{Available: true, TransactionOrdinal: 1, ResourceOrdinal: 50}, ExpiresAt: now.Add(browserResourceCursorTTL)}
}
func TestBrowserResourceCursorBindsExactSourceQueryAndBoundary(t *testing.T) {
	now := time.Unix(1700000000, 123456789).UTC()
	q := resourcePageTestQuery(t, nil)
	key := []byte(strings.Repeat("k", 32))
	payload := resourceCursorTestPayload(q, now)
	cursor, err := browserResourceSealCursor(key, payload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := browserResourceOpenCursor(key, cursor, q, now)
	if err != nil || *got != payload {
		t.Fatalf("valid cursor drifted: %+v %v", got, err)
	}
	altered := q
	altered.Limit = 20
	if _, err := browserResourceOpenCursor(key, cursor, altered, now); !errors.Is(err, errBrowserResourceCursor) {
		t.Fatal("query change admitted")
	}
	for name, mutate := range map[string]func(*browserResourceCursorPayload){"missing_digest": func(p *browserResourceCursorPayload) { p.ArtifactSHA256 = "" }, "future": func(p *browserResourceCursorPayload) { p.Version = 2 }, "unavailable_value": func(p *browserResourceCursorPayload) { p.Boundary.Available = false; p.Boundary.Value = 1 }, "invalid_ordinal": func(p *browserResourceCursorPayload) { p.Boundary.ResourceOrdinal = 0 }, "expiry": func(p *browserResourceCursorPayload) { p.ExpiresAt = now }, "unbounded_expiry": func(p *browserResourceCursorPayload) { p.ExpiresAt = now.Add(2 * browserResourceCursorTTL) }, "empty_project": func(p *browserResourceCursorPayload) { p.ProjectID = "" }} {
		t.Run(name, func(t *testing.T) {
			candidate := payload
			mutate(&candidate)
			raw, err := browserResourceSealCursor(key, candidate)
			if err != nil {
				return
			}
			if _, err := browserResourceOpenCursor(key, raw, q, now); err == nil {
				t.Fatal("signed impossible cursor admitted")
			}
		})
	}
	for _, candidate := range []string{cursor + "x", " " + cursor, strings.Repeat("x", 4097), "not-a-cursor"} {
		if _, err := browserResourceOpenCursor(key, candidate, q, now); err == nil {
			t.Fatal("malformed or altered cursor admitted")
		}
	}
	if _, err := browserResourceOpenCursor([]byte(strings.Repeat("z", 32)), cursor, q, now); err == nil {
		t.Fatal("cursor accepted by unrelated durable key")
	}
}
func TestBrowserResourceCursorKeyReopensAndInvalidKeyFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resource-key.db")
	store, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	one := &Server{store: store}
	first, err := one.browserResourceCursorKey(context.Background())
	if err != nil || len(first) != 32 {
		t.Fatalf("initialize durable key: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	second, err := (&Server{store: reopened}).browserResourceCursorKey(context.Background())
	if err != nil || string(first) != string(second) {
		t.Fatalf("restart changed cursor authority: %v", err)
	}
	if err := reopened.SaveSetting(browserResourceCursorKeySetting, "invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Server{store: reopened}).browserResourceCursorKey(context.Background()); !errors.Is(err, errBrowserResourceUnavailable) {
		t.Fatalf("invalid durable key was replaced or accepted: %v", err)
	}
	if _, err := (&Server{}).browserResourceCursorKey(context.Background()); !errors.Is(err, errBrowserResourceUnavailable) {
		t.Fatalf("memory-only unsupported store silently minted key: %v", err)
	}
}
