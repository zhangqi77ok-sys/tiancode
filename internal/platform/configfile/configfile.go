// Package configfile 负责用户级配置文件的定位、加载与模板生成（组合根的配置来源）。
//
// 做什么：读取 %APPDATA%\tiancode\config.json；不存在时生成模板供用户填写。
// 被谁依赖：main.go（组合根装配）。
// 依赖谁：仅 stdlib。
//
// 为什么以文件为主、环境变量为覆盖：安装版从开始菜单启动**没有环境变量**——
// 旧方案（env-only）导致"装完点开没反应"（M5 复现实证 exit=1）。
// env 保留为覆盖通道，便于开发与脚本化部署（见 ADR-0006）。
package configfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound 表示配置文件不存在（调用方据此进入首次运行引导）。
var ErrNotFound = errors.New("config file not found")

// appDirName 是用户级目录名（%APPDATA%\tiancode）。
const appDirName = "tiancode"

// File 是用户配置（字段与 config.json 一一对应）。
type File struct {
	BaseURL   string `json:"baseUrl"`   // OpenAI 兼容网关（含 /v1）
	APIKey    string `json:"apiKey"`    // 供应商密钥（只存本机）
	Model     string `json:"model"`     // 默认模型
	Workspace string `json:"workspace"` // agent 工作区（fs/shell/git 受控范围）
}

// DefaultPath 返回配置路径 %APPDATA%\tiancode\config.json。
func DefaultPath() string { return filepath.Join(appDataDir(), "config.json") }

// DataDir 返回会话账本目录 %APPDATA%\tiancode\sessions。
// 为什么固定在用户目录：与安装目录解耦——卸载不删用户数据，
// 也不会把会话写进安装目录（旧实现 DataDir="data" 相对 cwd，M5 复现时踩到）。
func DataDir() string { return filepath.Join(appDataDir(), "sessions") }

// appDataDir 返回 %APPDATA%\tiancode（APPDATA 缺失时退化到系统临时目录）。
func appDataDir() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, appDirName)
}

// Load 读取并解析配置；文件不存在时错误包装 ErrNotFound。
// 容忍 UTF-8 BOM：Windows 记事本另存为 UTF-8 会带 BOM，直接解析会失败（实测踩到）。
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return File{}, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // 去 UTF-8 BOM
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("配置解析失败 %s：%w", path, err)
	}
	return f, nil
}

// WriteTemplate 生成配置模板；已存在则不覆盖（防抹掉用户已填的密钥）。
func WriteTemplate(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tpl := File{
		BaseURL:   "https://your-gateway.example/v1",
		APIKey:    "sk-替换为你的密钥",
		Model:     "your-model",
		Workspace: filepath.Join(os.Getenv("USERPROFILE"), "projects"),
	}
	data, err := json.MarshalIndent(tpl, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// Merge 用环境变量覆盖文件值（getenv 注入以便测试）。
// 优先级：环境变量 > 配置文件。
func Merge(file File, getenv func(string) string) File {
	override := func(envKey, cur string) string {
		if v := getenv(envKey); v != "" {
			return v
		}
		return cur
	}
	return File{
		BaseURL:   override("TIANCODE_BASE_URL", file.BaseURL),
		APIKey:    override("TIANCODE_API_KEY", file.APIKey),
		Model:     override("TIANCODE_MODEL", file.Model),
		Workspace: override("TIANCODE_WORKSPACE", file.Workspace),
	}
}

// Validate 校验必填项，返回人类可读的缺失说明（供首次运行引导展示）。
func Validate(f File) error {
	var missing []string
	if f.BaseURL == "" {
		missing = append(missing, "baseUrl")
	}
	if f.APIKey == "" {
		missing = append(missing, "apiKey")
	}
	if f.Model == "" {
		missing = append(missing, "model")
	}
	if f.Workspace == "" {
		missing = append(missing, "workspace")
	}
	if len(missing) > 0 {
		return fmt.Errorf("配置缺少字段：%s", strings.Join(missing, "、"))
	}
	return nil
}
