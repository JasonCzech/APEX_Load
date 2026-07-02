package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// nowRFC3339 returns the current UTC time as RFC3339. A var so tests can stub it.
var nowRFC3339 = func() string { return time.Now().UTC().Format(time.RFC3339) }

const (
	recentsFileName = "recent.json"
	maxRecentItems  = 12
)

// recentItem is a recently-opened workspace entry shown on the desktop home.
type recentItem struct {
	Kind     string `json:"kind"`               // "script" | "import" | "test" | ...
	ID       string `json:"id"`                 // stable identifier within the kind
	Name     string `json:"name"`               // display label
	Route    string `json:"route"`              // dashboard hash route to reopen
	OpenedAt string `json:"openedAt,omitempty"` // RFC3339 timestamp, stamped server-side on add
	Pinned   bool   `json:"pinned,omitempty"`   // user-pinned: sorts first, exempt from the cap
}

// mergeRecent prepends next, de-duplicates by kind+id (keeping the newest
// position), and caps the list — but never evicts pinned items. Pure + testable.
func mergeRecent(existing []recentItem, next recentItem, limit int) []recentItem {
	out := make([]recentItem, 0, len(existing)+1)
	out = append(out, next)
	for _, item := range existing {
		if item.Kind == next.Kind && item.ID == next.ID {
			continue
		}
		out = append(out, item)
	}
	if limit <= 0 || len(out) <= limit {
		return out
	}
	// Cap while preserving order: keep all pinned items plus the most-recent
	// unpinned ones up to the remaining budget.
	pinned := 0
	for _, it := range out {
		if it.Pinned {
			pinned++
		}
	}
	unpinnedBudget := limit - pinned
	if unpinnedBudget < 0 {
		unpinnedBudget = 0
	}
	kept := make([]recentItem, 0, len(out))
	usedUnpinned := 0
	for i, it := range out {
		if it.Pinned {
			kept = append(kept, it)
			continue
		}
		if i == 0 {
			kept = append(kept, it)
			usedUnpinned++
			continue
		}
		if usedUnpinned < unpinnedBudget {
			kept = append(kept, it)
			usedUnpinned++
		}
	}
	return kept
}

func recentsPath(appDataPath string) string {
	return filepath.Join(appDataPath, recentsFileName)
}

func loadRecents(appDataPath string) []recentItem {
	if appDataPath == "" {
		return nil
	}
	data, err := os.ReadFile(recentsPath(appDataPath))
	if err != nil {
		return nil
	}
	var items []recentItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil
	}
	return items
}

func saveRecents(appDataPath string, items []recentItem) error {
	if appDataPath == "" {
		return fmt.Errorf("app data path is not available")
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	path := recentsPath(appDataPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), recentsFileName+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (a *App) emitRecentsChanged() {
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, eventRecentsChanged)
	}
}

// GetRecentItems returns the recent-items list as a JSON array string.
func (a *App) GetRecentItems() (string, error) {
	a.recentsMu.Lock()
	defer a.recentsMu.Unlock()
	items := loadRecents(a.appDataPath)
	if items == nil {
		items = []recentItem{}
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// AddRecentItem records a recently-opened item (passed as JSON). Invalid or
// empty items are ignored.
func (a *App) AddRecentItem(itemJSON string) error {
	itemJSON = strings.TrimSpace(itemJSON)
	if itemJSON == "" {
		return nil
	}
	var item recentItem
	if err := json.Unmarshal([]byte(itemJSON), &item); err != nil {
		return fmt.Errorf("invalid recent item: %w", err)
	}
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Kind) == "" {
		return nil
	}
	a.recentsMu.Lock()
	defer a.recentsMu.Unlock()
	item.OpenedAt = nowRFC3339() // stamp server-side so ordering/age is trustworthy
	existing := loadRecents(a.appDataPath)
	// Carry a prior pin across the re-add so opening a pinned item keeps it pinned.
	for _, prev := range existing {
		if prev.Kind == item.Kind && prev.ID == item.ID && prev.Pinned {
			item.Pinned = true
			break
		}
	}
	merged := mergeRecent(existing, item, maxRecentItems)
	if err := saveRecents(a.appDataPath, merged); err != nil {
		return err
	}
	a.rebuildMenu() // refresh the native "Open Recent" submenu
	a.emitRecentsChanged()
	return nil
}

// ToggleRecentPin flips the pinned state of a recent item (no-op if not found).
func (a *App) ToggleRecentPin(kind, id string) error {
	a.recentsMu.Lock()
	defer a.recentsMu.Unlock()
	items := loadRecents(a.appDataPath)
	changed := false
	for i := range items {
		if items[i].Kind == kind && items[i].ID == id {
			items[i].Pinned = !items[i].Pinned
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	if err := saveRecents(a.appDataPath, items); err != nil {
		return err
	}
	a.rebuildMenu()
	a.emitRecentsChanged()
	return nil
}

// ClearRecentItems removes all recent items.
func (a *App) ClearRecentItems() error {
	a.recentsMu.Lock()
	defer a.recentsMu.Unlock()
	if err := saveRecents(a.appDataPath, []recentItem{}); err != nil {
		return err
	}
	a.rebuildMenu()
	a.emitRecentsChanged()
	return nil
}
