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
	"sync"

	"tiancode/internal/platform/atomicfile"
	"tiancode/internal/platform/configfile"
)

// Server 是一条 MCP 配置。Transport 为 stdio（本地命令）或 http（远程 URL）。
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

// Save 原子写入清单。
func (s *Store) Save(f File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
