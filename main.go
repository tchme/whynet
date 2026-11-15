package main

import (
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app, err := NewApp()
	if err != nil {
		println("whynet:", err.Error())
		os.Exit(1)
	}
	err = wails.Run(&options.App{
		Title:            "whynet",
		Width:            860,
		Height:           150, // starts as an address bar; the page grows it to fit each result
		MinWidth:         560,
		MinHeight:        120,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		OnStartup:        app.startup,
		Bind:             []interface{}{app},
		Windows:          &windows.Options{Theme: windows.SystemDefault},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
