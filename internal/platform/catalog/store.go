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
