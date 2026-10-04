// Package autorun 保存"自主续跑"的开关设置（autorun.json，与 tones.json 同目录）。
//
// 为什么单独一个文件：自主续跑是**执行策略**开关（要不要让模型自己决定"是否继续"），
// 既不是渠道连接、也不是回答语气、更不是喂料，与现有任何存储的语义都对不上。
// 它只存一个数字——允许自主续几段。缺文件 = 0（关闭）：这是保守默认，
// "决策权交给模型"的功能必须由用户显式开启（ADR-0009）。
package autorun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"tiancode/internal/platform/configfile"
)

// MaxSegments 是硬上限：配置被手改成天文数字时也不放大失控面。
// 它是"决策权交给模型"之后的唯一刹车——额度必须有上界。
const MaxSegments = 3

// File 是 autorun.json 的内容（字段与文件一一对应）。
type File struct {
	Segments int `json:"segments"` // 0 = 关闭（默认）；>0 = 允许自主续跑的最大段数
}

// Defaults 是缺文件时的设置：{"segments":0}，即关闭。
func Defaults() File { return File{Segments: 0} }

// Store 读写自主续跑设置。
type Store struct {
	path string
	mu   sync.Mutex
}

// New 使用给定路径；空路径则用 %APPDATA%\tiancode\autorun.json。
func New(path string) *Store {
	if path == "" {
		path = filepath.Join(configfile.Dir(), "autorun.json")
	}
	return &Store{path: path}
}

// Resolve 读出可直接注入内核的额度（每轮调用方读一次快照）。
// 缺文件回落 0；负数归零；超上限夹到 MaxSegments。
// 解析失败**返回错误**而不是静默当 0——坏文件必须可见（见包注释）。
func (s *Store) Resolve() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取自主续跑设置失败：%w", err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return 0, fmt.Errorf("自主续跑设置不是合法 JSON（%s）：%w", s.path, err)
	}
	switch {
	case f.Segments <= 0:
		return 0, nil
	case f.Segments > MaxSegments:
		return MaxSegments, nil
	default:
		return f.Segments, nil
	}
}
