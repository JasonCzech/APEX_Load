package main

import (
	"runtime"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Menu event names emitted to the dashboard over the Wails event bus. The
// dashboard subscribes via window.runtime.EventsOn and maps them to in-app
// actions, because several "native" affordances are actually owned by the React
// app rather than the OS shell: webview zoom is implemented with the
// --dashboard-zoom CSS variable (Wails v2 has no stable runtime zoom API), the
// About panel is an in-app dialog so it can show live build info on every
// platform, and File actions translate to dashboard navigation.
const (
	menuEventZoom        = "menu:zoom"           // payload: "in" | "out" | "reset"
	menuEventAbout       = "menu:about"          // open the in-app About dialog
	menuEventAction      = "menu:action"         // payload: action id ("new-test" | "open-script" | "import" | "help" | "shortcuts")
	menuEventOpenBrowser = "menu:open-browser"   // renderer transfers its current dev preview/project context
	eventOpenRecent      = "desktop:open-recent" // payload: the recent item's dashboard route
	eventRecentsChanged  = "desktop:recents-changed"
)

// buildAppMenu constructs the native application menu for the host OS.
func buildAppMenu(app *App) *menu.Menu {
	return buildAppMenuForOS(app, runtime.GOOS)
}

// buildAppMenuForOS builds the menu for an explicit GOOS so it is unit-testable
// off the host platform.
//
// Menu roles (AppMenu/EditMenu/WindowMenu) are honored only on macOS — the Wails
// frontend processes Role solely in the darwin backend. So on macOS we use the
// role-based Edit menu, which is what wires ⌘C/⌘V/⌘X/⌘A/⌘Z to the WKWebView
// page. The previous menu replaced the macOS default menu with a single
// empty-callback "Quit" item, which stripped the Edit menu and disabled
// copy/paste/undo in the script editor, JSON viewers, and inputs.
//
// On Windows (WebView2) and Linux (WebKitGTK) the webview natively handles those
// editing shortcuts, so we deliberately do NOT add accelerator-bearing Edit
// items there: a menu accelerator would intercept the keystroke before the
// webview and break copy/paste.
func buildAppMenuForOS(app *App, goos string) *menu.Menu {
	isMac := goos == "darwin"
	appMenu := menu.NewMenu()

	if isMac {
		// Standard macOS application menu: About, Services, Hide, Quit (⌘Q).
		appMenu.Append(menu.AppMenu())
	}

	fileMenu := appMenu.AddSubmenu("File")
	fileMenu.AddText("New Run", keys.CmdOrCtrl("n"), app.menuAction("new-test"))
	fileMenu.AddText("Open Script…", keys.CmdOrCtrl("o"), app.menuAction("open-script"))
	fileMenu.AddText("Import…", keys.Combo("i", keys.CmdOrCtrlKey, keys.ShiftKey), app.menuAction("import"))
	app.appendOpenRecentSubmenu(fileMenu)
	fileMenu.AddSeparator()
	fileMenu.AddText("Reveal Logs Folder", nil, func(_ *menu.CallbackData) { _ = app.OpenLogsFolder() })
	fileMenu.AddText("Reveal App Data Folder", nil, func(_ *menu.CallbackData) { _ = app.OpenAppDataFolder() })
	fileMenu.AddSeparator()
	fileMenu.AddText("Print…", keys.CmdOrCtrl("p"), app.printWindow)
	if !isMac {
		// macOS exposes Quit through the role-based App menu; elsewhere File owns it.
		fileMenu.AddSeparator()
		fileMenu.AddText("Quit", keys.CmdOrCtrl("q"), app.quit)
	}

	if isMac {
		appMenu.Append(menu.EditMenu())
	}

	viewMenu := appMenu.AddSubmenu("View")
	viewMenu.AddText("Reload", keys.CmdOrCtrl("r"), app.reload)
	viewMenu.AddSeparator()
	viewMenu.AddText("Zoom In", keys.CmdOrCtrl("="), app.menuZoom("in"))
	viewMenu.AddText("Zoom Out", keys.CmdOrCtrl("-"), app.menuZoom("out"))
	viewMenu.AddText("Actual Size", keys.CmdOrCtrl("0"), app.menuZoom("reset"))

	if isMac {
		appMenu.Append(menu.WindowMenu())
	}

	helpMenu := appMenu.AddSubmenu("Help")
	// In-app help and the shortcut list are owned by the dashboard; the menu
	// only asks for them, like File's actions.
	helpMenu.AddText("APEX Load Help", nil, app.menuAction("help"))
	helpMenu.AddText("Keyboard Shortcuts…", nil, app.menuAction("shortcuts"))
	helpMenu.AddSeparator()
	helpMenu.AddText("About APEX Load", nil, app.menuEmit(menuEventAbout))
	// The renderer owns the live development preview/project selection. Route
	// this menu action through it so the native call cannot silently fall back to
	// an elevated admin context.
	helpMenu.AddText("Open in System Browser", nil, app.menuEmit(menuEventOpenBrowser))

	return appMenu
}

// supportsDynamicApplicationMenuForOS reports whether Wails can safely replace
// an already-visible native menu on the platform. In Wails v2.13's Linux
// backend, SetApplicationMenu allocates a new GtkMenuBar after the original has
// already been packed into the window. The replacement remains unparented and,
// worse, resets the callback maps used by the still-visible original menu.
// Keep every platform's initial menu static. Wails v2.13 cannot safely replace
// Linux's already-parented GtkMenuBar. Windows replacements leak old HMENU trees
// and stale callback maps. macOS replacements construct AppKit objects off the
// main thread and retain old items in package-global maps. prepareInitialMenu
// resolves app data before Wails packs the one safe menu so it can still contain
// persisted recents; changes made during the launch remain available in the
// dashboard and appear in the native menu on the next launch.
func supportsDynamicApplicationMenuForOS(goos string) bool {
	return false
}

// prepareInitialMenu resolves app-data without mutating or migrating it before
// Wails constructs the platform menu. Windows' legacy-directory migration stays
// in OnStartup behind single-instance coordination.
func (a *App) prepareInitialMenu() {
	a.prepareInitialMenuForOS(runtime.GOOS)
}

func (a *App) prepareInitialMenuForOS(goos string) {
	if goos != "darwin" && goos != "windows" && goos != "linux" {
		return
	}
	appDataPath, err := desktopAppDataPathForInitialMenu()
	if err != nil {
		return
	}
	a.publishAppDataPath(appDataPath)
}

// menuEmit returns a menu callback that emits a no-payload dashboard event.
func (a *App) menuEmit(event string) menu.Callback {
	return func(_ *menu.CallbackData) {
		a.emitOrQueueRendererEvent(event, "")
	}
}

// menuAction emits a menu:action event carrying the action id.
func (a *App) menuAction(action string) menu.Callback {
	return func(_ *menu.CallbackData) {
		a.emitOrQueueRendererEvent(menuEventAction, action)
	}
}

// menuZoom emits a menu:zoom event with the requested zoom action.
func (a *App) menuZoom(action string) menu.Callback {
	return func(_ *menu.CallbackData) {
		a.emitOrQueueRendererEvent(menuEventZoom, action)
	}
}

// appendOpenRecentSubmenu adds a native "Open Recent" submenu populated from the
// persisted recents. On supported platforms, rebuildMenu re-applies the whole
// menu whenever recents change (see AddRecentItem/ClearRecentItems). Linux
// captures this submenu once before startup because Wails v2.13 cannot safely
// replace platform menus repeatedly.
func (a *App) appendOpenRecentSubmenu(parent *menu.Menu) {
	recent := parent.AddSubmenu("Open Recent")
	items := loadRecents(a.appDataDirectory())
	if len(items) == 0 {
		recent.AddText("No Recent Items", nil, nil).Disable()
		return
	}
	for _, item := range items {
		route := item.Route // capture per iteration
		label := item.Name
		if label == "" {
			label = item.ID
		}
		recent.AddText(label, nil, func(_ *menu.CallbackData) {
			if route != "" {
				a.emitOrQueueRendererEvent(eventOpenRecent, route)
			}
		})
	}
	recent.AddSeparator()
	recent.AddText("Clear Menu", nil, func(_ *menu.CallbackData) {
		_ = a.ClearRecentItems()
	})
}

// rebuildMenu re-applies the application menu so a changed "Open Recent"
// submenu is reflected. It is deliberately a no-op on every platform; see
// supportsDynamicApplicationMenuForOS.
func (a *App) rebuildMenu() {
	// Intentionally static for this Wails generation. Recents continue updating
	// in the dashboard and are reflected in the native menu on the next launch.
}

// reload is View > Reload. It asks a page holding unsaved work first, as a
// close does (see reloadWithRendererGuard).
func (a *App) reload(_ *menu.CallbackData) {
	if ctx := a.runtimeContext(); ctx != nil {
		a.reloadWithRendererGuard(ctx)
	}
}

var windowReloadApp = wailsruntime.WindowReloadApp

// printWindow opens the native print dialog for the current view (run reports,
// dashboards). Wails exposes WindowPrint directly.
func (a *App) printWindow(_ *menu.CallbackData) {
	if ctx := a.runtimeContext(); ctx != nil {
		wailsruntime.WindowPrint(ctx)
	}
}

func (a *App) quit(_ *menu.CallbackData) {
	if ctx := a.runtimeContext(); ctx != nil {
		wailsruntime.Quit(ctx)
	}
}
