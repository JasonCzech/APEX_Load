package main

import (
	"context"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/menu"
)

func hasRole(m *menu.Menu, role menu.Role) bool {
	for _, item := range m.Items {
		if item.Role == role {
			return true
		}
	}
	return false
}

func TestReloadDoesNotRetireRendererBeforeCancelableNavigationCommits(t *testing.T) {
	app := NewApp()
	app.setRuntimeContext(context.Background())
	app.rendererReady = true
	app.rendererSession = "renderer-stays-active"
	originalReload := windowReloadApp
	reloads := 0
	windowReloadApp = func(context.Context) { reloads++ }
	t.Cleanup(func() { windowReloadApp = originalReload })

	app.reload(nil)

	if reloads != 1 {
		t.Fatalf("native reload calls = %d, want 1", reloads)
	}
	if !app.rendererReady || app.rendererSession != "renderer-stays-active" {
		t.Fatal("reload retired the renderer before navigation committed")
	}
}

func submenu(m *menu.Menu, label string) *menu.Menu {
	for _, item := range m.Items {
		if item.Label == label && item.SubMenu != nil {
			return item.SubMenu
		}
	}
	return nil
}

func labeledItem(m *menu.Menu, label string) *menu.MenuItem {
	for _, item := range m.Items {
		if item.Label == label {
			return item
		}
	}
	return nil
}

// TestBuildAppMenuMacUsesEditRole guards the copy/paste fix: on macOS the menu
// must expose the role-based Edit/App/Window menus (the only way WKWebView wires
// ⌘C/⌘V/⌘X/⌘A/⌘Z), and must NOT carry a custom File>Quit (Quit lives in the App
// menu role there).
func TestBuildAppMenuMacUsesEditRole(t *testing.T) {
	app := NewApp()
	m := buildAppMenuForOS(app, "darwin")

	for _, role := range []menu.Role{menu.AppMenuRole, menu.EditMenuRole, menu.WindowMenuRole} {
		if !hasRole(m, role) {
			t.Errorf("darwin menu missing role %d", role)
		}
	}
	if file := submenu(m, "File"); file != nil && labeledItem(file, "Quit") != nil {
		t.Error("darwin should rely on the App-menu Quit role, not a custom File>Quit")
	}
}

// TestBuildAppMenuNonMacHasFunctionalQuit verifies that off macOS we avoid the
// role-based items (no-ops there) and provide a real, wired Quit — guarding
// against the previous empty-callback Quit regression. We also confirm we do not
// add accelerator-bearing Edit items that would intercept the webview's native
// copy/paste handling.
func TestBuildAppMenuNonMacHasFunctionalQuit(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		app := NewApp()
		m := buildAppMenuForOS(app, goos)

		for _, role := range []menu.Role{menu.AppMenuRole, menu.EditMenuRole, menu.WindowMenuRole} {
			if hasRole(m, role) {
				t.Errorf("%s: role-based menu items are no-ops off darwin and must not be used (role %d)", goos, role)
			}
		}
		if submenu(m, "Edit") != nil {
			t.Errorf("%s: must not add an Edit menu (accelerators would break native webview copy/paste)", goos)
		}

		file := submenu(m, "File")
		if file == nil {
			t.Fatalf("%s: missing File menu", goos)
		}
		if labeledItem(file, "Print…") == nil {
			t.Errorf("%s: File menu missing Print…", goos)
		}
		// Windows names it Exit.
		quitLabel := map[string]string{"windows": "Exit", "linux": "Quit"}[goos]
		quit := labeledItem(file, quitLabel)
		if quit == nil {
			t.Fatalf("%s: missing File>%s", goos, quitLabel)
		}
		if quit.Click == nil {
			t.Errorf("%s: File>%s has no callback (regression: empty Quit)", goos, quitLabel)
		}
		for _, label := range []string{"Open Logs Folder", "Open App Data Folder"} {
			if labeledItem(file, label) == nil {
				t.Errorf("%s: File menu missing %q", goos, label)
			}
		}
	}
}

