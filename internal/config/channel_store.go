package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ChannelConfig 真实的渠道配置结构体
type ChannelConfig struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Primary     bool              `json:"primary"`
	Status      string            `json:"status"` // "online", "standby", "error"
	AuthType    string            `json:"auth_type"` // "api_key", "refresh_token", "azure", "none"
	Protocol    string            `json:"protocol,omitempty"` // "openai", "anthropic", "gemini", "ollama", "azure"
	Endpoint    string            `json:"endpoint"`
	APIKey      string            `json:"api_key,omitempty"`
	APIKeyEnc   string            `json:"api_key_enc,omitempty"`
	Model       string            `json:"model"`
	Latency     string            `json:"latency"` // e.g. "85ms"
	ExtraModels []string          `json:"extra_models,omitempty"`
	UpdatedAt   int64             `json:"updated_at"`
	ExtraConfig map[string]string `json:"extra_config,omitempty"`
}

// ChannelStore 真实的渠道磁盘存储管理器
type ChannelStore struct {
	mu       sync.RWMutex
	filePath string
	channels []ChannelConfig
}

// NewChannelStore 实例化存储，默认保存在用户主目录 ~/.tiancode/channels.json
func NewChannelStore() (*ChannelStore, error) {
	dir := UserDataDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("cannot create config dir [%s]: %w", dir, err)
	}

	store := &ChannelStore{
		filePath: filepath.Join(dir, "channels.json"),
		channels: make([]ChannelConfig, 0),
	}

	_ = store.load()
	return store, nil
}

func (s *ChannelStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	var list []ChannelConfig
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}

	for i := range list {
		// 历史老数据平滑迁移兼容
		if list[i].Protocol == "" {
			switch list[i].AuthType {
			case "anthropic", "gemini", "ollama", "azure":
				list[i].Protocol = list[i].AuthType
				if list[i].AuthType == "ollama" {
					list[i].AuthType = "none"
				} else {
					list[i].AuthType = "api_key"
				}
			default:
				list[i].Protocol = "openai"
				if list[i].AuthType == "bearer_token" || list[i].AuthType == "" {
					list[i].AuthType = "api_key"
				}
			}
		}
		if list[i].AuthType == "" {
			list[i].AuthType = "api_key"
		}
		if list[i].ExtraConfig == nil {
			list[i].ExtraConfig = make(map[string]string)
		}

		if list[i].APIKeyEnc != "" {
			plain, err := UnprotectSecret(list[i].APIKeyEnc)
			if err == nil && plain != "" {
				list[i].APIKey = plain
			}
		}
		list[i].APIKeyEnc = ""
	}
	s.channels = list
	return nil
}

func (s *ChannelStore) save() error {
	disk := make([]ChannelConfig, len(s.channels))
	for i, ch := range s.channels {
		enc, err := ProtectSecret(ch.APIKey)
		if err != nil {
			return err
		}
		ch.APIKeyEnc = enc
		ch.APIKey = ""
		disk[i] = ch
	}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteConfig(s.filePath, data)
}

// List 获取全部渠道列表
func (s *ChannelStore) List() []ChannelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]ChannelConfig, len(s.channels))
	copy(out, s.channels)
	return out
}

// Save 保存或更新渠道
func (s *ChannelStore) Save(ch ChannelConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch.UpdatedAt = time.Now().Unix()
	if ch.ID == "" {
		ch.ID = fmt.Sprintf("ch_%d", time.Now().UnixNano())
	}
	if ch.Protocol == "" {
		ch.Protocol = "openai"
	}
	if ch.AuthType == "" {
		ch.AuthType = "api_key"
	}
	if ch.ExtraConfig == nil {
		ch.ExtraConfig = make(map[string]string)
	}

	if IsMaskedAPIKey(ch.APIKey) {
		for _, item := range s.channels {
			if item.ID == ch.ID {
				ch.APIKey = item.APIKey
				break
			}
		}
	}

	// 如果设为主通道，把其他通道的 primary 取消
	if ch.Primary {
		for i := range s.channels {
			s.channels[i].Primary = false
		}
	}

	found := false
	for i, item := range s.channels {
		if item.ID == ch.ID {
			s.channels[i] = ch
			found = true
			break
		}
	}

	if !found {
		s.channels = append(s.channels, ch)
	}

	return s.save()
}

// Delete 删除渠道
func (s *ChannelStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	newList := make([]ChannelConfig, 0, len(s.channels))
	for _, item := range s.channels {
		if item.ID != id {
			newList = append(newList, item)
		}
	}
	s.channels = newList
	return s.save()
}

// GetChannelForModel 根据请求的模型名称路由最匹配的渠道
func (s *ChannelStore) GetChannelForModel(reqModel string) *ChannelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if reqModel == "" {
		return s.getPrimaryLocked()
	}

	// 1. 精确匹配主模型
	for _, ch := range s.channels {
		if ch.Status != "error" && ch.Model == reqModel {
			c := ch
			return &c
		}
	}
	// 2. 匹配备用模型列表 (ExtraModels)
	for _, ch := range s.channels {
		if ch.Status != "error" {
			for _, em := range ch.ExtraModels {
				if em == reqModel {
					c := ch
					return &c
				}
			}
		}
	}
	// 3. Fallback 到主渠道
	return s.getPrimaryLocked()
}

func (s *ChannelStore) getPrimaryLocked() *ChannelConfig {
	for _, ch := range s.channels {
		if ch.Primary {
			c := ch
			return &c
		}
	}
	if len(s.channels) > 0 {
		c := s.channels[0]
		return &c
	}
	return nil
}

// GetPrimary 获取当前主用渠道
func (s *ChannelStore) GetPrimary() *ChannelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getPrimaryLocked()
}

// Get 按 ID 取完整凭据（内存明文）。
func (s *ChannelStore) Get(id string) *ChannelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ch := range s.channels {
		if ch.ID == id {
			c := ch
			return &c
		}
	}
	return nil
}

// ListMasked 给 UI：密钥打码。
func (s *ChannelStore) ListMasked() []ChannelConfig {
	list := s.List()
	for i := range list {
		if list[i].AuthType != "none" {
			list[i].APIKey = MaskAPIKey(list[i].APIKey)
		}
		list[i].APIKeyEnc = ""
	}
	return list
}
