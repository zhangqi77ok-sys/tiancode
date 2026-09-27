// Package channels 是渠道配置的存储适配器（channels.json）。
//
// 做什么：读写用户级渠道配置（渠道列表 + 激活渠道），持久化走原子写；
// 提供从旧 config.json 的一次性迁移。
// 被谁依赖：main.go（组合根）/ internal/app（用例层经其接口取配置）。
// 依赖谁：core/llm（渠道类型）、platform/atomicfile、platform/configfile、stdlib。
//
// 为什么用 JSON 文件而非 SQLite：单用户桌面工具，渠道数量个位数，
// 引入数据库是负资产（YAGNI）；原子写已足够防半写损坏。
package channels

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/atomicfile"
	"tiancode/internal/platform/configfile"
)

// Config 是渠道配置全集（channels.json 的结构）。
// ApprovalTools 是"用户设置"的一部分（审批策略）：空 = 审批关闭（ADR-0007 默认关）。
// 为什么会放在这里：本文件是当前唯一的用户级可变配置存储（原子写 + 版本迁移都在此），
// 单独再造一个 settings 文件会引入第二处 IO 路径与第二套兼容逻辑。
type Config struct {
	Channels []llm.Channel `json:"channels"`
	ActiveID string        `json:"activeId"`
	// ApprovalTools 需要执行前审批的工具名（精确匹配；空 = 关闭）
	ApprovalTools []string `json:"approvalTools,omitempty"`
}

// Active 返回激活渠道；未配置激活项或找不到时返回 false。
func (c Config) Active() (llm.Channel, bool) {
	for _, ch := range c.Channels {
		if ch.ID == c.ActiveID {
			return ch, true
		}
	}
	return llm.Channel{}, false
}

// DefaultPath 返回渠道配置路径 %APPDATA%\tiancode\channels.json。
func DefaultPath() string { return filepath.Join(configfile.Dir(), "channels.json") }

// NewID 生成渠道标识（时间戳前缀，便于排障时判断创建顺序）。
func NewID() string { return fmt.Sprintf("ch-%d", time.Now().UnixNano()) }

// Store 是渠道配置存储；同进程内并发安全。
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore 构造存储。
func NewStore(path string) *Store { return &Store{path: path} }

// Load 读取配置；文件不存在返回空配置（不报错——调用方据此判断是否需迁移）。
func (s *Store) Load() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (Config, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("渠道配置解析失败 %s：%w", s.path, err)
	}
	return cfg, nil
}

// Save 原子写入配置（C-CH-5：temp + fsync + rename，崩溃不留半文件）。
func (s *Store) Save(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg.Channels == nil {
		// 保证序列化为 [] 而非 null（前端遍历更安全）
		cfg.Channels = []llm.Channel{}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFileAtomic(s.path, append(data, '\n'), 0o600)
}

// placeholderHints 是配置模板里的占位符特征（模板值必须原样保留在文档里，
// 但绝不能变成"真渠道"——实机事故：首启迁移出 your-gateway.example 假渠道，
// 用户看到"有渠道"却连不通，比没有渠道更难排查）。
var placeholderHints = []string{"your-", "placeholder", ".example", "change-me", "sk-xxxx"}

// looksLikePlaceholder 判断配置项是否为模板占位符（大小写不敏感）。
func looksLikePlaceholder(values ...string) bool {
	for _, v := range values {
		lower := strings.ToLower(strings.TrimSpace(v))
		for _, hint := range placeholderHints {
			if strings.Contains(lower, hint) {
				return true
			}
		}
	}
	return false
}

// MigrateFromConfig 由旧 config.json 生成首个渠道（C-CH-1：首次运行免二次配置）。
// 返回 (配置, 是否发生迁移)；源配置缺少关键字段或是模板占位符时不迁移（返回 false），
// 由调用方走"首次运行引导"。
func MigrateFromConfig(src configfile.File) (Config, bool) {
	if src.BaseURL == "" || src.Model == "" {
		return Config{}, false
	}
	if looksLikePlaceholder(src.BaseURL, src.Model, src.APIKey) {
		return Config{}, false
	}
	// 密钥可以缺失（部分本地网关如 Ollama 不需要），其余字段必须有
	ch := llm.Channel{
		ID:       NewID(),
		Name:     "默认渠道（由配置文件迁移）",
		Protocol: llm.ProtocolOpenAI,
		BaseURL:  src.BaseURL,
		APIKey:   src.APIKey,
		Model:    src.Model,
	}
	return Config{Channels: []llm.Channel{ch}, ActiveID: ch.ID}, true
}