// TestBuildAppMenuViewZoom confirms the View menu exposes the zoom actions on
// every platform (these drive the in-app --dashboard-zoom via menu:zoom events).
func TestBuildAppMenuViewZoom(t *testing.T) {
	app := NewApp()
	for _, goos := range []string{"darwin", "windows", "linux"} {
		m := buildAppMenuForOS(app, goos)
		view := submenu(m, "View")
		if view == nil {
			t.Fatalf("%s: missing View menu", goos)
		}
		labels := []string{"Zoom In", "Zoom Out", "Actual Size"}
		if goos == "windows" {
			// Wails cannot map "=" or "-" on Windows: the labels show the keys
			// the page handles, and the items carry no accelerator of their own.
			labels = []string{"Zoom In\tCtrl+=", "Zoom Out\tCtrl+-", "Actual Size"}
		}
		for i, label := range labels {
			item := labeledItem(view, label)
			if item == nil {
				t.Errorf("%s: View missing %q", goos, label)
				continue
			}
			if wantKey := goos != "windows" || i == 2; (item.Accelerator != nil) != wantKey {
				t.Errorf("%s: %q accelerator = %v, want one: %v", goos, label, item.Accelerator, wantKey)
			}
		}
	}
}

// TestBuildAppMenuOpenRecent verifies the File menu carries an "Open Recent"
// submenu that lists persisted recents (and shows a disabled placeholder when
// empty).
func TestBuildAppMenuOpenRecent(t *testing.T) {
	// Empty recents → disabled placeholder.
	app := NewApp()
	file := submenu(buildAppMenuForOS(app, "darwin"), "File")
	if file == nil {
		t.Fatal("missing File menu")
	}
	recent := submenu(file, "Open Recent")
	if recent == nil {
		t.Fatal("File menu missing Open Recent submenu")
	}
	placeholder := labeledItem(recent, "No Recent Items")
	if placeholder == nil || !placeholder.Disabled {
		t.Error("empty recents should show a disabled 'No Recent Items' item")
	}

	// With persisted recents → they appear as items.
	app2 := NewApp()
	app2.appDataPath = t.TempDir()
	if err := saveRecents(app2.appDataPath, []recentItem{
		{Kind: "script", ID: "s1", Name: "Order flow", Route: "#/scripts/s1"},
	}); err != nil {
		t.Fatal(err)
	}
	recent2 := submenu(submenu(buildAppMenuForOS(app2, "windows"), "File"), "Open Recent")
	if recent2 == nil {
		t.Fatal("missing Open Recent submenu (windows)")
	}
	if labeledItem(recent2, "Order flow") == nil {
		t.Error("Open Recent should list the persisted recent 'Order flow'")
	}
	if labeledItem(recent2, "Clear Recent Items") == nil {
		t.Error("Open Recent should offer 'Clear Recent Items' on Windows when items exist")
	}
	macRecent := submenu(submenu(buildAppMenuForOS(app2, "darwin"), "File"), "Open Recent")
	if labeledItem(macRecent, "Clear Menu") == nil {
		t.Error("Open Recent should offer 'Clear Menu' on macOS when items exist")
	}
}

func TestPlatformMenusLoadRecentsBeforeRemainingStatic(t *testing.T) {
	app := NewApp()
	appDataPath := t.TempDir()
	t.Setenv("APEXLOAD_DESKTOP_APP_DATA", appDataPath)
	if err := saveRecents(appDataPath, []recentItem{
		{Kind: "script", ID: "s1", Name: "Order flow", Route: "#/scripts/s1"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		app.prepareInitialMenuForOS(goos)
		file := submenu(buildAppMenuForOS(app, goos), "File")
		if file == nil {
			t.Fatalf("%s menu missing File menu", goos)
		}
		recent := submenu(file, "Open Recent")
		if recent == nil || labeledItem(recent, "Order flow") == nil {
			t.Fatalf("%s initial static menu did not preload persisted recents", goos)
		}
		if supportsDynamicApplicationMenuForOS(goos) {
			t.Errorf("%s must reject dynamic application-menu replacement", goos)
		}
	}
}

// TestBuildAppMenuHelpHasAbout verifies the Help menu can open the in-app About
// dialog on all platforms.
func TestBuildAppMenuHelpHasAbout(t *testing.T) {
	app := NewApp()
	for _, goos := range []string{"darwin", "windows", "linux"} {
		m := buildAppMenuForOS(app, goos)
		help := submenu(m, "Help")
		if help == nil {
			t.Fatalf("%s: missing Help menu", goos)
		}
		if labeledItem(help, "About APEX Load") == nil {
			t.Errorf("%s: Help missing About entry", goos)
		}
		openBrowser := labeledItem(help, "Open in System Browser")
		if openBrowser == nil || openBrowser.Click == nil {
			t.Errorf("%s: Help browser entry must emit a renderer-owned context action", goos)
		}
		// In-app help and the shortcut list are reachable from the native menu.
		for _, label := range []string{"APEX Load Help", "Keyboard Shortcuts…"} {
			if item := labeledItem(help, label); item == nil || item.Click == nil {
				t.Errorf("%s: Help missing %q", goos, label)
			}
		}
	}
}
