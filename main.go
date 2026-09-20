package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"whatsweb/src/backend"
	"whatsweb/src/crypto"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Initialize encryption service
	encryptionService, err := crypto.NewEncryptionService()
	if err != nil {
		log.Fatalf("Failed to initialize encryption service: %v", err)
	}

	// Create backend app instance
	app := backend.NewApp(encryptionService)

	// Create application with options
	err = wails.Run(&options.App{
		Title:  "Whatsweb",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		OnStartup:        app.Startup,
		OnDomReady:       app.DomReady,
		OnBeforeClose:    app.BeforeClose,
		OnShutdown:       app.Shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		log.Fatal(err)
	}
}
