// tiancode 是个人 Windows 桌面 AI 编程智能体（从零重建版）。
// 本文件只做组合根装配：配置加载 → 用例层构造 → Wails 生命周期；
// 任何业务逻辑不得出现在本文件（docs/ARCHITECTURE.md 分层规则：壳层禁业务）。
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	shell "tiancode/app"
	"tiancode/internal/platform/configfile"

	"tiancode/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

// version 由 release.ps1 通过 -ldflags "-X main.version=<VERSION>" 注入。
var version = "dev"

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

	// 内置 MCP 幂等补齐（启动期任务，显式编排：构造无写盘副作用，测试不碰真机数据）
	if err := chat.EnsureBuiltinMCP(); err != nil {
		shell.NotifyError("tiancode 启动失败", err.Error())
		os.Exit(1)
	}

	bind := shell.New(chat)
	err = wails.Run(&options.App{
		Title: "tiancode",
		// 无边框：标题栏由前端自绘（品牌 logo + 窗口控制按钮都在应用内），
		// 去掉系统标题栏这条"外框"，视觉上是一块完整的应用面板
		Frameless: true,
		Width:     1280,
		Height:    800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: func(ctx context.Context) {
			// 应用上下文经字段注入：绑定方法不能带 context.Context 参数
			// （Wails 的 ParseArgs 严格校验实参个数且不注入 ctx，见 app.Bind 注释）
			bind.AppCtx = ctx
			// 窗口就绪的可断言证据（排障与验收都依赖这行）
			shell.LogLifecycle(fmt.Sprintf("started v%s model=%s workspace=%s", version, cfg.Model, cfg.WorkDir))
			// 自动禁用渠道的重启恢复会改变用户上次看到的渠道状态，必须留痕（否则用户
			// 无法解释"为什么这次又能用了"）
			if revived := chat.RevivedChannels(); len(revived) > 0 {
				shell.LogLifecycle("渠道从自动禁用恢复（重启重新评估）：" + strings.Join(revived, "、"))
			}
		},
		OnShutdown: func(ctx context.Context) {
			// 显式回收（MCP 子进程 / 账本句柄 / codex 监听）：wails.Run 异常返回或
			// 启动失败的 os.Exit 路径上 defer 不执行——只靠 defer 会留下孤儿
			// node 进程（Close 幂等，重复调用安全）
			if err := chat.Close(); err != nil {
				shell.LogLifecycle("shutdown close error: " + err.Error())
			}
			shell.LogLifecycle("shutdown")
		},
		Bind: []interface{}{bind},
	})
	if err != nil {
		// os.Exit 跳过 defer：手动回收，避免孤儿 MCP 进程
		if cerr := chat.Close(); cerr != nil {
			shell.LogLifecycle("close after run error: " + cerr.Error())
		}
		shell.NotifyError("tiancode 运行错误", err.Error())
		os.Exit(1)
	}
}

// loadConfig 装配配置：配置文件为主、环境变量覆盖（开发/脚本化部署用）。
// 首次运行（文件不存在）生成模板并给出明确指引，绝不静默退出。
func loadConfig(path string) (app.Config, error) {
	// 首启零配置：文件不存在时生成可直接启动的默认配置并**继续启动**——
	// 旧行为是"生成模板后报错退出"，等于装完点开就没反应（最硬的门槛）。
	file, created, err := configfile.EnsureDefault(path)
	if err != nil {
		return app.Config{}, err
	}
	if created {
		shell.LogLifecycle(fmt.Sprintf(
			"first run: generated default config %s (workspace=%s)", path, file.Workspace))
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
		// 默认无工作区（纯对话）：工作区由用户在 UI 里显式进入（顶栏选择 / 侧栏空间组 ＋）。
		// 配置里的 workspace 不再开机自动应用——"默认没有选择工作区，选了才归属"；
		// 需要固定工作区的场景由用户每次进入（会话归属仍以首条消息落账时的快照为准）。
		WorkDir: "",
	}, nil
}
