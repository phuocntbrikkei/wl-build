package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"

	"client/internal/browser"
	"client/internal/proxywatch"
	"client/internal/singleinstance"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func initLogging() *os.File {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	appDir := filepath.Join(configDir, "SimpleCare")
	_ = os.MkdirAll(appDir, 0755)
	logFile, err := os.OpenFile(filepath.Join(appDir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		log.SetOutput(logFile)
		log.Println("--- Client App Started ---")
	}
	return logFile
}

func main() {
	// Chế độ watchdog (được app chính spawn): chỉ canh tiến trình cha và gỡ proxy
	// khi cha chết, KHÔNG mở app/không giữ single-instance lock. Phải rẽ nhánh sớm.
	if ppid, ok := proxywatch.ParentPID(); ok {
		proxywatch.Run(ppid)
		return
	}
	// Chế độ trình duyệt tích hợp (Client spawn khi sinh viên bấm "Trình duyệt"):
	// cửa sổ riêng, không giữ single-instance lock.
	if browser.IsChild() {
		browser.Run()
		return
	}

	if err := singleinstance.Acquire(); err != nil {
		os.Exit(0)
	}

	lf := initLogging()
	if lf != nil {
		defer lf.Close()
	}

	// Create an instance of the app structure
	app := NewApp()

	appMenu := menu.NewMenu()
	fileMenu := appMenu.AddSubmenu("Rikkei Lms Connect")
	fileMenu.AddText("Về trang chính", keys.CmdOrCtrl("h"), func(_ *menu.CallbackData) {
		app.ReturnToDashboard()
	})
	fileMenu.AddText("Làm mới trang (F5)", keys.Key("f5"), func(_ *menu.CallbackData) {
		app.ReloadExamPage()
	})
	fileMenu.AddText("Xóa cache trình duyệt", keys.CmdOrCtrl("Delete"), func(_ *menu.CallbackData) {
		app.ClearExamBrowserCache()
	})

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "Rikkei Lms Connect " + versionLabel() + " — Rikkei Education",
		Width:     720,
		Height:    480,
		MinWidth:  640,
		MinHeight: 440,
		Menu:      appMenu,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			return app.HandleBeforeClose()
		},
		Windows: &windows.Options{
			WebviewUserDataPath: app.webviewDataPath,
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
