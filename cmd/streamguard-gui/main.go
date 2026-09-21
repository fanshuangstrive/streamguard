package main

import (
	"embed"
	"flag"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// 版本信息，由构建脚本通过 -ldflags "-X main.version=... -X main.commit=..." 注入。
// 未注入时保留默认值，保证 go run / 直接编译也能正常工作。
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	// 支持 -config 指定配置文件；为空时自动查找 config.local.json。
	configPath := flag.String("config", "", "path to config file (JSON); auto-discovers config.local.json if empty")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		println("streamguard-gui " + version + " (" + commit + ")")
		return
	}

	// Create an instance of the app structure
	app := NewApp(*configPath)

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "StreamGuard - 大模型 API 限流代理",
		Width:     1100,
		Height:    760,
		MinWidth:  900,
		MinHeight: 600,
		// 启动即最大化，避免小屏下按钮展示不全。
		StartHidden:      false,
		WindowStartState: options.Maximised,
		// 使用无边框窗口，由前端自绘标题栏（含最小化/最大化/关闭按钮）。
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 18, G: 20, B: 26, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
