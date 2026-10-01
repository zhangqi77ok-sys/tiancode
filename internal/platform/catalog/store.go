// Package catalog 保存用户启用的 MCP 服务器与 Skill（extensions.json）。
//
// 做什么：读写本机扩展清单，供界面管理，也供对话前注入模型。
// 被谁依赖：internal/app。
// 依赖谁：stdlib、platform/configfile（配置目录）。
package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tiancode/internal/platform/atomicfile"
	"tiancode/internal/platform/configfile"
)

// Server 是一条 MCP 配置。Transport 为 stdio（本地命令）或 http（远程 URL）。
// Builtin 标记内置项：永远启用、不可删除/修改（三层锁：前端 UI、前端 store、
// 模型侧 mcp_remove；用户手改 JSON 删掉也无妨——重启 seed 幂等补回）。
type Server struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Command   string `json:"command,omitempty"`
	Args      string `json:"args,omitempty"`
	Env       string `json:"env,omitempty"`
	URL       string `json:"url,omitempty"`
	Headers   string `json:"headers,omitempty"`
	Enabled   bool   `json:"enabled"`
	Builtin   bool   `json:"builtin,omitempty"`
}

// BuiltinMCPs 返回内置 MCP（零凭证、npx 型三件套：查文档/分步推理/跨会话记忆）。
// 需要凭证（github PAT）或重量级（playwright 要下载浏览器内核）的不内置——
// 内置必须开箱即用，跑不起来的内置只会变成"每轮都带着的摆设"。
func BuiltinMCPs() []Server {
	return []Server{
		{ID: "builtin-context7", Name: "context7", Transport: "stdio", Command: "npx", Args: "-y @upstash/context7-mcp", Enabled: true, Builtin: true},
		{ID: "builtin-sequential-thinking", Name: "sequential-thinking", Transport: "stdio", Command: "npx", Args: "-y @modelcontextprotocol/server-sequential-thinking", Enabled: true, Builtin: true},
		{ID: "builtin-memory", Name: "memory", Transport: "stdio", Command: "npx", Args: "-y @modelcontextprotocol/server-memory", Enabled: true, Builtin: true},
	}
}

// Skill 是一条技能（SKILL.md 的 name / description / 正文）。
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Body        string `json:"body,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// File 是扩展清单。
type File struct {
	MCP    []Server `json:"mcp"`
	Skills []Skill  `json:"skills"`
}

// Store 读写扩展清单。
type Store struct {
	path string
	mu   sync.Mutex
}

// New 使用给定路径；空路径则用 %APPDATA%\tiancode\extensions.json。
func New(path string) *Store {
	if path == "" {
		path = filepath.Join(configfile.Dir(), "extensions.json")
	}
	return &Store{path: path}
}

// Load 读取清单。文件不存在时返回空清单。
func (s *Store) Load() (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (File, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, nil
		}
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, err
	}
	if f.MCP == nil {
		f.MCP = []Server{}
	}
	if f.Skills == nil {
		f.Skills = []Skill{}
	}
	return f, nil
}

// EnsureBuiltin 幂等地把内置 MCP 补进清单：缺哪个补哪个；清单里已有同名项
// （用户自己配置过）不劫持、不覆盖。升级老用户时自动获得内置能力（幂等补齐）。
// 无变化时也会重写一次同内容文件（Update 的语义），幂等无害。
func (s *Store) EnsureBuiltin() error {
	return s.Update(func(f *File) error {
		have := make(map[string]bool, len(f.MCP))
		for _, srv := range f.MCP {
			have[strings.ToLower(srv.Name)] = true
		}
		for _, b := range BuiltinMCPs() {
			if !have[strings.ToLower(b.Name)] {
				f.MCP = append(f.MCP, b)
			}
		}
		return nil
	})
}

// Save 原子写入清单。
func (s *Store) Save(f File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(f)
}

func (s *Store) saveLocked(f File) error {
	if f.MCP == nil {
		f.MCP = []Server{}
	}
	if f.Skills == nil {
		f.Skills = []Skill{}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFileAtomic(s.path, append(data, '\n'), 0o600)
}

// Update 在一次持锁内完成 Load → 修改 → Save（0.2.27）。
// 为什么必须原子：界面保存与 AI（ext_manage）两条写路径各自的
// Load→改→Save 并发时会互相覆盖（后写者把先写者的新增整表抹掉）。
// mutate 返回错误则中止且不写盘（校验失败用它短路）。
func (s *Store) Update(mutate func(*File) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return err
	}
	if err := mutate(&f); err != nil {
		return err
	}
	return s.saveLocked(f)
}
