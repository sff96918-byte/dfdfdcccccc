package main

import (
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"chitchatbot/internal/runtimeassets"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var embeddedAssets embed.FS

func main() {
	dataDir, err := resolveDataDir()
	if err != nil {
		log.Fatal(err)
	}

	dist, err := fs.Sub(embeddedAssets, "frontend/dist")
	if err != nil {
		log.Fatal(err)
	}

	app, err := NewApp(dataDir)
	if err != nil {
		log.Fatal(err)
	}

	err = wails.Run(&options.App{
		Title:            "ChitChat Bot",
		Width:            1280,
		Height:           820,
		MinWidth:         1080,
		MinHeight:        700,
		BackgroundColour: &options.RGBA{R: 22, G: 23, B: 22, A: 255},
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
