package main

import (
	"tiancode/internal/lsp"
	"tiancode/internal/mcp"

	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"os"
	"path/filepath"
	goruntime "runtime"

	"tiancode/internal/config"
	"tiancode/internal/network"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) ListChannels() []config.ChannelConfig {
	if a.channelStore == nil {
		return nil
	}
	return a.channelStore.ListMasked()
}

func (a *App) SaveChannel(cfg config.ChannelConfig) error {
	if a.channelStore == nil {
		return fmt.Errorf("channel store not initialized")
	}
	return a.channelStore.Save(cfg)
}

func (a *App) DeleteChannel(id string) error {
	if a.channelStore == nil {
		return fmt.Errorf("channel store not initialized")
	}
	return a.channelStore.Delete(id)
}

func (a *App) PingChannel(id string) (string, error) {
	if a.channelStore == nil {
		return "", fmt.Errorf("channel store not initialized")
	}
	ch := a.channelStore.Get(id)
	if ch == nil {
		return "", fmt.Errorf("channel [%s] not found", id)
	}
	latency, err := network.PingTarget(ch.Endpoint)
	if err != nil {
		return "", err
	}
	ch.Latency = latency
	_ = a.channelStore.Save(*ch)
	return latency, nil
}

func (a *App) ListMCPs() []config.MCPServerConfig {
	if a.extraStore == nil {
		return nil
	}
	return a.extraStore.ListMCPs()
}

func (a *App) SaveMCP(cfg config.MCPServerConfig) error {
	if a.extraStore == nil {
		return fmt.Errorf("extra store not initialized")
	}
	if err := a.extraStore.SaveMCP(cfg); err != nil {
		return err
	}
	// 动态联动启停 MCP 进程实例
	if a.mcpManager != nil {
		go func() {
			if cfg.Enabled {
				_ = a.mcpManager.StartServer(context.Background(), cfg)
			} else {
				_ = a.mcpManager.StopServer(context.Background(), cfg.ID)
			}
		}()
	}
	return nil
}

// DeleteMCP 从磁盘删除 MCP 配置并停止运行中的实例
func (a *App) DeleteMCP(id string) error {
	if a.extraStore == nil {
		return fmt.Errorf("extra store not initialized")
	}
	if err := a.extraStore.DeleteMCP(id); err != nil {
		return err
	}
	if a.mcpManager != nil {
		go func() {
			_ = a.mcpManager.StopServer(context.Background(), id)
		}()
	}
	return nil
}

// DiagnoseFile 触发指定文件的毫秒级轻量编译器语法诊断
func (a *App) DiagnoseFile(relPath string) (*lsp.DiagnosticReport, error) {
	return lsp.DiagnoseFile(a.workspace, relPath)
}

// TestMCPServer 对指定 MCP 服务执行标准 JSON-RPC 2.0 握手与工具探活
func (a *App) TestMCPServer(id string) (mcp.MCPTestResult, error) {
	if a.mcpManager == nil {
		return mcp.MCPTestResult{Status: "ERROR", Error: "mcp manager not initialized"}, fmt.Errorf("mcp manager not initialized")
	}
	if a.extraStore != nil {
		mcps := a.extraStore.ListMCPs()
		for _, srv := range mcps {
			if srv.ID == id || strings.Contains(strings.ToLower(srv.Name), strings.ToLower(id)) {
				return a.mcpManager.TestServer(context.Background(), srv)
			}
		}
	}
	return mcp.MCPTestResult{
		ID:     id,
		Status: "ERROR",
		Error:  fmt.Sprintf("未找到指定的 MCP 服务配置: [%s]", id),
	}, fmt.Errorf("mcp server [%s] not found", id)
}

func (a *App) ListSkills() []config.SkillConfig {
	if a.extraStore == nil {
		return nil
	}
	return a.extraStore.ListSkills()
}

func (a *App) SaveSkill(cfg config.SkillConfig) error {
	if a.extraStore == nil {
		return fmt.Errorf("extra store not initialized")
	}
	return a.extraStore.SaveSkill(cfg)
}

