package main

import (
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

// App struct binds App in the main package for Wails
type App struct {
	*backend.App
}

func main() {
	// Initialize encryption service
	encryptionService, err := crypto.NewEncryptionService()
	if err != nil {
		log.Fatalf("Failed to initialize encryption service: %v", err)
	}

	// Create backend app instance
	backendApp := backend.NewApp(encryptionService)
	mainApp := &App{App: backendApp}

	// Create application with options
	err = wails.Run(&options.App{
		Title:  "Whatsweb",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		OnStartup:        backendApp.Startup,
		OnDomReady:       backendApp.DomReady,
		OnBeforeClose:    backendApp.BeforeClose,
		OnShutdown:       backendApp.Shutdown,
		Bind: []interface{}{
			backendApp,
			mainApp,
		},
	})

	if err != nil {
		log.Fatal(err)
	}
}
