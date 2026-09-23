// tiancode 是个人 Windows 桌面 AI 编程智能体（从零重建版）。
// 本文件只做 Wails 生命周期装配与配置注入；任何业务逻辑不得出现在本文件
// （docs/ARCHITECTURE.md 分层规则：壳层禁业务）。
package main

import (
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	shell "tiancode/app"
	"tiancode/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// 密钥与网关只经环境变量注入（docs/STANDARDS.md §1）；首次使用前需设置：
	//   TIANCODE_BASE_URL   OpenAI 兼容网关根地址（含 /v1）
	//   TIANCODE_API_KEY    供应商密钥
	//   TIANCODE_MODEL      默认模型
	//   TIANCODE_WORKSPACE  工作区目录（fs 工具受控范围；缺省为当前目录）
	workDir := os.Getenv("TIANCODE_WORKSPACE")
	if workDir == "" {
		workDir = "."
	}
	cfg := app.Config{
		BaseURL: os.Getenv("TIANCODE_BASE_URL"),
		APIKey:  os.Getenv("TIANCODE_API_KEY"),
		Model:   os.Getenv("TIANCODE_MODEL"),
		DataDir: "data", // 运行时账本目录（.gitignore 已排除）
		WorkDir: workDir,
	}
	chat, err := app.NewChatService(cfg)
	if err != nil {
		println("config error:", err.Error())
		os.Exit(1)
	}
	defer chat.Close()

	err = wails.Run(&options.App{
		Title:  "tiancode",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		Bind: []interface{}{shell.New(chat)},
	})
	if err != nil {
		println("Error:", err.Error())
		os.Exit(1)
	}
}
