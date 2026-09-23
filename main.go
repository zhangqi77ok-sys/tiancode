// tiancode 是个人 Windows 桌面 AI 编程智能体（从零重建版）。
// 本文件只做组合根装配：配置加载 → 用例层构造 → Wails 生命周期；
// 任何业务逻辑不得出现在本文件（docs/ARCHITECTURE.md 分层规则：壳层禁业务）。
package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	shell "tiancode/app"
	"tiancode/internal/platform/configfile"

	"tiancode/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	configPath := flag.String("config", configfile.DefaultPath(), "配置文件路径（默认 %APPDATA%\\tiancode\\config.json）")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		// 弹出可见错误（GUI 子系统下 println 不可见，见 app.NotifyError 注释）
		shell.NotifyError("tiancode 启动失败", err.Error())
		os.Exit(1)
	}

	chat, err := app.NewChatService(cfg)
	if err != nil {
		shell.NotifyError("tiancode 启动失败", err.Error())
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
		shell.NotifyError("tiancode 运行错误", err.Error())
		os.Exit(1)
	}
}

// loadConfig 装配配置：配置文件为主、环境变量覆盖（开发/脚本化部署用）。
// 首次运行（文件不存在）生成模板并给出明确指引，绝不静默退出。
func loadConfig(path string) (app.Config, error) {
	file, err := configfile.Load(path)
	if errors.Is(err, configfile.ErrNotFound) {
		if werr := configfile.WriteTemplate(path); werr != nil {
			return app.Config{}, fmt.Errorf("未找到配置文件，且模板生成失败：%v", werr)
		}
		return app.Config{}, fmt.Errorf(
			"首次运行：已生成配置模板，请填写后重新启动\n\n文件位置：\n%s\n\n需要填写：baseUrl（网关地址，含 /v1）、apiKey、model、workspace（工作目录）",
			path)
	}
	if err != nil {
		return app.Config{}, err
	}

	merged := configfile.Merge(file, os.Getenv)
	if err := configfile.Validate(merged); err != nil {
		return app.Config{}, fmt.Errorf("配置无效（%s）：%w", path, err)
	}
	return app.Config{
		BaseURL: merged.BaseURL,
		APIKey:  merged.APIKey,
		Model:   merged.Model,
		// 数据目录固定用户级（与安装目录解耦，卸载不删会话）
		DataDir: configfile.DataDir(),
		WorkDir: merged.Workspace,
	}, nil
}
