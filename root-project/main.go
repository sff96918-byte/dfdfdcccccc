package main

import (
	"embed"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"chitchatbot/internal/applog"
	"chitchatbot/internal/runtimeassets"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:cmd/chitchatui/frontend/dist
var embeddedAssets embed.FS

func main() {
	dataDir, err := resolveDataDir()
	if err != nil {
		log.Fatal(err)
	}

	// Set up diagnostics before database/license/UI startup, so startup failures
	// from log.Fatal are also retained. Existing console output stays available.
	diskLogger := applog.Logger{Path: applog.Path(dataDir), Source: "wails"}
	// File first: a Windows GUI build may have an unavailable stderr handle.
	log.SetOutput(io.MultiWriter(applog.Logger{Path: diskLogger.Path, Source: "go"}, os.Stderr))
	log.SetFlags(0)
	diskLogger.Info("DESKTOP START | diagnostic logging initialized")
	capture, captureErr := applog.CaptureOutput(diskLogger.Path)
	if captureErr != nil {
		diskLogger.Error("Process diagnostics capture unavailable: " + captureErr.Error())
	} else {
		defer func() {
			if err := capture.Close(); err != nil {
				diskLogger.Error("Diagnostic stream close failed: " + err.Error())
			}
		}()
	}
	defer diskLogger.Info("DESKTOP STOP | main returned")

	dist, err := fs.Sub(embeddedAssets, "cmd/chitchatui/frontend/dist")
	if err != nil {
		log.Fatal(err)
	}

	app, err := NewApp(dataDir)
	if err != nil {
		log.Fatal(err)
	}

	err = wails.Run(&options.App{
		Title:  "ChitChat Bot",
		Logger: diskLogger,
		// Include framework info/warnings; do not enable raw RPC debug dumps that
		// could serialize settings/credentials. App logs have no severity filter.
		LogLevelProduction: logger.INFO,
		Width:              1280,
		Height:             820,
		MinWidth:           1080,
		MinHeight:          700,
		BackgroundColour:   &options.RGBA{R: 22, G: 23, B: 22, A: 255},
		AssetServer: &assetserver.Options{
			Assets: dist,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			Theme:              windows.SystemDefault,
			WebviewBrowserPath: runtimeassets.BundledWebView2Path(),
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

func resolveDataDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, "data"), nil
}
