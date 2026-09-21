package main

import (
	"embed"
	"log"
	"os"
	"slices"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"parley/src/backend"
	"parley/src/crypto"
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
		Title:  "Parley",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 21, B: 24, A: 255},
		// Closing the dashboard only hides it; Parley keeps supervising sessions
		// and delivering notifications. Quit from the dashboard to exit.
		HideWindowOnClose: true,
		StartHidden:       slices.Contains(os.Args[1:], "--hidden"),
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "com.parley.app",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { backendApp.ShowWindow() },
		},
		OnStartup:  backendApp.Startup,
		OnDomReady: backendApp.DomReady,
		OnShutdown: backendApp.Shutdown,
		Bind: []interface{}{
			backendApp,
			mainApp,
		},
	})

	if err != nil {
		log.Fatal(err)
	}
}