// ImportSkillMarkdown 解析本地 SKILL.md 或 Markdown 文件并存入技能库
func (a *App) ImportSkillMarkdown(filePath string) (*config.SkillConfig, error) {
	if a.extraStore == nil {
		return nil, fmt.Errorf("extra store not initialized")
	}
	cleanPath := filepath.Clean(strings.TrimSpace(filePath))
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read skill file: %w", err)
	}

	content := string(data)
	name := ""
	desc := ""
	prompt := content

	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "---") {
		parts := strings.SplitN(trimmed[3:], "---", 2)
		if len(parts) == 2 {
			frontmatter := parts[0]
			prompt = strings.TrimSpace(parts[1])
			for _, line := range strings.Split(frontmatter, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "name:") {
					name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
					name = strings.Trim(name, "\"'")
				} else if strings.HasPrefix(line, "description:") {
					desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
					desc = strings.Trim(desc, "\"'")
				}
			}
		}
	}

	if name == "" {
		base := filepath.Base(cleanPath)
		ext := filepath.Ext(base)
		name = strings.TrimSuffix(base, ext)
		if strings.EqualFold(name, "skill") {
			parent := filepath.Base(filepath.Dir(cleanPath))
			if parent != "" && parent != "." && parent != "/" && parent != "\\" {
				name = parent
			}
		}
	}

	cfg := config.SkillConfig{
		ID:          fmt.Sprintf("skill-%d", time.Now().UnixNano()),
		Name:        name,
		Description: desc,
		Prompt:      prompt,
		Enabled:     true,
		UpdatedAt:   time.Now().Unix(),
	}

	if err := a.extraStore.SaveSkill(cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ImportSkillFromDialog 唤起原生文件选择框并导入 SKILL.md
func (a *App) ImportSkillFromDialog() (*config.SkillConfig, error) {
	if a.ctx == nil {
		return nil, fmt.Errorf("app context not initialized")
	}
	filePath, err := wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:            "选择本地 SKILL.md 技能文件",
		DefaultDirectory: a.workspace,
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "Markdown (*.md)", Pattern: "*.md"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(filePath) == "" {
		return nil, nil
	}
	return a.ImportSkillMarkdown(filePath)
}

func (a *App) DeleteSkill(id string) error {
	if a.extraStore == nil {
		return fmt.Errorf("extra store not initialized")
	}
	return a.extraStore.DeleteSkill(id)
}

func (a *App) ListRules() []config.RuleConfig {
	if a.extraStore == nil {
		return nil
	}
	return a.extraStore.ListRules()
}

func (a *App) SaveRule(cfg config.RuleConfig) error {
	if a.extraStore == nil {
		return fmt.Errorf("extra store not initialized")
	}
	return a.extraStore.SaveRule(cfg)
}

func (a *App) DeleteRule(id string) error {
	if a.extraStore == nil {
		return fmt.Errorf("extra store not initialized")
	}
	return a.extraStore.DeleteRule(id)
}
func (a *App) GetADR(nodeID string) string {
	if a.adrStore == nil {
		return ""
	}
	return a.adrStore.Get(nodeID)
}

func (a *App) ListADR() map[string]string {
	if a.adrStore == nil {
		return map[string]string{}
	}
	return a.adrStore.List()
}

func (a *App) SaveADR(nodeID, note string) error {
	if a.adrStore == nil {
		a.adrStore = config.DefaultADRStore()
	}
	return a.adrStore.Save(nodeID, note)
}

func (a *App) FetchUpstreamModels(endpoint, apiKey string) ([]string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("未填写 endpoint，拒绝使用内置假网关地址")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		if strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1") {
			endpoint = "http://" + endpoint
		} else {
			endpoint = "https://" + endpoint
		}
	}
	if apiKey == "" && a.channelStore != nil {
		if primary := a.channelStore.GetPrimary(); primary != nil && primary.APIKey != "" {
			apiKey = primary.APIKey
		}
	}
	if apiKey == "" {
		return nil, fmt.Errorf("未配置有效 API Key，请先在渠道配置中填写模型 API Key")
	}
	cleanEndpoint := strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(cleanEndpoint, "/models") {
		cleanEndpoint = strings.TrimSuffix(cleanEndpoint, "/models")
	}
	url := cleanEndpoint + "/models"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464")
	req.Header.Set("Originator", "codex_cli_rs")
	req.Header.Set("Version", "0.101.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("上游模型网关响应错误 (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	res := make([]string, 0, len(data.Data))
	for _, m := range data.Data {
		res = append(res, m.ID)
	}
	return res, nil
}

type RuntimeInfo struct {
	Product    string `json:"product"`
	Version    string `json:"version"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	GoVersion  string `json:"go_version"`
	Workspace  string `json:"workspace"`
	DataDir    string `json:"data_dir"`
	WebView    string `json:"webview"`
}

type SandboxStatus struct {
	PathIsolation     bool `json:"path_isolation"`
	DangerousCommand  bool `json:"dangerous_command"`
	SecretStrip       bool `json:"secret_strip"`
	Workspace         string `json:"workspace"`
}

func (a *App) GetUIPrefs() config.UIPrefs {
	if a.extraStore == nil {
		return config.DefaultUIPrefs()
	}
	return a.extraStore.GetUIPrefs()
}

func (a *App) SaveUIPrefs(p config.UIPrefs) error {
	if a.extraStore == nil {
		return fmt.Errorf("extra store not initialized")
	}
	return a.extraStore.SaveUIPrefs(p)
}

func (a *App) ImportWorkspaceRules() (int, error) {
	if a.extraStore == nil {
		return 0, fmt.Errorf("extra store not initialized")
	}
	return a.extraStore.ImportWorkspaceRules(a.workspace)
}

func (a *App) GetSandboxStatus() SandboxStatus {
	return SandboxStatus{
		PathIsolation:    a.sandbox != nil,
		DangerousCommand: true,
		SecretStrip:      true,
		Workspace:        a.workspace,
	}
}

func (a *App) GetRuntimeInfo() RuntimeInfo {
	return RuntimeInfo{
		Product:   "湉码 / tiancode",
		Version:   "0.0.1",
		OS:        goruntime.GOOS,
		Arch:      goruntime.GOARCH,
		GoVersion: goruntime.Version(),
		Workspace: a.workspace,
		DataDir:   config.UserDataDir(),
		WebView:   "Microsoft Edge WebView2",
	}
}

func (a *App) ExportDiagnostics() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	desktop := filepath.Join(home, "Desktop")
	if st, err := os.Stat(desktop); err != nil || !st.IsDir() {
		desktop = home
	}
	out := filepath.Join(desktop, "tiancode-diagnostics.json")
	info := a.GetRuntimeInfo()
	payload, _ := json.MarshalIndent(map[string]any{
		"runtime":   info,
		"channels":  len(a.ListChannels()),
		"mcps":      len(a.ListMCPs()),
		"skills":    len(a.ListSkills()),
		"rules":     len(a.ListRules()),
		"sandbox":   a.GetSandboxStatus(),
	}, "", "  ")
	if err := os.WriteFile(out, payload, 0644); err != nil {
		return "", err
	}
	return out, nil
}

func (a *App) CheckForUpdates() (string, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/repos/zhangqi77ok-sys/tiancode/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tiancode")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var data struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}
	if data.TagName == "" {
		return "仓库尚无 GitHub Release；当前发货版本 0.0.1", nil
	}
	return fmt.Sprintf("最新 Release: %s %s", data.TagName, data.HTMLURL), nil
}

func (a *App) ListSkillTemplates() []config.SkillConfig {
	return config.BuiltinSkillTemplates()
}

func (a *App) InstallSkillTemplate(id string) error {
	for _, t := range config.BuiltinSkillTemplates() {
		if t.ID == id {
			return a.SaveSkill(t)
		}
	}
	return fmt.Errorf("unknown skill template %s", id)
}
