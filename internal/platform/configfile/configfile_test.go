package configfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// C-CFG-1：文件不存在 → ErrNotFound（调用方据此进入"首次运行引导"分支）。
func TestLoad_MissingFileIsNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// C-CFG-2：合法配置按字段加载。
func TestLoad_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"baseUrl":"https://gw/v1","apiKey":"sk-x","model":"m1","workspace":"C:\\proj"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.BaseURL != "https://gw/v1" || f.APIKey != "sk-x" || f.Model != "m1" || f.Workspace != `C:\proj` {
		t.Fatalf("loaded = %+v", f)
	}
}

// C-CFG-3：JSON 损坏 → 报错且指明路径（用户能定位文件）。
func TestLoad_InvalidJSONMentionsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want mentions %s", err, path)
	}
}

// C-CFG-4：模板生成；已存在则不覆盖（防抹掉用户填好的 key）。
func TestWriteTemplate_NoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	if err := WriteTemplate(path); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("template must be valid JSON: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"apiKey":"user-filled"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteTemplate(path); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.APIKey != "user-filled" {
		t.Fatalf("template overwrote user config: %+v", f)
	}
}

// C-CFG-5：env 覆盖文件值（开发便利），未设置的字段保留文件值。
func TestMerge_EnvOverridesFile(t *testing.T) {
	file := File{BaseURL: "file-url", APIKey: "file-key", Model: "file-model", Workspace: `C:\file-ws`}
	env := map[string]string{
		"TIANCODE_API_KEY":   "env-key",
		"TIANCODE_WORKSPACE": `C:\env-ws`,
	}
	got := Merge(file, func(k string) string { return env[k] })
	if got.APIKey != "env-key" || got.Workspace != `C:\env-ws` {
		t.Fatalf("env did not override: %+v", got)
	}
	if got.BaseURL != "file-url" || got.Model != "file-model" {
		t.Fatalf("unset env must keep file values: %+v", got)
	}
}

// C-CFG-6：数据目录固定到用户级路径（与安装目录解耦，卸载不删用户数据）。
func TestDataDir_UnderAppData(t *testing.T) {
	t.Setenv("APPDATA", `C:\Users\x\AppData\Roaming`)
	dir := DataDir()
	want := filepath.Join(`C:\Users\x\AppData\Roaming`, "tiancode", "sessions")
	if dir != want {
		t.Fatalf("DataDir() = %q, want %q", dir, want)
	}
}

// DefaultPath 落在 %APPDATA%\tiancode\config.json。
func TestDefaultPath_UnderAppData(t *testing.T) {
	t.Setenv("APPDATA", `C:\Users\x\AppData\Roaming`)
	want := filepath.Join(`C:\Users\x\AppData\Roaming`, "tiancode", "config.json")
	if got := DefaultPath(); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

// C-CFG-7：容忍 UTF-8 BOM（记事本另存为 UTF-8 的默认产物）。
func TestLoad_ToleratesUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := []byte(`{"baseUrl":"u","apiKey":"k","model":"m","workspace":"w"}`)
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, body...)
	if err := os.WriteFile(path, withBOM, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatalf("BOM 配置必须可解析（记事本默认行为）：%v", err)
	}
	if f.Model != "m" {
		t.Fatalf("loaded = %+v", f)
	}
}

// 首启零配置：首次运行必须能直接启动，而不是"先手改配置文件再启动"。
func TestEnsureDefault_CreatesUsableConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	f, created, err := EnsureDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("首次运行应创建配置")
	}
	if err := Validate(f); err != nil {
		t.Fatalf("默认配置必须直接可用：%v", err)
	}
	// 工作区必须真实存在：否则启动即失败，等于没消灭门槛
	st, err := os.Stat(f.Workspace)
	if err != nil || !st.IsDir() {
		t.Fatalf("默认工作区应为已存在目录：%q err=%v", f.Workspace, err)
	}
	// 必须是配置目录内的工作区（不猜用户主目录/文档目录）
	if filepath.Dir(f.Workspace) != filepath.Clean(dir) {
		t.Fatalf("默认工作区应位于配置目录内：%q", f.Workspace)
	}
	// 不得写入模板占位符（占位符会被拒迁/误导用户）
	if f.BaseURL != "" || f.APIKey != "" || f.Model != "" {
		t.Fatalf("默认配置不应含网关占位符：%+v", f)
	}
	// 落盘后可被 Load 读回（不是只在内存里对）
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("生成的文件必须能被 Load 读回：%v", err)
	}
	if loaded.Workspace != f.Workspace {
		t.Fatalf("落盘内容不一致：%+v", loaded)
	}

	// 幂等且不覆盖：用户改过的配置必须保留
	if err := os.WriteFile(path, []byte(`{"workspace":"C:\\custom-ws"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f2, created2, err := EnsureDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("已存在时不得报告为已创建")
	}
	if f2.Workspace != `C:\custom-ws` {
		t.Fatalf("不得覆盖既有配置：%+v", f2)
	}
}

// Validate：只要求 workspace（渠道信息由 channels.json 持有）。
func TestValidate_OnlyWorkspaceRequired(t *testing.T) {
	err := Validate(File{BaseURL: "u", Model: "m"})
	if err == nil {
		t.Fatal("missing workspace must fail validation")
	}
	if !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("err = %v, want mentions workspace", err)
	}
	// 仅 workspace 即可通过：网关信息是可选迁移来源（用户可能只在应用内配渠道）
	if err := Validate(File{Workspace: "w"}); err != nil {
		t.Fatalf("workspace-only config must pass: %v", err)
	}
}
