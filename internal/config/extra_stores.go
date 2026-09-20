package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MCPServerConfig MCP 服务器配置
type MCPServerConfig struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Type      string            `json:"type"` // "stdio" | "sse"
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Enabled   bool              `json:"enabled"`
	UpdatedAt int64             `json:"updated_at"`
}

// SkillConfig Agent 技能规约
type SkillConfig struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Enabled     bool   `json:"enabled"`
	UpdatedAt   int64  `json:"updated_at"`
}

// RuleConfig 项目工程规则
type RuleConfig struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Scope     string `json:"scope"` // "global" | "workspace"
	Enabled   bool   `json:"enabled"`
	UpdatedAt int64  `json:"updated_at"`
}

type ExtraStore struct {
	mu        sync.RWMutex
	baseDir   string
	mcpFile   string
	skillFile string
	ruleFile  string
	mcps      []MCPServerConfig
	skills    []SkillConfig
	rules     []RuleConfig
}

func NewExtraStore() (*ExtraStore, error) {
	dir := UserDataDir()
	_ = os.MkdirAll(dir, 0700)

	store := &ExtraStore{
		baseDir:   dir,
		mcpFile:   filepath.Join(dir, "mcp_servers.json"),
		skillFile: filepath.Join(dir, "skills.json"),
		ruleFile:  filepath.Join(dir, "rules.json"),
		mcps:      make([]MCPServerConfig, 0),
		skills:    make([]SkillConfig, 0),
		rules:     make([]RuleConfig, 0),
	}

	_ = store.loadAll()
	return store, nil
}

func (s *ExtraStore) loadAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. MCP (严禁任何假数据预置，无配置时保持纯净空列表)
	if data, err := os.ReadFile(s.mcpFile); err == nil {
		_ = json.Unmarshal(data, &s.mcps)
	} else {
		s.mcps = make([]MCPServerConfig, 0)
	}

	// 2. Skills (纯净空状态)
	if data, err := os.ReadFile(s.skillFile); err == nil {
		_ = json.Unmarshal(data, &s.skills)
	} else {
		s.skills = make([]SkillConfig, 0)
	}

	// 3. Rules (纯净空状态)
	if data, err := os.ReadFile(s.ruleFile); err == nil {
		_ = json.Unmarshal(data, &s.rules)
	} else {
		s.rules = make([]RuleConfig, 0)
	}

	return nil
}

func atomicWriteConfig(filePath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0700); err != nil {
		return err
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", filePath, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		_ = os.Remove(filePath)
		if renameErr := os.Rename(tmpPath, filePath); renameErr != nil {
			_ = os.Remove(tmpPath)
			return os.WriteFile(filePath, data, 0600)
		}
	}
	return nil
}

func (s *ExtraStore) saveMCPs() error {
	data, _ := json.MarshalIndent(s.mcps, "", "  ")
	return atomicWriteConfig(s.mcpFile, data)
}

func (s *ExtraStore) saveSkills() error {
	data, _ := json.MarshalIndent(s.skills, "", "  ")
	return atomicWriteConfig(s.skillFile, data)
}

func (s *ExtraStore) saveRules() error {
	data, _ := json.MarshalIndent(s.rules, "", "  ")
	return atomicWriteConfig(s.ruleFile, data)
}

// ListMCPs 获取 MCP 列表
func (s *ExtraStore) ListMCPs() []MCPServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MCPServerConfig, len(s.mcps))
	copy(out, s.mcps)
	return out
}

// SaveMCP 保存 MCP 配置
func (s *ExtraStore) SaveMCP(cfg MCPServerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg.UpdatedAt = time.Now().Unix()
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("mcp_%d", time.Now().UnixNano())
	}

	found := false
	for i, item := range s.mcps {
		if item.ID == cfg.ID {
			s.mcps[i] = cfg
			found = true
			break
		}
	}
	if !found {
		s.mcps = append(s.mcps, cfg)
	}
	return s.saveMCPs()
}

// DeleteMCP 从配置中删除指定 MCP
func (s *ExtraStore) DeleteMCP(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, item := range s.mcps {
		if item.ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		s.mcps = append(s.mcps[:idx], s.mcps[idx+1:]...)
		return s.saveMCPs()
	}
	return nil
}

// ListSkills 获取 Skills 列表
func (s *ExtraStore) ListSkills() []SkillConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SkillConfig, len(s.skills))
	copy(out, s.skills)
	return out
}

// SaveSkill 保存 Skill 配置
func (s *ExtraStore) SaveSkill(cfg SkillConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg.UpdatedAt = time.Now().Unix()
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("skill_%d", time.Now().UnixNano())
	}

	found := false
	for i, item := range s.skills {
		if item.ID == cfg.ID {
			s.skills[i] = cfg
			found = true
			break
		}
	}
	if !found {
		s.skills = append(s.skills, cfg)
	}
	return s.saveSkills()
}

// DeleteSkill 从配置中删除指定 Skill
func (s *ExtraStore) DeleteSkill(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, item := range s.skills {
		if item.ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		s.skills = append(s.skills[:idx], s.skills[idx+1:]...)
		return s.saveSkills()
	}
	return nil
}

// ListRules 获取规则列表
func (s *ExtraStore) ListRules() []RuleConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RuleConfig, len(s.rules))
	copy(out, s.rules)
	return out
}

// SaveRule 保存规则配置
func (s *ExtraStore) SaveRule(cfg RuleConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg.UpdatedAt = time.Now().Unix()
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("rule_%d", time.Now().UnixNano())
	}

	found := false
	for i, item := range s.rules {
		if item.ID == cfg.ID {
			s.rules[i] = cfg
			found = true
			break
		}
	}
	if !found {
		s.rules = append(s.rules, cfg)
	}
	return s.saveRules()
}

// DeleteRule 从配置中删除指定规则
func (s *ExtraStore) DeleteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, item := range s.rules {
		if item.ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		s.rules = append(s.rules[:idx], s.rules[idx+1:]...)
		return s.saveRules()
	}
	return nil
}
