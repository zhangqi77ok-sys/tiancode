package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tiancode/internal/core/sandbox"
	"tiancode/internal/host"
	transporthttp "tiancode/internal/transport/http"
	"tiancode/plugins/provider/openai"
	fstool "tiancode/plugins/tool/fs"
	gittool "tiancode/plugins/tool/git"
	searchtool "tiancode/plugins/tool/search"
	terminaltool "tiancode/plugins/tool/terminal"
)

const (
	Version = "0.0.1-DEV"
	Banner  = `
 _____               _        ____                                   
|_   _|__ ___   __| | ___  |  _ \  __ _  ___ _ __ ___   ___  _ __  
  | |/ __/ _ \ / _` + "`" + ` |/ _ \ | | | |/ _` + "`" + ` |/ _ \ '_ ` + "`" + ` _ \ / _ \| '_ \ 
  | | (_| (_) | (_| |  __/ | |_| | (_| |  __/ | | | | | (_) | | | |
  |_|\___\___/ \__,_|\___| |____/ \__,_|\___|_| |_| |_|\___/|_| |_|
               Tcode Micro-Kernel Daemon (Go Edition)
`
)

func main() {
	fmt.Print(Banner)
	fmt.Printf("[Tcode] Starting Micro-Kernel Daemon v%s...\n", Version)

	workspaceRoot, _ := os.Getwd()

	// 1. 初始化文件物理沙箱与 Git Plumbing 管道快照管理器
	sb, err := sandbox.NewSandbox(workspaceRoot)
	if err != nil {
		fmt.Printf("[Tcode] Sandbox init error: %v\n", err)
	}
	sm := sandbox.NewSnapshotManager(workspaceRoot)

	// 2. 初始化插件宿主引擎与注册核心 Provider & Tool 算子
	reg := host.NewRegistry()
	openaiProv := openai.NewProvider()
	_ = reg.Register(openaiProv)

	fsT := fstool.NewTool(sb, sm)
	_ = reg.Register(fsT)

	searchT := searchtool.NewTool(sb)
	_ = reg.Register(searchT)

	gitT := gittool.NewTool(workspaceRoot)
	_ = reg.Register(gitT)

	termT := terminaltool.NewTool(workspaceRoot)
	_ = reg.Register(termT)

	fmt.Printf("[Tcode] Registered plugin: [%s] (%s)\n", openaiProv.ID(), openaiProv.Name())
	fmt.Printf("[Tcode] Registered plugin: [%s] (%s)\n", fsT.ID(), fsT.Name())
	fmt.Printf("[Tcode] Registered plugin: [%s] (%s)\n", searchT.ID(), searchT.Name())
	fmt.Printf("[Tcode] Registered plugin: [%s] (%s)\n", gitT.ID(), gitT.Name())
	fmt.Printf("[Tcode] Registered plugin: [%s] (%s)\n", termT.ID(), termT.Name())

	// 3. 启动本地环回 HTTP/SSE 服务 (127.0.0.1:8765)
	srv := transporthttp.NewServer("127.0.0.1:8765", reg, sm, sb)
	go func() {
		fmt.Println("[Tcode] HTTP/SSE Server listening on http://127.0.0.1:8765")
		if err := srv.Start(); err != nil && err.Error() != "http: Server closed" {
			fmt.Printf("[Tcode] HTTP Server error: %v\n", err)
		}
	}()

	// 3. 阻塞等待优雅停机信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	fmt.Printf("\n[Tcode] Received shutdown signal: %v. Cleaning up resources...\n", sig)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()

	_ = srv.Stop(shutdownCtx)
	fmt.Println("[Tcode] Micro-Kernel gracefully stopped.")
}
