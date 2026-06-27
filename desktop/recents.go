package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
}

// mergeRecent prepends next, de-duplicates by kind+id (keeping the newest
// position), and caps the list. Pure + unit-testable.
func mergeRecent(existing []recentItem, next recentItem, limit int) []recentItem {
	out := make([]recentItem, 0, len(existing)+1)
	out = append(out, next)
	for _, item := range existing {
		if item.Kind == next.Kind && item.ID == next.ID {
			continue
		}
		out = append(out, item)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
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
	return os.WriteFile(recentsPath(appDataPath), data, 0o644)
}

// GetRecentItems returns the recent-items list as a JSON array string.
func (a *App) GetRecentItems() (string, error) {
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
	item.OpenedAt = nowRFC3339() // stamp server-side so ordering/age is trustworthy
	merged := mergeRecent(loadRecents(a.appDataPath), item, maxRecentItems)
	if err := saveRecents(a.appDataPath, merged); err != nil {
		return err
	}
	a.rebuildMenu() // refresh the native "Open Recent" submenu
	return nil
}

// ClearRecentItems removes all recent items.
func (a *App) ClearRecentItems() error {
	if err := saveRecents(a.appDataPath, []recentItem{}); err != nil {
		return err
	}
	a.rebuildMenu()
	return nil
}
