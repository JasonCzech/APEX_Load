package main

import (
	"io/fs"
	"log"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// Extract the "static" subdirectory from the embedded filesystem
	staticSub, err := fs.Sub(api.StaticFS, "static")
	if err != nil {
		log.Fatal("failed to load embedded static assets:", err)
	}

	// Create application menu
	AppMenu := menu.NewMenu()
	FileMenu := AppMenu.AddSubmenu("File")
	FileMenu.AddText("Quit", nil, func(_ *menu.CallbackData) {
		// Handled by default OS behaviors mostly, but we can hook if needed
	})

	// Create application with options
	err = wails.Run(&options.App{
		Title:  "APEX Load",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: staticSub,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 23, A: 255}, // Match --bg-primary
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Menu:             AppMenu,
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: true,
				HideTitle:                  true,
				HideTitleBar:               false,
				FullSizeContent:            true,
				UseToolbar:                 false,
				HideToolbarSeparator:       true,
			},
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   "APEX Load",
				Message: "Enterprise Performance Testing Platform\nCopyright 2026 Project Stormrunner",
			},
		},
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			BackdropType:         windows.Mica,
			DisableWindowIcon:    false,
		},
	})

	if err != nil {
		log.Fatal(err)
	}
}
