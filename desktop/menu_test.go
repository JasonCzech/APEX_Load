package main

import (
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
		quit := labeledItem(file, "Quit")
		if quit == nil {
			t.Fatalf("%s: missing File>Quit", goos)
		}
		if quit.Click == nil {
			t.Errorf("%s: File>Quit has no callback (regression: empty Quit)", goos)
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
		for _, label := range []string{"Zoom In", "Zoom Out", "Actual Size"} {
			if labeledItem(view, label) == nil {
				t.Errorf("%s: View missing %q", goos, label)
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
	if labeledItem(recent2, "Clear Menu") == nil {
		t.Error("Open Recent should offer 'Clear Menu' when items exist")
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
	}
}
