// SPDX-FileCopyrightText: 2026 Dinko Korunic <dinko.korunic@gmail.com>
// SPDX-License-Identifier: MIT

// Command app is the Nightglass GUI: a Wails v3 window around
// app/control, with the frontend in app/frontend.
package main

import (
	"embed"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	_ "time/tzdata" // IANA zones for RunInitialize on Windows

	"github.com/dkorunic/nightglass/app/control"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed frontend/index.html frontend/style.css frontend/*.js
var frontend embed.FS

// icon is Nightglass's own icon (build/icon.svg, rendered by scripts/icons.sh).
//
//go:embed build/icon.png
var icon []byte

func main() {
	// Logs, Wails' own included, also go to memory, for "Save Nightglass Log".
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, control.AppLog), nil))
	slog.SetDefault(logger)
	dir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	var app *application.App
	var window *application.WebviewWindow
	ctl, err := control.New(filepath.Join(dir, "nightglass", "settings.json"), func(name string, data any) {
		app.Event.Emit(name, data)
	})
	if err != nil {
		log.Fatal(err)
	}
	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	app = application.New(application.Options{
		Name:     "Nightglass",
		Logger:   logger,
		Icon:     icon,
		Services: []application.Service{application.NewServiceWithOptions(ctl, application.ServiceOptions{Route: "/frame/"})},
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		// One instance at a time, as the app's primary instance alone keeps
		// the telescope and the settings: a second brings the first forward.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "net.nightglass.app",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				window.Show()
				window.Focus()
			},
		},
	})
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Nightglass", Width: 1400, Height: 900, URL: "/",
	})
	ctl.Launched() // past the single-instance check: this is the instance that runs
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
