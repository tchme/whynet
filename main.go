package main

import (
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"whynet/internal/etw"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if ran, err := etw.HelperMain(os.Args[1:]); ran { // the elevated helper (UDP, frames): no window
		if err != nil {
			os.Exit(1)
		}
		return
	}
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
		BackgroundColour: &options.RGBA{R: 0x16, G: 0x1d, B: 0x24, A: 255}, // the page's own background: no white flash
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		Windows:          &windows.Options{Theme: windows.Dark},      // dark title bar too, whatever the Windows theme
		DragAndDrop:      &options.DragAndDrop{EnableFileDrop: true}, // a browser record (.har) dropped on the window
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
